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

	var postIDs []int64
	recentLikes := map[int64]int{}
	for rows.Next() {
		var postID int64
		var likes int
		rows.Scan(&postID, &likes)
		postIDs = append(postIDs, postID)
		recentLikes[postID] = likes
	}
	rows.Close()

	fetched, err := h.fetchPostsBulk(r, postIDs, myID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	posts := make([]any, 0, len(fetched))
	for _, p := range fetched {
		posts = append(posts, map[string]any{
			"post":         p,
			"recent_likes": recentLikes[p.ID],
		})
	}

	h.respondJSON(w, http.StatusOK, map[string]any{
		"trending": posts,
	})
}
