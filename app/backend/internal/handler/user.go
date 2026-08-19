package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
)

func (h *Handler) GetMe(w http.ResponseWriter, r *http.Request) {
	myID, ok := h.currentUserID(r)
	if !ok {
		h.respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	user, err := h.fetchUser(r, myID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.respondJSON(w, http.StatusOK, map[string]any{"user": user})
}

// GetProfile は指定ユーザーのプロフィールを返し、他人の閲覧なら足跡を残す。
// 足跡そのものは訪問回数を数えるため毎回 1 行増やすが、通知は
// createNotificationOnce にして同じ訪問者からの再訪では増やさない。
func (h *Handler) GetProfile(w http.ResponseWriter, r *http.Request) {
	userID, err := pathID(r, "user_id")
	if err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid user_id")
		return
	}

	user, err := h.fetchUser(r, userID)
	if err == sql.ErrNoRows {
		h.respondError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	// 足跡を記録（認証済みユーザーのみ）
	if visitorID, ok := h.currentUserID(r); ok {
		recordFootprint(h, r, userID, visitorID)
		createNotificationOnce(h, r, userID, "footprint", visitorID, nil)
	}

	h.respondJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (h *Handler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	myID, _ := h.currentUserID(r)

	var req struct {
		DisplayName string  `json:"display_name"`
		Bio         *string `json:"bio"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid request")
		return
	}

	_, err := h.DB.ExecContext(r.Context(),
		`UPDATE users SET display_name = ?, bio = ? WHERE id = ?`,
		req.DisplayName, req.Bio, myID,
	)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	user, _ := h.fetchUser(r, myID)
	h.respondJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (h *Handler) GetFollowers(w http.ResponseWriter, r *http.Request) {
	userID, err := pathID(r, "user_id")
	if err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid user_id")
		return
	}

	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT follower_id FROM follows WHERE followee_id = ?`, userID,
	)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var followerID int64
		rows.Scan(&followerID)
		ids = append(ids, followerID)
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
	h.respondJSON(w, http.StatusOK, map[string]any{"users": users, "total": len(users)})
}

func (h *Handler) GetFollowing(w http.ResponseWriter, r *http.Request) {
	userID, err := pathID(r, "user_id")
	if err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid user_id")
		return
	}

	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT followee_id FROM follows WHERE follower_id = ?`, userID,
	)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var followeeID int64
		rows.Scan(&followeeID)
		ids = append(ids, followeeID)
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
	h.respondJSON(w, http.StatusOK, map[string]any{"users": users, "total": len(users)})
}

// Follow は対象ユーザーをフォローする。follows は主キーで冪等なのに通知だけが
// 毎回増えていたので、通知も createNotificationOnce で冪等にする。
// この通知は post_id が NULL であり、= 比較では重複排除できない側の経路である。
func (h *Handler) Follow(w http.ResponseWriter, r *http.Request) {
	myID, _ := h.currentUserID(r)
	targetID, err := pathID(r, "user_id")
	if err != nil || myID == targetID {
		h.respondError(w, http.StatusBadRequest, "invalid user_id")
		return
	}

	// フォロー前に対象ユーザーが存在するか確認する。外部キーが無いので、
	// 存在しないユーザーへのフォローを許すと孤児行が残る。
	if _, err := h.fetchUser(r, targetID); errors.Is(err, sql.ErrNoRows) {
		h.respondError(w, http.StatusNotFound, "user not found")
		return
	} else if err != nil {
		h.serverError(w, r, err)
		return
	}

	_, err = h.DB.ExecContext(r.Context(),
		`INSERT INTO follows (follower_id, followee_id) VALUES (?, ?)
		 ON DUPLICATE KEY UPDATE follower_id = follower_id`,
		myID, targetID,
	)

	if err != nil {
		h.serverError(w, r, err)
		return
	}

	createNotificationOnce(h, r, targetID, "follow", myID, nil)

	h.respondJSON(w, http.StatusOK, map[string]string{"message": "followed"})
}

func (h *Handler) Unfollow(w http.ResponseWriter, r *http.Request) {
	myID, _ := h.currentUserID(r)
	targetID, err := pathID(r, "user_id")
	if err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid user_id")
		return
	}

	if _, err := h.DB.ExecContext(r.Context(),
		`DELETE FROM follows WHERE follower_id = ? AND followee_id = ?`,
		myID, targetID,
	); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.respondJSON(w, http.StatusOK, map[string]string{"message": "unfollowed"})
}
