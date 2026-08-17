package handler

import (
	"encoding/json"
	"net/http"
)

func (h *Handler) Repost(w http.ResponseWriter, r *http.Request) {
	myID, _ := h.currentUserID(r)

	var req struct {
		PostID int64 `json:"post_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid request")
		return
	}

	h.DB.ExecContext(r.Context(),
		`INSERT INTO reposts (user_id, post_id) VALUES (?, ?)
		 ON DUPLICATE KEY UPDATE user_id = user_id`,
		myID, req.PostID,
	)
	h.DB.ExecContext(r.Context(),
		`INSERT INTO posts (user_id, is_repost, original_post_id)
		 VALUES (?, TRUE, ?)
		 ON DUPLICATE KEY UPDATE id = id`,
		myID, req.PostID,
	)

	var postOwnerID int64
	if err := h.DB.QueryRowContext(r.Context(),
		`SELECT user_id FROM posts WHERE id = ?`, req.PostID,
	).Scan(&postOwnerID); err == nil {
		createNotification(h, r, postOwnerID, "repost", myID, &req.PostID)
	}

	var count int
	h.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM reposts WHERE post_id = ?`, req.PostID,
	).Scan(&count)

	h.respondJSON(w, http.StatusOK, map[string]int{"reposts_count": count})
}

func (h *Handler) UnRepost(w http.ResponseWriter, r *http.Request) {
	myID, _ := h.currentUserID(r)
	postID, err := pathID(r, "post_id")
	if err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid post_id")
		return
	}

	h.DB.ExecContext(r.Context(),
		`DELETE FROM reposts WHERE user_id = ? AND post_id = ?`, myID, postID,
	)
	h.DB.ExecContext(r.Context(),
		`DELETE FROM posts WHERE user_id = ? AND original_post_id = ? AND is_repost = TRUE`,
		myID, postID,
	)

	var count int
	h.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM reposts WHERE post_id = ?`, postID,
	).Scan(&count)

	h.respondJSON(w, http.StatusOK, map[string]int{"reposts_count": count})
}
