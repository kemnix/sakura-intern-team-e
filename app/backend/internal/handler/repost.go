package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-sql-driver/mysql"
)

// Repost は投稿をリポストする。reposts / posts / notifications への書き込みを
// 1 つのトランザクションにまとめ、既にリポスト済みなら posts 行も通知も増やさない。
// SSE 配信はコミット成功後だけに限定する（ロールバックした通知を配信しないため）。
// 既にリポスト済みの場合も posts 行の存在だけは確かめて補う。過去に失敗した
// リクエストが reposts 行だけを残した「割れた状態」は、通知を出さないまま
// リポストをやり直すことでしか直せないためである（通知の抑止自体は維持する）。
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
		if err := ensureRepostPost(r.Context(), tx, myID, req.PostID); err != nil {
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
		// 既にリポスト済み。通知は作らない（連打による通知の多重発火を止める）が、
		// posts 行の欠落だけは補う。reposts 行だけが残った割れた状態は、
		// リポストのやり直しでしか修復できないためである。
		if err := ensureRepostPost(r.Context(), tx, myID, req.PostID); err != nil {
			h.respondError(w, http.StatusInternalServerError, "server error")
			return
		}
		if err := tx.Commit(); err != nil {
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

// ensureRepostPost はリポスト由来の posts 行が無ければ 1 行だけ作る。
// ON DUPLICATE KEY UPDATE ではなく WHERE NOT EXISTS で守るのは、
// migrations/003_repost_unique.sql の UNIQUE KEY が未適用の DB
// （docker-compose.yml の initdb マウントは初回のみ実行されるため、既存 DB では
// 黙って読み飛ばされる）でも重複行を作らないようにするためである。
// なお NOT EXISTS の検査と INSERT の間には同時実行の隙間が残る。それを塞ぐのは
// 上記 UNIQUE KEY であり、本関数はそれ単独では競合を防げない。
func ensureRepostPost(ctx context.Context, tx *sql.Tx, userID, postID int64) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO posts (user_id, is_repost, original_post_id)
		 SELECT ?, TRUE, ? FROM DUAL
		 WHERE NOT EXISTS (
		     SELECT 1 FROM posts p
		     WHERE p.user_id = ? AND p.original_post_id = ?
		 )`,
		userID, postID, userID, postID,
	)
	return err
}

// UnRepost はリポストを取り消す。reposts と posts の 2 つの DELETE を 1 つの
// トランザクションにまとめ、両方の結果を検査してからコミットする。
// 片方だけ成功すると reposts 行が消えて posts 行だけが残るが、UI は reposts を見て
// 「未リポスト」と表示するため取り消し導線が消え、孤児になった posts 行を
// ユーザーが二度と消せなくなるからである。
func (h *Handler) UnRepost(w http.ResponseWriter, r *http.Request) {
	myID, _ := h.currentUserID(r)
	postID, err := pathID(r, "post_id")
	if err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid post_id")
		return
	}

	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "server error")
		return
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(r.Context(),
		`DELETE FROM reposts WHERE user_id = ? AND post_id = ?`, myID, postID,
	); err != nil {
		h.respondError(w, http.StatusInternalServerError, "server error")
		return
	}
	if _, err := tx.ExecContext(r.Context(),
		`DELETE FROM posts WHERE user_id = ? AND original_post_id = ? AND is_repost = TRUE`,
		myID, postID,
	); err != nil {
		h.respondError(w, http.StatusInternalServerError, "server error")
		return
	}
	if err := tx.Commit(); err != nil {
		h.respondError(w, http.StatusInternalServerError, "server error")
		return
	}

	var count int
	if err := h.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM reposts WHERE post_id = ?`, postID,
	).Scan(&count); err != nil {
		h.respondError(w, http.StatusInternalServerError, "server error")
		return
	}

	h.respondJSON(w, http.StatusOK, map[string]int{"reposts_count": count})
}
