package handler

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"sakuravel/internal/realtime"
)

// CreateReply は指定した投稿への返信を作成する。返信も posts の1行として保存する。
func (h *Handler) CreateReply(w http.ResponseWriter, r *http.Request) {
	myID, _ := h.currentUserID(r)

	var req struct {
		PostID  int64  `json:"post_id"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.Content == "" || len([]rune(req.Content)) > 140 {
		h.respondError(w, http.StatusBadRequest, "content must be 1-140 characters")
		return
	}

	parentID := req.PostID
	var parentAuthorID int64
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT user_id FROM posts WHERE id = ?`, parentID,
	).Scan(&parentAuthorID)
	if err == sql.ErrNoRows {
		h.respondError(w, http.StatusNotFound, "post not found")
		return
	}
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "server error")
		return
	}

	res, err := h.DB.ExecContext(r.Context(),
		`INSERT INTO posts (user_id, content, parent_post_id) VALUES (?, ?, ?)`,
		myID, req.Content, parentID,
	)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "server error")
		return
	}
	postID, err := res.LastInsertId()
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "server error")
		return
	}

	post, _ := h.fetchPost(r, postID, myID)

	// 通知は直接の返信先の著者にのみ送る
	createNotification(h, r, parentAuthorID, "reply", myID, &postID)

	// 同じスレッドを開いている購読者へリアルタイム配信する
	h.Threads.Publish(h.threadRootID(r, parentID), realtime.Event{Type: "reply", Data: post})

	h.respondJSON(w, http.StatusCreated, map[string]any{"post": post})
}

// GetThread は対象投稿と、その祖先チェーン・返信ツリーをまとめて返す。
func (h *Handler) GetThread(w http.ResponseWriter, r *http.Request) {
	postID, err := pathID(r, "id")
	if err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid id")
		return
	}
	viewerID, _ := h.currentUserID(r)

	post, err := h.fetchPost(r, postID, viewerID)
	if err == sql.ErrNoRows {
		h.respondError(w, http.StatusNotFound, "post not found")
		return
	}
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "server error")
		return
	}

	// 祖先をたどる（古い順に並べ替えて返す）
	ancestors := make([]any, 0)
	parent := post.ParentPostID
	for depth := 0; parent != nil && depth < maxThreadDepth; depth++ {
		a, err := h.fetchPost(r, *parent, viewerID)
		if err != nil {
			break
		}
		ancestors = append([]any{a}, ancestors...)
		parent = a.ParentPostID
	}

	h.respondJSON(w, http.StatusOK, map[string]any{
		"ancestors": ancestors,
		"post":      post,
		"replies":   h.fetchReplyTree(r, postID, viewerID, 0),
	})
}

// fetchReplyTree は子返信をツリー状に取得する。
func (h *Handler) fetchReplyTree(r *http.Request, postID, viewerID int64, depth int) []any {
	nodes := make([]any, 0)
	if depth >= maxThreadDepth {
		return nodes
	}

	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT id FROM posts
		WHERE parent_post_id = ?
		ORDER BY created_at ASC, id ASC
	`, postID)
	if err != nil {
		return nodes
	}

	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()

	for _, id := range ids {
		p, err := h.fetchPost(r, id, viewerID)
		if err != nil {
			continue
		}
		nodes = append(nodes, map[string]any{
			"post":    p,
			"replies": h.fetchReplyTree(r, id, viewerID, depth+1),
		})
	}
	return nodes
}
