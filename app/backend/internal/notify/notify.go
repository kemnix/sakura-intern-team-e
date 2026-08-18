// Package notify は複数の API プロセス間で SSE を配るための中継 (バス) を提供する。
// サブスクライバーは各プロセスのメモリ上にしかいないので、配信のきっかけを sse_events に
// id だけ 1 行書き、各プロセスがそれを追って自分のサブスクライバーにだけ配る。
package notify

import (
	"context"
	"database/sql"

	"sakuravel/internal/realtime"
)

// kind 列の値。ポーラはこれを見て配信先の hub を選ぶ。
const (
	kindNotification = "notification"
	kindReply        = "reply"
)

// Execer はイベント行の INSERT に必要な最小限の実行インターフェース。
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Publish は通知の fan-out 用のイベント行を 1 行書く。*sql.Tx から呼ぶと
// (id は INSERT 時採番・可視化は commit 時なので) ポーラに読み飛ばされるため、
// 必ず本体を commit した後に *sql.DB で呼ぶこと。
func Publish(ctx context.Context, exec Execer, userID int64, ntype string, postID *int64) error {
	_, err := exec.ExecContext(ctx,
		`INSERT INTO sse_events (kind, subject_id, type, post_id) VALUES (?, ?, ?, ?)`,
		kindNotification, userID, ntype, postID,
	)
	return err
}

// PublishReply は返信の fan-out 用のイベント行を 1 行書く。rootID は購読の鍵となる
// スレッドの根、postID は返信そのもの。commit 後に呼ぶ理由は Publish と同じ。
func PublishReply(ctx context.Context, exec Execer, rootID, postID int64) error {
	_, err := exec.ExecContext(ctx,
		`INSERT INTO sse_events (kind, subject_id, post_id) VALUES (?, ?, ?)`,
		kindReply, rootID, postID,
	)
	return err
}

// NotificationEvent は SSE に流す通知イベントを組み立てる。ローカル即時配信と
// ポーラ経由で同じ関数を通し、ワイヤ形式が 2 箇所で食い違わないようにしている。
func NotificationEvent(ntype string, postID *int64) realtime.Event {
	return realtime.Event{
		Type: "notification",
		Data: map[string]any{"type": ntype, "post_id": postID},
	}
}
