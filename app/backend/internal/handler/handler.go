package handler

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"sakuravel/internal/middleware"
	"sakuravel/internal/model"
	"sakuravel/internal/realtime"
	"strconv"
	"strings"
)

type Handler struct {
	DB *sql.DB
	// CookieSecure が true の場合、セッションCookieに Secure を付与する（HTTPS 配信環境向け）。
	// 単一オリジン構成のため SameSite は既定（Lax 相当）のままでよい。
	CookieSecure bool
	// Notifications はユーザーIDごと、Threads はスレッドのルート投稿IDごとの SSE 購読を管理する。
	Notifications *realtime.Hub
	Threads       *realtime.Hub
}

func (h *Handler) respondJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (h *Handler) respondError(w http.ResponseWriter, status int, msg string) {
	h.respondJSON(w, status, map[string]string{"error": msg})
}

// serverError は 500 応答の定型。応答本文は固定なので、原因はここでログにだけ残す。
// 呼び出し側は続けて return すること。
func (h *Handler) serverError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("server error", "method", r.Method, "url", r.URL.Path, "error", err)
	h.respondError(w, http.StatusInternalServerError, "server error")
}

func (h *Handler) currentUserID(r *http.Request) (int64, bool) {
	id, ok := r.Context().Value(middleware.UserIDKey).(int64)
	return id, ok
}

func (h *Handler) pagination(r *http.Request) (page, perPage, offset int) {
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	perPage, _ = strconv.Atoi(r.URL.Query().Get("per_page"))
	if perPage < 1 || perPage > 50 {
		perPage = 20
	}
	offset = (page - 1) * perPage
	return
}

// fetchUser は users テーブルから1件取得する
func (h *Handler) fetchUser(r *http.Request, userID int64) (model.User, error) {
	var u model.User
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT id, username, display_name, bio, created_at FROM users WHERE id = ?`,
		userID,
	).Scan(&u.ID, &u.Username, &u.DisplayName, &u.Bio, &u.CreatedAt)
	if err != nil {
		return u, err
	}
	u.AvatarColor = model.AvatarColor(u.ID)

	// フォロワー数・フォロー数・投稿数を取得
	h.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM follows WHERE followee_id = ?`, u.ID,
	).Scan(&u.FollowersCount)

	h.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM follows WHERE follower_id = ?`, u.ID,
	).Scan(&u.FollowingCount)

	h.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM posts WHERE user_id = ?`, u.ID,
	).Scan(&u.PostCount)

	if viewerID, ok := h.currentUserID(r); ok && viewerID != u.ID {
		h.DB.QueryRowContext(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM follows WHERE follower_id = ? AND followee_id = ?)`,
			viewerID, u.ID,
		).Scan(&u.FollowedByMe)
	}

	return u, nil
}

// fetchPost は posts テーブルから1件取得し、関連データを付加する
func (h *Handler) fetchPost(r *http.Request, postID, viewerID int64) (model.Post, error) {
	var p model.Post
	var userID int64
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT id, user_id, content, is_repost, original_post_id, parent_post_id, created_at
		 FROM posts WHERE id = ?`,
		postID,
	).Scan(&p.ID, &userID, &p.Content, &p.IsRepost, &p.OriginalPostID, &p.ParentPostID, &p.CreatedAt)
	if err != nil {
		return p, err
	}

	author, err := h.fetchUser(r, userID)
	if err != nil {
		return p, err
	}
	p.Author = author

	h.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM likes WHERE post_id = ?`, p.ID,
	).Scan(&p.LikesCount)

	h.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM reposts WHERE post_id = ?`, p.ID,
	).Scan(&p.RepostsCount)

	p.RepliesCount = h.countReplies(r, p.ID)

	if viewerID > 0 {
		h.DB.QueryRowContext(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM likes WHERE user_id = ? AND post_id = ?)`,
			viewerID, p.ID,
		).Scan(&p.LikedByMe)

		h.DB.QueryRowContext(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM reposts WHERE user_id = ? AND post_id = ?)`,
			viewerID, p.ID,
		).Scan(&p.RepostedByMe)
	}

	// 返信の場合、返信先の投稿者を解決する
	if p.ParentPostID != nil {
		var username, displayName string
		err := h.DB.QueryRowContext(r.Context(), `
			SELECT u.username, u.display_name
			FROM posts parent JOIN users u ON u.id = parent.user_id
			WHERE parent.id = ?
		`, *p.ParentPostID).Scan(&username, &displayName)
		if err == nil {
			p.ReplyToUsername = &username
			p.ReplyToDisplayName = &displayName
		}
	}

	// リポストの場合、何をリポストしたか分かるように元投稿を解決する
	if p.IsRepost && p.OriginalPostID != nil && *p.OriginalPostID != p.ID {
		if original, err := h.fetchPost(r, *p.OriginalPostID, viewerID); err == nil {
			p.OriginalPost = &original
		}
	}

	return p, nil
}

// placeholders は n 個のプレースホルダを並べた "?,?,?" 形式の文字列を返す。
// IN 句の要素数だけが動的で、値そのものは常にプレースホルダ経由で束縛する。
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// maxRepostDepth はリポストの元投稿を辿る深さの上限（相互リポスト等の循環に対する保険）。
const maxRepostDepth = 50

// fetchPostsBulk は fetchPost の一括版で、引数と同じ順序で返す（取得できなかった投稿は除く）。
// fetchPost とまったく同じ JSON を返すことが前提なので、model.Post の形や
// fetchPost / fetchUser が埋める項目を変えるときは必ず両方を同時に更新すること。
func (h *Handler) fetchPostsBulk(r *http.Request, postIDs []int64, viewerID int64) ([]model.Post, error) {
	return h.fetchPostsBulkDepth(r, postIDs, viewerID, 0)
}

// fetchPostsBulkDepth は fetchPostsBulk の実体で、depth はリポスト元投稿を辿った深さ。
// 元投稿の解決は取得済み投稿ぶんをまとめた再帰呼び出し1回で行い、
// depth が maxRepostDepth に達した時点で打ち切る。
func (h *Handler) fetchPostsBulkDepth(r *http.Request, postIDs []int64, viewerID int64, depth int) ([]model.Post, error) {
	posts := make([]model.Post, 0, len(postIDs))
	if len(postIDs) == 0 {
		return posts, nil
	}

	// followed_by_me は fetchUser と同じ条件（閲覧者がログイン済みかつ著者本人でない）で判定する。
	followerID, ok := h.currentUserID(r)
	if !ok {
		followerID = 0
	}

	args := make([]any, 0, len(postIDs)+4)
	args = append(args, viewerID, viewerID, followerID, followerID)
	for _, id := range postIDs {
		args = append(args, id)
	}

	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT p.id, p.content, p.is_repost, p.original_post_id, p.parent_post_id, p.created_at,
		       u.id, u.username, u.display_name, u.bio, u.created_at,
		       (SELECT COUNT(*) FROM likes pl WHERE pl.post_id = p.id),
		       (SELECT COUNT(*) FROM reposts pr WHERE pr.post_id = p.id),
		       EXISTS(SELECT 1 FROM likes ml WHERE ml.user_id = ? AND ml.post_id = p.id),
		       EXISTS(SELECT 1 FROM reposts mr WHERE mr.user_id = ? AND mr.post_id = p.id),
		       (SELECT COUNT(*) FROM follows fr WHERE fr.followee_id = u.id),
		       (SELECT COUNT(*) FROM follows fg WHERE fg.follower_id = u.id),
		       (SELECT COUNT(*) FROM posts up WHERE up.user_id = u.id),
		       (u.id <> ? AND EXISTS(SELECT 1 FROM follows mf WHERE mf.follower_id = ? AND mf.followee_id = u.id))
		FROM posts p JOIN users u ON u.id = p.user_id
		WHERE p.id IN (`+placeholders(len(postIDs))+`)
	`, args...)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]model.Post, len(postIDs))
	for rows.Next() {
		var p model.Post
		var u model.User
		if err := rows.Scan(
			&p.ID, &p.Content, &p.IsRepost, &p.OriginalPostID, &p.ParentPostID, &p.CreatedAt,
			&u.ID, &u.Username, &u.DisplayName, &u.Bio, &u.CreatedAt,
			&p.LikesCount, &p.RepostsCount, &p.LikedByMe, &p.RepostedByMe,
			&u.FollowersCount, &u.FollowingCount, &u.PostCount, &u.FollowedByMe,
		); err != nil {
			continue
		}
		u.AvatarColor = model.AvatarColor(u.ID)
		p.Author = u
		byID[p.ID] = p
	}
	rows.Close()

	h.applyRepliesCount(r, postIDs, byID)
	h.applyReplyTo(r, byID)

	// リポストの元投稿は、1件ずつ引かずにまとめてもう一度一括取得する。
	if depth < maxRepostDepth {
		var originalIDs []int64
		for _, p := range byID {
			if p.IsRepost && p.OriginalPostID != nil && *p.OriginalPostID != p.ID {
				originalIDs = append(originalIDs, *p.OriginalPostID)
			}
		}
		if originals, err := h.fetchPostsBulkDepth(r, originalIDs, viewerID, depth+1); err == nil {
			origByID := make(map[int64]model.Post, len(originals))
			for _, o := range originals {
				origByID[o.ID] = o
			}
			for id, p := range byID {
				if !p.IsRepost || p.OriginalPostID == nil || *p.OriginalPostID == p.ID {
					continue
				}
				if o, found := origByID[*p.OriginalPostID]; found {
					p.OriginalPost = &o
					byID[id] = p
				}
			}
		}
	}

	for _, id := range postIDs {
		if p, found := byID[id]; found {
			posts = append(posts, p)
		}
	}
	return posts, nil
}

// applyRepliesCount は byID の各投稿にぶら下がる返信の総数を1クエリでまとめて数え、代入する。
// countReplies の再帰CTEに起点となる root_id を持たせた形で、投稿数によらず発行は常に1件。
// 数えられなかった投稿の replies_count は countReplies と同じく 0 のままになる。
func (h *Handler) applyRepliesCount(r *http.Request, postIDs []int64, byID map[int64]model.Post) {
	args := make([]any, 0, len(postIDs))
	for _, id := range postIDs {
		args = append(args, id)
	}
	rows, err := h.DB.QueryContext(r.Context(), `
		WITH RECURSIVE d(root_id, id) AS (
			SELECT parent_post_id, id FROM posts WHERE parent_post_id IN (`+placeholders(len(postIDs))+`)
			UNION ALL
			SELECT d.root_id, p.id FROM posts p JOIN d ON p.parent_post_id = d.id
		)
		SELECT root_id, COUNT(*) FROM d GROUP BY root_id
	`, args...)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var rootID int64
		var count int
		if err := rows.Scan(&rootID, &count); err != nil {
			continue
		}
		if p, found := byID[rootID]; found {
			p.RepliesCount = count
			byID[rootID] = p
		}
	}
}

// applyReplyTo は返信である投稿に対して、返信先の投稿者名を1クエリでまとめて解決する。
// 返信が1件も無ければクエリは発行しない。
func (h *Handler) applyReplyTo(r *http.Request, byID map[int64]model.Post) {
	var args []any
	for id, p := range byID {
		if p.ParentPostID != nil {
			args = append(args, id)
		}
	}
	if len(args) == 0 {
		return
	}
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT child.id, u.username, u.display_name
		FROM posts child
		JOIN posts parent ON parent.id = child.parent_post_id
		JOIN users u ON u.id = parent.user_id
		WHERE child.id IN (`+placeholders(len(args))+`)
	`, args...)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var childID int64
		var username, displayName string
		if err := rows.Scan(&childID, &username, &displayName); err != nil {
			continue
		}
		if p, found := byID[childID]; found {
			p.ReplyToUsername = &username
			p.ReplyToDisplayName = &displayName
			byID[childID] = p
		}
	}
}

// maxThreadDepth はスレッドを辿る深さの上限（循環や極端に深いスレッドの保険）。
const maxThreadDepth = 50

// countReplies は投稿にぶら下がる返信の数を、ネストした返信も含めて返す。
// 再帰CTEの単一クエリで数えるので、発行クエリ数も深さの上限もスレッドの大きさに依らない。
func (h *Handler) countReplies(r *http.Request, postID int64) int {
	var total int
	err := h.DB.QueryRowContext(r.Context(), `
		WITH RECURSIVE descendants AS (
			SELECT id FROM posts WHERE parent_post_id = ?
			UNION ALL
			SELECT p.id FROM posts p JOIN descendants d ON p.parent_post_id = d.id
		)
		SELECT COUNT(*) FROM descendants
	`, postID).Scan(&total)
	if err != nil {
		return 0
	}
	return total
}

// threadRootID はスレッドの起点となる投稿IDを返す。祖先が消えて根に届かないときは、
// 同じ部分木が同じキーに解決されるよう、たどれた最上位の親IDを返す。
func (h *Handler) threadRootID(r *http.Request, postID int64) int64 {
	var rootID int64
	err := h.DB.QueryRowContext(r.Context(), `
		WITH RECURSIVE ancestors AS (
			SELECT id, parent_post_id, 0 AS depth FROM posts WHERE id = ?
			UNION ALL
			SELECT p.id, p.parent_post_id, a.depth + 1 FROM posts p JOIN ancestors a ON p.id = a.parent_post_id
		)
		SELECT COALESCE(parent_post_id, id) FROM ancestors ORDER BY depth DESC LIMIT 1
	`, postID).Scan(&rootID)
	if err != nil {	
		slog.Error("thread root lookup failed", "post", postID, "error", err, "falling back to", postID)
		return postID
	}
	return rootID
}

func pathID(r *http.Request, key string) (int64, error) {
	return strconv.ParseInt(r.PathValue(key), 10, 64)
}
