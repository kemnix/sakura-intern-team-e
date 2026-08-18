package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-sql-driver/mysql"
)

// Repost は投稿をリポストする。reposts / posts / notifications への書き込みを
// 1 つのトランザクションにまとめ、既にリポスト済みなら posts 行も通知も増やさない。
// SSE 配信はコミット成功後だけに限定する（ロールバックした通知を配信しないため）。
func (h *Handler) Repost(w http.ResponseWriter, r *http.Request) {
	myID, _ := h.currentUserID(r)

	var req struct {
		PostID int64 `json:"post_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid request")
		return
	}

	// 元投稿の存在と所有者を書き込みより先に確認する。外部キーが無いため、
	// 存在しない投稿へのリポストを許すと孤児行が残ってしまう。
	var postOwnerID int64
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT user_id FROM posts WHERE id = ?`, req.PostID,
	).Scan(&postOwnerID)
	if errors.Is(err, sql.ErrNoRows) {
		h.respondError(w, http.StatusNotFound, "post not found")
		return
	}
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "server error")
		return
	}

	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "server error")
		return
	}
	defer tx.Rollback()

	// reposts は素の INSERT にして重複を検知できるようにする。
	notified := false
	_, err = tx.ExecContext(r.Context(),
		`INSERT INTO reposts (user_id, post_id) VALUES (?, ?)`,
		myID, req.PostID,
	)
	switch {
	case err == nil:
		// ON DUPLICATE KEY UPDATE は migrations/003_repost_unique.sql の
		// UNIQUE KEY (user_id, original_post_id) によって初めて実際に効く。
		if _, err := tx.ExecContext(r.Context(),
			`INSERT INTO posts (user_id, is_repost, original_post_id)
			 VALUES (?, TRUE, ?)
			 ON DUPLICATE KEY UPDATE id = id`,
			myID, req.PostID,
		); err != nil {
			h.respondError(w, http.StatusInternalServerError, "server error")
			return
		}
		notified, err = insertNotification(
			r.Context(), tx, postOwnerID, "repost", myID, &req.PostID,
		)
		if err != nil {
			h.respondError(w, http.StatusInternalServerError, "server error")
			return
		}
		if err := tx.Commit(); err != nil {
			h.respondError(w, http.StatusInternalServerError, "server error")
			return
		}
	case isDuplicateKeyError(err):
		// 既にリポスト済み。通知を作らずに閉じることで、連打による
		// 通知の多重発火を止める。書き込みは無いのでロールバックでよい。
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			h.respondError(w, http.StatusInternalServerError, "server error")
			return
		}
	default:
		h.respondError(w, http.StatusInternalServerError, "server error")
		return
	}

	// コミットが成功した場合に限って SSE を配信する。
	if notified {
		publishNotification(h, postOwnerID, "repost", &req.PostID)
	}

	var count int
	if err := h.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM reposts WHERE post_id = ?`, req.PostID,
	).Scan(&count); err != nil {
		h.respondError(w, http.StatusInternalServerError, "server error")
		return
	}

	h.respondJSON(w, http.StatusOK, map[string]int{"reposts_count": count})
}

// isDuplicateKeyError は MySQL / MariaDB の重複キーエラー（error 1062）かを判定する。
// リポスト済みかどうかを INSERT の結果から見分けるために使う。
func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
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
