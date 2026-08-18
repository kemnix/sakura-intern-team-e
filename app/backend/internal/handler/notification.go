package handler

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"sakuravel/internal/notify"
	"time"
)

// relayWriteTimeout は応答後のイベント行の書き込みに掛ける上限 (DSN に readTimeout が無い)。
const relayWriteTimeout = 5 * time.Second

func (h *Handler) GetNotifications(w http.ResponseWriter, r *http.Request) {
	myID, _ := h.currentUserID(r)
	page, perPage, offset := h.pagination(r)

	// type で種別を絞る（reply / like / repost / follow / footprint）。空または all は全種別。
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
		h.serverError(w, r, err)
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
// *sql.DB と *sql.Tx の双方が満たすので、通常経路とトランザクション経路で共有できる。
type notifExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// insertNotificationOnce は通知行だけを渡された exec で書く (イベント行は commit 後に
// deliverNotification が書く)。post_id が NULL になる follow / footprint では = も UNIQUE も
// 重複排除に黙って失敗するため、既存判定は NULL 安全等価 <=> でしか書けない。
func insertNotificationOnce(ctx context.Context, exec notifExecutor, userID int64, ntype string, actorID int64, postID *int64) (bool, error) {
	if userID == actorID {
		return false, nil
	}
	res, err := exec.ExecContext(ctx,
		`INSERT INTO notifications (user_id, type, actor_id, post_id)
		 SELECT ?, ?, ?, ? FROM DUAL
		 WHERE NOT EXISTS (
		     SELECT 1 FROM notifications n
		     WHERE n.user_id = ? AND n.type = ? AND n.actor_id = ? AND n.post_id <=> ?
		 )`,
		userID, ntype, actorID, postID,
		userID, ntype, actorID, postID,
	)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// insertNotificationOnceRetry はロック競合の敗者になったときだけ 1 度やり直す。
// 敗者はトランザクションが丸ごと巻き戻されるだけなので、同じ文をもう一度流せばよい。
func insertNotificationOnceRetry(ctx context.Context, exec notifExecutor, userID int64, ntype string, actorID int64, postID *int64) (bool, error) {
	inserted, err := insertNotificationOnce(ctx, exec, userID, ntype, actorID, postID)
	if err != nil && isRetryableLockError(err) {
		log.Printf("notify: notification insert lost a lock conflict, retrying once (user=%d type=%s actor=%d): %v",
			userID, ntype, actorID, err)
		inserted, err = insertNotificationOnce(ctx, exec, userID, ntype, actorID, postID)
	}
	return inserted, err
}

// createNotificationOnce は通知行の INSERT と、その後の配信をまとめて行う。通知の失敗で
// 本体の操作まで失敗させないので戻り値は無いが、握り潰すと誰にも気付かれないのでログに出す。
// 明示的なトランザクションで囲まないのは、同時いいねのデッドロック窓を広げないため。
func createNotificationOnce(h *Handler, r *http.Request, userID int64, ntype string, actorID int64, postID *int64) {
	if userID == actorID {
		return
	}
	inserted, err := insertNotificationOnceRetry(r.Context(), h.DB, userID, ntype, actorID, postID)
	if err != nil {
		log.Printf("notify: create notification (user=%d type=%s actor=%d): %v",
			userID, ntype, actorID, err)
		return
	}
	if !inserted {
		return
	}
	deliverNotification(r.Context(), h, userID, ntype, postID)
}

// deliverNotification は commit 済みの通知を配る。自プロセスのサブスクライバーへ即時配信し、
// 他プロセス向けにイベント行を書く (必ず commit 後。理由は notify.Publish の注記)。自分の
// ポーラも同じ行を読むので同一プロセスでは 2 回届くが、配信保証は at-least-once で足りる。
func deliverNotification(ctx context.Context, h *Handler, userID int64, ntype string, postID *int64) {
	h.Notifications.Publish(userID, notify.NotificationEvent(ntype, postID))

	// 応答を返した直後にリクエストの ctx がキャンセルされてもイベント行は書き切る。
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), relayWriteTimeout)
	defer cancel()
	if err := notify.Publish(writeCtx, h.DB, userID, ntype, postID); err != nil {
		log.Printf("notify: publish event (user=%d type=%s): %v", userID, ntype, err)
	}
}
