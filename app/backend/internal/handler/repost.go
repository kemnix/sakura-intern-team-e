package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// Repost は投稿をリポストする。reposts / posts / notifications への書き込みを 1 つの
// トランザクションにまとめ、コミットが成功した場合にだけ SSE を配信する。
func (h *Handler) Repost(w http.ResponseWriter, r *http.Request) {
	myID, _ := h.currentUserID(r)

	var req struct {
		PostID int64 `json:"post_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid request")
		return
	}

	// posts に外部キーが無いので、存在しない投稿へのリポストは孤児行として残る。
	var postOwnerID int64
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT user_id FROM posts WHERE id = ?`, req.PostID,
	).Scan(&postOwnerID)
	if errors.Is(err, sql.ErrNoRows) {
		h.respondError(w, http.StatusNotFound, "post not found")
		return
	}
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	notified, err := repostOnce(r.Context(), h.DB, myID, req.PostID, postOwnerID)
	if err != nil && isRetryableLockError(err) {
		// 敗者はトランザクションが丸ごと巻き戻っているのでそのまま流し直せる。ここで諦めると
		// 再試行すれば通る操作が 500 として見えてしまう。
		slog.Error("repost: lock conflict, retrying once", "user", myID, "post", req.PostID, "error", err)
		notified, err = repostOnce(r.Context(), h.DB, myID, req.PostID, postOwnerID)
	}
	if err != nil {
		h.respondWriteError(w, r, err)
		return
	}
	// イベント行は commit の後に書く（理由は notify.Publish の注記）。
	if notified {
		deliverNotification(r.Context(), h, postOwnerID, "repost", &req.PostID)
	}

	var count int
	if err := h.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM reposts WHERE post_id = ?`, req.PostID,
	).Scan(&count); err != nil {
		h.serverError(w, r, err)
		return
	}

	h.respondJSON(w, http.StatusOK, map[string]int{"reposts_count": count})
}

// repostOnce は reposts / posts / notifications への書き込みを 1 つのトランザクションで行い、
// 通知行を新しく作ったかどうかを返す。そのまま呼び直せるよう HTTP 応答には触れない。
// 通知行を同じトランザクションに入れるのは、巻き戻ったリポストの通知だけを残さないため。
func repostOnce(ctx context.Context, db *sql.DB, myID, postID, postOwnerID int64) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	// reposts は素の INSERT にして、リポスト済みかどうかを重複キーエラーで見分ける。
	_, err = tx.ExecContext(ctx,
		`INSERT INTO reposts (user_id, post_id) VALUES (?, ?)`,
		myID, postID,
	)
	firstRepost := err == nil
	if err != nil && !isDuplicateKeyError(err) {
		return false, err
	}

	// リポスト済みでも posts 行の欠落は補う。割れた状態はやり直しでしか直せないため。
	if err := ensureRepostPost(ctx, tx, myID, postID); err != nil &&
		!isDuplicateKeyError(err) {
		return false, err
	}

	// 通知は初回のリポストでだけ作る（連打による多重発火を止める）。
	notified := false
	if firstRepost {
		notified, err = insertNotificationOnce(ctx, tx, postOwnerID, "repost", myID, &postID)
		if err != nil {
			return false, err
		}
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}
	return notified, nil
}

// isDuplicateKeyError は MySQL / MariaDB の重複キーエラー（error 1062）かを判定する。
func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}

// isRetryableLockError は流し直せば通りうるロック競合の敗者かを判定する。1213 は素の
// デッドロック、1467 は AUTO_INCREMENT の採番中に敗者になった MariaDB の番号で、50 ラウンド
// × 16 並列の同時いいねの実測では 1213 が 0 件・1467 が 64 件と、1213 だけでは取りこぼす。
func isRetryableLockError(err error) bool {
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) {
		return false
	}
	return mysqlErr.Number == 1213 || mysqlErr.Number == 1467
}

// isMissingPostError は参照先の投稿が消えている外部キー違反（error 1452）かを判定する。
// users 側の外部キーも同じ番号を出すので、投稿を参照する制約に限る。
func isMissingPostError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1452 &&
		strings.Contains(mysqlErr.Message, "REFERENCES `posts`")
}

// respondWriteError は書こうとした投稿が消えていたときだけ 404 にする。
func (h *Handler) respondWriteError(w http.ResponseWriter, r *http.Request, err error) {
	if isMissingPostError(err) {
		h.respondError(w, http.StatusNotFound, "post not found")
		return
	}
	h.serverError(w, r, err)
}

// ensureRepostPost はリポスト由来の posts 行が無ければ 1 行だけ作る。検査と INSERT の間の
// 同時実行の窓を塞ぐのは本関数ではなく reposts の主キーへの INSERT で、実測でも敗者はそこで
// 待たされる。呼び出し側は重複キーエラー (1062) を成功として扱うこと。
func ensureRepostPost(ctx context.Context, tx *sql.Tx, userID, postID int64) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO posts (user_id, is_repost, original_post_id)
		 SELECT ?, TRUE, ? FROM DUAL
		 WHERE NOT EXISTS (
		     SELECT 1 FROM posts p
		     WHERE p.user_id = ? AND p.original_post_id = ?
		 )`,
		userID, postID, userID, postID,
	)
	return err
}

// UnRepost はリポストを取り消す。reposts と posts の DELETE を 1 つのトランザクションに
// まとめる。posts 行だけが残ると UI から取り消し導線が消え、二度と消せなくなるためである。
func (h *Handler) UnRepost(w http.ResponseWriter, r *http.Request) {
	myID, _ := h.currentUserID(r)
	postID, err := pathID(r, "post_id")
	if err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid post_id")
		return
	}

	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(r.Context(),
		`DELETE FROM reposts WHERE user_id = ? AND post_id = ?`, myID, postID,
	); err != nil {
		h.serverError(w, r, err)
		return
	}
	if _, err := tx.ExecContext(r.Context(),
		`DELETE FROM posts WHERE user_id = ? AND original_post_id = ? AND is_repost = TRUE`,
		myID, postID,
	); err != nil {
		h.serverError(w, r, err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.serverError(w, r, err)
		return
	}

	var count int
	if err := h.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM reposts WHERE post_id = ?`, postID,
	).Scan(&count); err != nil {
		h.serverError(w, r, err)
		return
	}

	h.respondJSON(w, http.StatusOK, map[string]int{"reposts_count": count})
}
