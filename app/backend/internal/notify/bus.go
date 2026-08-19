package notify

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"sakuravel/internal/realtime"
)

// Deliverer は poller が配信先とするプロセス内の購読管理。*realtime.Hub が満たす。
type Deliverer interface {
	Publish(key int64, ev realtime.Event)
}

// subscriberChecker は宛先ごとのサブスクライバー有無を答えられる Deliverer の任意拡張。
type subscriberChecker interface {
	HasSubscribers(key int64) bool
}

// ReplyHydrator は返信イベントの本文を postID から組み立て直す。イベント行には id しか
// 載せていないため、サブスクライバーを抱えているプロセスだけがこれを呼ぶ。
type ReplyHydrator func(ctx context.Context, postID int64) (realtime.Event, error)

// Bus は sse_events を追うポーラと古い行を消す GC の組。プロセスごとに 1 つ生成する。
type Bus struct {
	db *sql.DB
	// notifications は宛先ユーザー ID ごと、threads はスレッドの根の投稿 ID ごとの配信先。
	notifications Deliverer
	threads       Deliverer
	hydrateReply  ReplyHydrator

	wg sync.WaitGroup

	pollInterval time.Duration
	batchSize    int

	gcInterval  time.Duration
	gcBatchSize int
	retention   time.Duration
}

// 既定値。配信の遅延はここのポーリング間隔と poller.go の pollLag の和になる。
const (
	defaultPollIntervalMS = 250
	defaultBatchSize      = 200
	defaultGCIntervalSec  = 60
	defaultGCBatchSize    = 1000
	defaultRetentionSec   = 3600
)

// New は Bus を組み立てる。各パラメータは NOTIFY_* 環境変数で上書きでき、
// 不正値は既定値に落ちる。
func New(db *sql.DB, notifications, threads Deliverer, hydrateReply ReplyHydrator) *Bus {
	return &Bus{
		db:            db,
		notifications: notifications,
		threads:       threads,
		hydrateReply:  hydrateReply,

		pollInterval: time.Duration(envInt("NOTIFY_POLL_INTERVAL_MS", defaultPollIntervalMS)) * time.Millisecond,
		batchSize:    envInt("NOTIFY_POLL_BATCH", defaultBatchSize),
		gcInterval:   time.Duration(envInt("NOTIFY_GC_INTERVAL_SEC", defaultGCIntervalSec)) * time.Second,
		gcBatchSize:  envInt("NOTIFY_GC_BATCH", defaultGCBatchSize),
		retention:    time.Duration(envInt("NOTIFY_RETENTION_SEC", defaultRetentionSec)) * time.Second,
	}
}

// Start はカーソルを同期的に確定させてから、ポーラと GC をゴルーチンで起こす。確定前に
// つないだサブスクライバーは取りこぼすので、HTTP の受付は Start が戻ってから始めること。
func (b *Bus) Start(ctx context.Context) error {
	cursor, err := b.initialCursor(ctx)
	if err != nil {
		return err
	}
	b.wg.Add(2)
	go func() {
		defer b.wg.Done()
		b.runPoller(ctx, cursor)
	}()
	go func() {
		defer b.wg.Done()
		b.runGC(ctx)
	}()
	return nil
}

// Wait は Start で起こしたループの終了を待つ。先に ctx をキャンセルしておくこと。
func (b *Bus) Wait() {
	b.wg.Wait()
}

// hydrateRetryDelay は本文の引き直しが一時的な不調で失敗したときの、やり直しまでの待ち。
const hydrateRetryDelay = 100 * time.Millisecond

// deliver は 1 行ぶんのイベントを kind に応じた配信先へ配る。
func (b *Bus) deliver(ctx context.Context, ev event) {
	switch ev.kind {
	case kindNotification:
		if !hasSubscribers(b.notifications, ev.subjectID) {
			return
		}
		b.notifications.Publish(ev.subjectID, NotificationEvent(ev.ntype.String, ev.postID))
	case kindReply:
		if !hasSubscribers(b.threads, ev.subjectID) || b.hydrateReply == nil || ev.postID == nil {
			return
		}
		// ここで失敗しても他のイベントの配信は続ける。投稿が消えている (ErrNoRows) のは
		// 恒久的で配る意味が無いが、それ以外は一時的な不調とみなして 1 度だけやり直す。
		out, err := b.hydrateReply(ctx, *ev.postID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil {
			time.Sleep(hydrateRetryDelay)
			out, err = b.hydrateReply(ctx, *ev.postID)
		}
		if err != nil {
			if ctx.Err() == nil { // 終了時のキャンセルはポーラ側と同じく黙って畳む
				slog.Error("notify: hydrate reply, giving up", "post", *ev.postID, "error", err)
			}
			return
		}
		b.threads.Publish(ev.subjectID, out)
	}
}

// hasSubscribers は答えられない配信先 (テストの記録用など) には常に配る側へ倒す。
func hasSubscribers(out Deliverer, key int64) bool {
	if out == nil {
		return false
	}
	c, ok := out.(subscriberChecker)
	return !ok || c.HasSubscribers(key)
}

// envInt は環境変数 key を正の整数として読み取り、読めなければ fallback を返す。
func envInt(key string, fallback int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}
