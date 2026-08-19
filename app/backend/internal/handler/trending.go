package handler

import (
	"net/http"
)

func (h *Handler) GetTrending(w http.ResponseWriter, r *http.Request) {
	myID, _ := h.currentUserID(r)

	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT p.id, t.recent_likes
		FROM (
			SELECT post_id, COUNT(*) AS recent_likes
			FROM likes
			WHERE created_at > NOW() - INTERVAL 1 HOUR
			GROUP BY post_id
			ORDER BY recent_likes DESC, post_id DESC
			LIMIT 20
		) t
		JOIN posts p ON p.id = t.post_id
		ORDER BY t.recent_likes DESC, p.id DESC
	`)
	if err != nil {
		h.serverError(w, r, err)
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
		rows.Scan(&t.postID, &t.recentLikes)
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
