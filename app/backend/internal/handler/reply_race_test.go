package handler

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"sakuravel/internal/middleware"
)

// TestConcurrentReplyToDeletedPostReturnsNotFound は「返信の作成中に親投稿が消えると、親を持たない
// 返信が 201 で作られる」障害に対するテスト。親のない返信は祖先を辿れず配信先がずれる。
func TestConcurrentReplyToDeletedPostReturnsNotFound(t *testing.T) {
	f := newFixtureWithRoutes(t, func(mux *http.ServeMux, h *Handler, auth *middleware.Auth) {
		mux.Handle("POST /replies", auth.Required(http.HandlerFunc(h.CreateReply)))
	})

	author := f.registerUser("reply_parent")
	replier := f.registerUser("replier")

	rounds := notifRounds
	orphans, created, notFound, serverErrors := 0, 0, 0, 0
	for round := 1; round <= rounds; round++ {
		parentID := f.insertPost(author.id, fmt.Sprintf("削除される親投稿 %d 回目", round), nil)

		// 親の DELETE を先に始めて commit だけを遅らせ、返信要求を全て「存在確認は通るが
		// INSERT では親が消えている」窓に入れる。同時に投げるだけでは親のない返信の由来を区別できない。
		tx, err := f.db.Begin()
		if err != nil {
			t.Fatalf("round %d: トランザクションの開始に失敗: %v", round, err)
		}
		if _, err := tx.Exec(`DELETE FROM posts WHERE id = ?`, parentID); err != nil {
			tx.Rollback()
			t.Fatalf("round %d: 親投稿の DELETE に失敗: %v", round, err)
		}

		// 0 番は削除の commit 係。返信要求が存在確認を終えた頃に確定させる。
		res := runBurst(burstSize+1, func(i int) (int, error) {
			if i == 0 {
				time.Sleep(50 * time.Millisecond)
				return 0, tx.Commit()
			}
			return postJSON(f, replier, "/replies", map[string]any{
				"post_id": parentID,
				"content": fmt.Sprintf("消えた親への返信 %d-%d", round, i),
			})
		})
		if err := res.firstErr(); err != nil {
			t.Fatalf("round %d: 同時 POST /replies の送信に失敗: %v", round, err)
		}
		created += res.count(http.StatusCreated)
		notFound += res.count(http.StatusNotFound)
		serverErrors += res.count(http.StatusInternalServerError)

		if got := f.countRows(`SELECT COUNT(*) FROM posts WHERE id = ?`, parentID); got != 0 {
			t.Fatalf("round %d: 親投稿が %d 行残っており、テストの前提が壊れている", round, got)
		}
		orphans += f.countRows(`SELECT COUNT(*) FROM posts WHERE parent_post_id = ?`, parentID)
	}

	want := rounds * burstSize
	t.Logf("%d ラウンド × %d 並列: 親のない返信=%d 件, 201=%d 件, 404=%d 件 (want %d), 500=%d 件",
		rounds, burstSize, orphans, created, notFound, want, serverErrors)

	if orphans != 0 {
		t.Errorf("親の消えた投稿への返信が %d 件残った。存在確認と INSERT が別の文なので、"+
			"その間に親が消えても返信が作られている", orphans)
	}
	if created != 0 {
		t.Errorf("201 が %d 件。親が消えている以上、返信の作成が成功してはならない", created)
	}
	if notFound != want {
		t.Errorf("404 = %d 件, want %d 件", notFound, want)
	}
	if serverErrors != 0 {
		t.Errorf("500 が %d 件", serverErrors)
	}
}

// TestReplyToLivePostCreatesReplyAndNotification は生きている親への返信が 201 で作られ、
// 親の著者に reply 通知が 1 件届くことを見る。
func TestReplyToLivePostCreatesReplyAndNotification(t *testing.T) {
	f := newFixtureWithRoutes(t, func(mux *http.ServeMux, h *Handler, auth *middleware.Auth) {
		mux.Handle("POST /replies", auth.Required(http.HandlerFunc(h.CreateReply)))
	})

	author := f.registerUser("live_parent")
	replier := f.registerUser("live_replier")
	parentID := f.insertPost(author.id, "残っている親投稿", nil)

	status, body, _ := f.do(&replier, http.MethodPost, "/replies", map[string]any{
		"post_id": parentID,
		"content": "生きている親への返信",
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /replies = %d, want 201, body=%s", status, body)
	}

	if got := f.countRows(
		`SELECT COUNT(*) FROM posts WHERE parent_post_id = ? AND user_id = ?`,
		parentID, replier.id,
	); got != 1 {
		t.Errorf("返信の行数 = %d, want 1", got)
	}
	if got := f.countRows(
		`SELECT COUNT(*) FROM notifications
		 WHERE user_id = ? AND type = 'reply' AND actor_id = ?`,
		author.id, replier.id,
	); got != 1 {
		t.Errorf("reply 通知の件数 = %d, want 1", got)
	}
}

// TestReplyNotifiesParentAuthorWhenParentDiesAfterInsert は、親が消えるのが INSERT より後なら
// 返信が 201 で作られ、親の著者への通知も落ちないことを見る。
func TestReplyNotifiesParentAuthorWhenParentDiesAfterInsert(t *testing.T) {
	f := newFixtureWithRoutes(t, func(mux *http.ServeMux, h *Handler, auth *middleware.Auth) {
		mux.Handle("POST /replies", auth.Required(http.HandlerFunc(h.CreateReply)))
	})

	author := f.registerUser("late_delete_parent")
	replier := f.registerUser("late_delete_replier")

	created := 0
	for round := 1; round <= notifRounds; round++ {
		parentID := f.insertPost(author.id, fmt.Sprintf("返信の直後に消える親投稿 %d 回目", round), nil)

		// 0 番は削除係。返信の INSERT が済んだ頃に親を消し、201 のまま親が居ない状態を作る。
		res := runBurst(2, func(i int) (int, error) {
			if i == 0 {
				time.Sleep(time.Duration(round%6) * 500 * time.Microsecond)
				_, err := f.db.Exec(`DELETE FROM posts WHERE id = ?`, parentID)
				return 0, err
			}
			return postJSON(f, replier, "/replies", map[string]any{
				"post_id": parentID,
				"content": fmt.Sprintf("消える親への返信 %d", round),
			})
		})
		if err := res.firstErr(); err != nil {
			t.Fatalf("round %d: 返信の送信または親投稿の DELETE に失敗: %v", round, err)
		}
		created += res.count(http.StatusCreated)
	}

	notified := f.countRows(
		`SELECT COUNT(*) FROM notifications WHERE user_id = ? AND type = 'reply' AND actor_id = ?`,
		author.id, replier.id)
	t.Logf("%d ラウンド: 201=%d 件, reply 通知=%d 件", notifRounds, created, notified)

	if created == 0 {
		t.Fatalf("201 が 1 件も無く、通知の有無を測れていない")
	}
	if notified != created {
		t.Errorf("201 が %d 件に対し reply 通知は %d 件。親の著者を INSERT の後に読むと、"+
			"その間に親が消えた分の通知が落ちる", created, notified)
	}
}

// TestReplyFailsWhenParentAuthorLookupFails は親の著者の取得が失敗したときの応答を見る。
// sql.ErrNoRows（親が既に無い）は 404 の経路に任せ、それ以外の失敗は 500 で止める。
func TestReplyFailsWhenParentAuthorLookupFails(t *testing.T) {
	dsn := os.Getenv(testDBEnv)
	if dsn == "" {
		t.Skipf("%s が未設定のため skip", testDBEnv)
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("%s の解析に失敗: %v", testDBEnv, err)
	}
	if cfg.Net != "tcp" {
		t.Skipf("接続断を挟むため tcp 接続の DSN が要る (net=%s)", cfg.Net)
	}

	// 親の著者を読む 1 文だけを狙って接続を切る。INSERT は別の接続で通るので、
	// 「SELECT だけが失敗した」状態がそのまま残る。
	killer := startQueryKiller(t, cfg.Addr, `SELECT user_id FROM posts WHERE id = ?`)
	cfg.Addr = killer.addr()
	t.Setenv(testDBEnv, cfg.FormatDSN())

	f := newFixtureWithRoutes(t, func(mux *http.ServeMux, h *Handler, auth *middleware.Auth) {
		mux.Handle("POST /replies", auth.Required(http.HandlerFunc(h.CreateReply)))
	})

	author := f.registerUser("lookup_fail_parent")
	replier := f.registerUser("lookup_fail_replier")
	parentID := f.insertPost(author.id, "著者の取得が失敗する親投稿", nil)

	killer.arm()
	status, body, _ := f.do(&replier, http.MethodPost, "/replies", map[string]any{
		"post_id": parentID,
		"content": "著者を読めなかったときの返信",
	})
	killer.disarm()

	if killer.kills() == 0 {
		t.Fatalf("親の著者を読む文が 1 度も止められておらず、障害を再現できていない (status=%d body=%s)",
			status, body)
	}

	replies := f.countRows(`SELECT COUNT(*) FROM posts WHERE parent_post_id = ?`, parentID)
	notified := f.countRows(
		`SELECT COUNT(*) FROM notifications WHERE user_id = ? AND type = 'reply' AND actor_id = ?`,
		author.id, replier.id)
	t.Logf("親の著者の取得が失敗したとき: status=%d, 返信=%d 件, reply 通知=%d 件, 切断=%d 回",
		status, replies, notified, killer.kills())

	if status != http.StatusInternalServerError {
		t.Errorf("POST /replies = %d, want 500 (body=%s)。sql.ErrNoRows 以外の失敗を握り潰すと、"+
			"返信は作られるのに通知だけが落ちる", status, body)
	}
	if replies != 0 {
		t.Errorf("返信が %d 行作られた。著者を読めていない以上、通知の宛先が確定しないまま"+
			"返信だけを残してはならない", replies)
	}
	if notified != 0 {
		t.Errorf("reply 通知が %d 件。返信が作られていないのに通知だけが出ている", notified)
	}
}

// queryKiller は DB への接続を中継し、marker を含む文を見つけたらその接続を送らずに切る。
// 特定の 1 クエリだけを決定的に失敗させるための、テスト専用の中継。
type queryKiller struct {
	ln       net.Listener
	upstream string
	marker   []byte
	armed    atomic.Bool
	killed   atomic.Int64
}

// startQueryKiller は upstream への中継を 127.0.0.1 の空きポートで始める。
// 切断は arm() を呼んでいる間だけ行う。
func startQueryKiller(t *testing.T, upstream, marker string) *queryKiller {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("中継の listen に失敗: %v", err)
	}
	k := &queryKiller{ln: ln, upstream: upstream, marker: []byte(marker)}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go k.relay(conn)
		}
	}()
	return k
}

func (k *queryKiller) addr() string { return k.ln.Addr().String() }
func (k *queryKiller) arm()         { k.armed.Store(true) }
func (k *queryKiller) disarm()      { k.armed.Store(false) }
func (k *queryKiller) kills() int64 { return k.killed.Load() }

// relay は 1 接続分の中継を行う。クライアント→サーバ方向だけを覗き、marker を含む書き込みが
// 来たら両側を閉じる。marker は 1 回の書き込みに収まる前提 (COM_STMT_PREPARE は文全体を 1 パケット)。
func (k *queryKiller) relay(client net.Conn) {
	server, err := net.Dial("tcp", k.upstream)
	if err != nil {
		client.Close()
		return
	}
	closeBoth := func() {
		client.Close()
		server.Close()
	}
	go func() {
		io.Copy(client, server)
		closeBoth()
	}()

	buf := make([]byte, 32*1024)
	for {
		n, rerr := client.Read(buf)
		if n > 0 {
			if k.armed.Load() && bytes.Contains(buf[:n], k.marker) {
				k.killed.Add(1)
				closeBoth()
				return
			}
			if _, werr := server.Write(buf[:n]); werr != nil {
				closeBoth()
				return
			}
		}
		if rerr != nil {
			closeBoth()
			return
		}
	}
}
