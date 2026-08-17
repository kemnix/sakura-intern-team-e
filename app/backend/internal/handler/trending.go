package handler

import (
	"net/http"
)

func (h *Handler) GetTrending(w http.ResponseWriter, r *http.Request) {
	myID, _ := h.currentUserID(r)

	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT p.id, p.user_id, COUNT(l.post_id) AS recent_likes
		FROM posts p
		JOIN likes l ON l.post_id = p.id
		WHERE l.created_at > NOW() - INTERVAL 1 HOUR
		GROUP BY p.id, p.user_id
		ORDER BY recent_likes DESC
		LIMIT 20
	`)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "server error")
		return
	}
	defer rows.Close()

	type trendRow struct {
		postID      int64
		recentLikes int
	}
	var rawTrends []trendRow
	for rows.Next() {
		var t trendRow
		var userID int64
		rows.Scan(&t.postID, &userID, &t.recentLikes)
		rawTrends = append(rawTrends, t)
	}

	posts := make([]any, 0, len(rawTrends))
	for _, rt := range rawTrends {
		p, err := h.fetchPost(r, rt.postID, myID)
		if err != nil {
			continue
		}
		posts = append(posts, map[string]any{
			"post":         p,
			"recent_likes": rt.recentLikes,
		})
	}

	h.respondJSON(w, http.StatusOK, map[string]any{
		"trending": posts,
	})
}
