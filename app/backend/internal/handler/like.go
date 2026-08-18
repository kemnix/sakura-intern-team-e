package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
)

func (h *Handler) GetLikes(w http.ResponseWriter, r *http.Request) {
	postID, err := pathID(r, "id")
	if err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid id")
		return
	}

	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT user_id FROM likes WHERE post_id = ?`, postID,
	)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var uid int64
		rows.Scan(&uid)
		ids = append(ids, uid)
	}

	var users []any
	for _, id := range ids {
		u, err := h.fetchUser(r, id)
		if err == nil {
			users = append(users, u)
		}
	}
	if users == nil {
		users = []any{}
	}
	h.respondJSON(w, http.StatusOK, map[string]any{"users": users})
}

// Like は投稿にいいねを付ける。INSERT と件数の読み取りは結果を捨てると、失敗しても 200 と
// 古い件数を返してしまう（ユーザーには成功に見える）ので両方とも検査する。
// 通知は createNotificationOnce 経由にして、いいね直しや連打で増えないようにする。
func (h *Handler) Like(w http.ResponseWriter, r *http.Request) {
	myID, _ := h.currentUserID(r)

	var req struct {
		PostID int64 `json:"post_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid request")
		return
	}

	// 外部キーが無いので、存在しない投稿へのいいねを許すと孤児行が残る。
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

	if _, err := h.DB.ExecContext(r.Context(),
		`INSERT INTO likes (user_id, post_id) VALUES (?, ?)
		 ON DUPLICATE KEY UPDATE user_id = user_id`,
		myID, req.PostID,
	); err != nil {
		h.respondWriteError(w, r, err)
		return
	}

	// 投稿者に通知
	createNotificationOnce(h, r, postOwnerID, "like", myID, &req.PostID)

	var count int
	if err := h.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM likes WHERE post_id = ?`, req.PostID,
	).Scan(&count); err != nil {
		h.serverError(w, r, err)
		return
	}

	h.respondJSON(w, http.StatusOK, map[string]int{"likes_count": count})
}

// Unlike はいいねを取り消す。DELETE と件数の読み取りは結果を捨てると、失敗しても 200 を
// 返してユーザーには取り消せたように見えるので両方とも検査する。
func (h *Handler) Unlike(w http.ResponseWriter, r *http.Request) {
	myID, _ := h.currentUserID(r)
	postID, err := pathID(r, "post_id")
	if err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid post_id")
		return
	}

	if _, err := h.DB.ExecContext(r.Context(),
		`DELETE FROM likes WHERE user_id = ? AND post_id = ?`, myID, postID,
	); err != nil {
		h.serverError(w, r, err)
		return
	}

	var count int
	if err := h.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM likes WHERE post_id = ?`, postID,
	).Scan(&count); err != nil {
		h.serverError(w, r, err)
		return
	}

	h.respondJSON(w, http.StatusOK, map[string]int{"likes_count": count})
}
