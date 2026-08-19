package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sakuravel/internal/notify"
	"sakuravel/internal/realtime"
)

// CreateReply は指定した投稿への返信を作成する。返信も posts の 1 行として保存する。
// 存在確認と INSERT を 1 文にするのは、その間に親が消えると親のない返信が残るため。
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
	// 宛先も配信キーも INSERT より前に読む。後から読むと親が消えた隙に通知が落ち、配信キーが
	// サブスクライブしている側とずれる。sql.ErrNoRows は下の INSERT が 0 行になり 404 で返る経路に任せる。
	var parentAuthorID int64
	authorErr := h.DB.QueryRowContext(r.Context(),
		`SELECT user_id FROM posts WHERE id = ?`, parentID,
	).Scan(&parentAuthorID)
	if authorErr != nil && !errors.Is(authorErr, sql.ErrNoRows) {
		h.serverError(w, r, authorErr)
		return
	}
	rootID := h.threadRootID(r, parentID)

	res, err := h.DB.ExecContext(r.Context(),
		`INSERT INTO posts (user_id, content, parent_post_id)
		 SELECT ?, ?, ? FROM DUAL
		 WHERE EXISTS (SELECT 1 FROM posts p WHERE p.id = ?)`,
		myID, req.Content, parentID, parentID,
	)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	affected, err := res.RowsAffected()
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if affected == 0 {
		h.respondError(w, http.StatusNotFound, "post not found")
		return
	}
	postID, err := res.LastInsertId()
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	post, _ := h.fetchPost(r, postID, myID)

	// 通知は直接の返信先の著者にのみ送る
	if authorErr == nil {
		createNotificationOnce(h, r, parentAuthorID, "reply", myID, &postID)
	}

	// 自プロセスのサブスクライバーにもここで直接は配らず、必ずイベント行を経由させる。本文を
	// 持つイベントなので、直接配信とポーラ経由の両方を通すと同じ返信が二重に描かれてしまう。
	// イベント行は commit の後に書き、応答後に ctx がキャンセルされても書き切る。
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), relayWriteTimeout)
	defer cancel()
	if err := notify.PublishReply(writeCtx, h.DB, rootID, postID); err != nil {
		slog.Error("notify: publish reply event", "root", rootID, "post", postID, "error", err)
	}

	h.respondJSON(w, http.StatusCreated, map[string]any{"post": post})
}

// ReplyEvent は返信イベントの本文を post_id から組み立て直す (notify.ReplyHydrator)。
// バスから呼ばれるためリクエストが無く、fetchPost には ctx だけを持つ空のリクエストを渡す。
// 閲覧者を 0 にできるのは、書かれたばかりの返信では閲覧者依存の項目が常に false のため。
func (h *Handler) ReplyEvent(ctx context.Context, postID int64) (realtime.Event, error) {
	post, err := h.fetchPost((&http.Request{}).WithContext(ctx), postID, 0)
	if err != nil {
		return realtime.Event{}, err
	}
	return realtime.Event{Type: "reply", Data: post}, nil
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
		h.serverError(w, r, err)
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
