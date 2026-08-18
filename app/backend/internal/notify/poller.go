package notify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/go-sql-driver/mysql"
)

// pollLag は書かれたばかりの行を読まずに置く時間。id は INSERT 時採番・可視化は commit 時
// なので、直後に読むと採番済みで未可視の id をカーソルが飛び越えうる。
const pollLag = 200 * time.Millisecond

const pollQuery = `
	SELECT id, kind, subject_id, type, post_id
	FROM sse_events
	WHERE id > ? AND created_at < NOW(6) - INTERVAL ? MICROSECOND
	ORDER BY id
	LIMIT ?
`

// cursorRetryInterval はカーソル初期化に失敗したときの再試行間隔。
const cursorRetryInterval = 3 * time.Second

// runPoller は sse_events を繰り返し読み、自プロセスのサブスクライバーに配る。
func (b *Bus) runPoller(ctx context.Context, cursor int64) {
	log.Printf("notify: poller started (cursor=%d, interval=%s, batch=%d)",
		cursor, b.pollInterval, b.batchSize)

	ticker := time.NewTicker(b.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// 満杯のバッチが続く限り読み進め、遅延を積み上げない。
			for {
				n, next, err := b.pollOnce(ctx, cursor)
				if err != nil {
					if ctx.Err() == nil {
						log.Printf("notify: poll: %v", err)
					}
					break
				}
				cursor = next
				if n < b.batchSize || ctx.Err() != nil {
					break
				}
			}
		}
	}
}

// initialCursor は起動時のカーソル初期値として現在の最大 id を返す。0 起点にすると
// 過去のイベントを全部読み直し、サブスクライバーに古い通知を再配信してしまう。
func (b *Bus) initialCursor(ctx context.Context) (int64, error) {
	ticker := time.NewTicker(cursorRetryInterval)
	defer ticker.Stop()

	for {
		var maxID int64
		err := b.db.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(id), 0) FROM sse_events`,
		).Scan(&maxID)
		if err == nil {
			return maxID, nil
		}
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		// サーバがエラーを返した = DB には届いており、待っても直らない。
		var serverErr *mysql.MySQLError
		if errors.As(err, &serverErr) {
			return 0, fmt.Errorf("sse_events を確認できない: %w\n"+
				"migrations/004_sse_events.sql が未適用の可能性が高い", err)
		}
		log.Printf("notify: initial cursor: %v (retrying)", err)

		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-ticker.C:
		}
	}
}

// pollOnce は cursor より新しいイベントを最大 batchSize 件読んで配信し、
// 読んだ件数と次のカーソルを返す。
func (b *Bus) pollOnce(ctx context.Context, cursor int64) (int, int64, error) {
	rows, err := b.db.QueryContext(ctx, pollQuery, cursor, pollLag.Microseconds(), b.batchSize)
	if err != nil {
		return 0, cursor, err
	}
	defer rows.Close()

	// 配信の前に読み切る。読みながら配ると詰まったぶん DB のカーソルを開いたままにする。
	events := make([]event, 0, b.batchSize)
	next := cursor
	// 途中で失敗しても読めたぶんは配る。カーソルは読み切れた行までしか進めない。
	deliver := func() int {
		for _, ev := range events {
			b.deliver(ctx, ev)
		}
		return len(events)
	}

	for rows.Next() {
		var id int64
		var ev event
		if err := rows.Scan(&id, &ev.kind, &ev.subjectID, &ev.ntype, &ev.postID); err != nil {
			return deliver(), next, err
		}
		next = id
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		return deliver(), next, err
	}
	return deliver(), next, nil
}

// event はポーラが読み取った 1 行ぶんの配信内容。列の意味は
// migrations/004_sse_events.sql を参照。
type event struct {
	kind      string
	subjectID int64
	ntype     sql.NullString
	postID    *int64
}
