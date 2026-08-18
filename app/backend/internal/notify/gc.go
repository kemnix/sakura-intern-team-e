package notify

import (
	"context"
	"database/sql"
	"log"
	"time"
)

// gcLockName は GC を 1 プロセスだけに絞るための名前付きロック。
// DB 側の event_scheduler は OFF なので掃除を DB には任せられない。
const gcLockName = "sakuravel:events_gc"

// releaseLockTimeout は RELEASE_LOCK に掛ける上限。DB が無応答でも Bus.Wait() を返すため。
const releaseLockTimeout = 5 * time.Second

// runGC は古いイベント行を定期的に削除する。ctx がキャンセルされたら戻る。
func (b *Bus) runGC(ctx context.Context) {
	ticker := time.NewTicker(b.gcInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := b.gcOnce(ctx); err != nil && ctx.Err() == nil {
				log.Printf("notify: gc: %v", err)
			}
		}
	}
}

// gcOnce はロックを取れた場合だけ、保持期間を過ぎた行を上限つきのバッチで削除する。
// GET_LOCK / DELETE / RELEASE_LOCK は必ず同一の *sql.Conn 上で実行すること。プール越しに
// 別コネクションへ散ると解放に失敗し、そのコネクションが破棄されるまで全プロセスの GC が止まる。
func (b *Bus) gcOnce(ctx context.Context) error {
	conn, err := b.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	// GET_LOCK の第 2 引数 0 は「他プロセスが持っていたら待たずに諦める」。
	var locked sql.NullInt64
	if err := conn.QueryRowContext(ctx,
		`SELECT GET_LOCK(?, 0)`, gcLockName,
	).Scan(&locked); err != nil {
		return err
	}
	if !locked.Valid || locked.Int64 != 1 {
		return nil
	}
	defer func() {
		// ctx がキャンセルされていても解放だけは流す。DSN に readTimeout が無いので
		// 無応答の DB で bus.Wait() が戻らなくならないよう独自の期限を付ける。
		releaseCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx), releaseLockTimeout)
		defer cancel()

		var released sql.NullInt64
		if err := conn.QueryRowContext(releaseCtx,
			`SELECT RELEASE_LOCK(?)`, gcLockName,
		).Scan(&released); err != nil {
			log.Printf("notify: gc: release lock: %v", err)
		}
	}()

	// 1 回の DELETE に上限をかけ、長いロック保持と巨大な undo log を避ける。
	retentionSec := int(b.retention / time.Second)
	for {
		res, err := conn.ExecContext(ctx, `
			DELETE FROM sse_events
			WHERE created_at < NOW() - INTERVAL ? SECOND
			ORDER BY id
			LIMIT ?
		`, retentionSec, b.gcBatchSize)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n < int64(b.gcBatchSize) || ctx.Err() != nil {
			return nil
		}
	}
}
