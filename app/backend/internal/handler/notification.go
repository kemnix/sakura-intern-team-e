package handler

import (
	"context"
	"database/sql"
	"net/http"
	"sakuravel/internal/realtime"
)

func (h *Handler) GetNotifications(w http.ResponseWriter, r *http.Request) {
	myID, _ := h.currentUserID(r)
	page, perPage, offset := h.pagination(r)

	// type で種別を絞る（reply / like / repost / follow / footprint）。
	// 空または all のときは全種別を返す。
	typeCond := ""
	typeArgs := []any{}
	if t := r.URL.Query().Get("type"); t != "" && t != "all" {
		typeCond = " AND type = ?"
		typeArgs = append(typeArgs, t)
	}

	listArgs := append([]any{myID}, typeArgs...)
	listArgs = append(listArgs, perPage, offset)
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT id, type, actor_id, post_id, is_read, created_at
		FROM notifications
		WHERE user_id = ?`+typeCond+`
		ORDER BY created_at DESC, id DESC
		LIMIT ? OFFSET ?
	`, listArgs...)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "server error")
		return
	}
	defer rows.Close()

	type notifRow struct {
		id      int64
		ntype   string
		actorID int64
		postID  *int64
		isRead  bool
	}
	var rawNotifs []notifRow
	for rows.Next() {
		var n notifRow
		var createdAt any
		rows.Scan(&n.id, &n.ntype, &n.actorID, &n.postID, &n.isRead, &createdAt)
		rawNotifs = append(rawNotifs, n)
	}

	notifs := make([]any, 0, len(rawNotifs))
	for _, rn := range rawNotifs {
		actor, err := h.fetchUser(r, rn.actorID)
		if err != nil {
			continue
		}

		// 対象投稿の本文の抜粋を付ける。
		// 返信通知の post_id は返信そのものを指すので、返信先の抜粋も添える。
		var excerpt, parentExcerpt *string
		if rn.postID != nil {
			var content *string
			var parentID *int64
			if err := h.DB.QueryRowContext(r.Context(),
				`SELECT content, parent_post_id FROM posts WHERE id = ?`, *rn.postID,
			).Scan(&content, &parentID); err == nil {
				excerpt = excerptOf(content)
				if parentID != nil {
					var parentContent *string
					if err := h.DB.QueryRowContext(r.Context(),
						`SELECT content FROM posts WHERE id = ?`, *parentID,
					).Scan(&parentContent); err == nil {
						parentExcerpt = excerptOf(parentContent)
					}
				}
			}
		}

		notifs = append(notifs, map[string]any{
			"id":             rn.id,
			"type":           rn.ntype,
			"actor":          actor,
			"post_id":        rn.postID,
			"post_excerpt":   excerpt,
			"parent_excerpt": parentExcerpt,
			"is_read":        rn.isRead,
		})
	}

	var unreadCount int
	h.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM notifications WHERE user_id = ? AND is_read = FALSE`,
		myID,
	).Scan(&unreadCount)

	// total は絞り込み後の件数（ページングに使う）。unread_count はバッジ用なので全種別のまま。
	var total int
	h.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM notifications WHERE user_id = ?`+typeCond,
		append([]any{myID}, typeArgs...)...,
	).Scan(&total)

	h.respondJSON(w, http.StatusOK, map[string]any{
		"notifications": notifs,
		"total":         total,
		"unread_count":  unreadCount,
		"page":          page,
		"per_page":      perPage,
	})
}

func (h *Handler) MarkNotificationsRead(w http.ResponseWriter, r *http.Request) {
	myID, _ := h.currentUserID(r)
	h.DB.ExecContext(r.Context(),
		`UPDATE notifications SET is_read = TRUE WHERE user_id = ? AND is_read = FALSE`,
		myID,
	)
	h.respondJSON(w, http.StatusOK, map[string]string{"message": "ok"})
}

func (h *Handler) GetUnreadCount(w http.ResponseWriter, r *http.Request) {
	myID, _ := h.currentUserID(r)
	var count int
	h.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM notifications WHERE user_id = ? AND is_read = FALSE`,
		myID,
	).Scan(&count)
	h.respondJSON(w, http.StatusOK, map[string]int{"unread_count": count})
}

// excerptOf は通知一覧に載せる本文の抜粋を返す。
func excerptOf(content *string) *string {
	if content == nil {
		return nil
	}
	const limit = 40
	runes := []rune(*content)
	if len(runes) <= limit {
		return content
	}
	s := string(runes[:limit]) + "…"
	return &s
}

// notifExecutor は通知の INSERT に必要な最小限の実行インターフェース。
// *sql.DB と *sql.Tx の双方が満たすので、通常経路とトランザクション経路で
// 同じ INSERT を共有できる。
type notifExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// insertNotification は通知行の INSERT だけを行う。SSE 配信を含まないので、
// 呼び出し側はコミット後まで配信を遅らせられる。
// 自分自身への通知は抑止し、その場合は inserted=false を返す。
func insertNotification(ctx context.Context, exec notifExecutor, userID int64, ntype string, actorID int64, postID *int64) (bool, error) {
	if userID == actorID {
		return false, nil
	}
	if _, err := exec.ExecContext(ctx,
		`INSERT INTO notifications (user_id, type, actor_id, post_id) VALUES (?, ?, ?, ?)`,
		userID, ntype, actorID, postID,
	); err != nil {
		return false, err
	}
	return true, nil
}

// publishNotification は SSE 配信だけを行う。
// 宛先ユーザーが SSE で接続していればバッジ更新用に通知する。
func publishNotification(h *Handler, userID int64, ntype string, postID *int64) {
	h.Notifications.Publish(userID, realtime.Event{
		Type: "notification",
		Data: map[string]any{"type": ntype, "post_id": postID},
	})
}

// createNotification は INSERT と SSE 配信をまとめて行う従来どおりの入口で、
// 署名も挙動（自己通知の抑止・エラー握り潰し）も変更していない。
func createNotification(h *Handler, r *http.Request, userID int64, ntype string, actorID int64, postID *int64) {
	inserted, err := insertNotification(r.Context(), h.DB, userID, ntype, actorID, postID)
	if err != nil || !inserted {
		return
	}
	publishNotification(h, userID, ntype, postID)
}
