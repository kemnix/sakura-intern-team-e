package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"sakuravel/internal/middleware"
)

// このファイルは「同時に届いた同じ操作」に対する回帰テストである。既存のテストは
// すべて逐次・単一プロセスなので、本番（1 つの MariaDB を複数の API プロセスが共有し、
// ロードバランサ配下で同じユーザーのリクエストが別 VM に散る構成）で起きる
// 連打・多重タブ・リトライの重なりを 1 件も検証していない。ここで測るのは
// 「重複キーの敗者が 500 になっていないか」と「検査してから INSERT するまでの
// 窓で通知が二重に増えないか」の 2 点である。

// burstSize は 1 回の同時実行で投げるリクエスト数、
// notifRounds は競合の再現率を測るための繰り返し回数。
// 再現しにくい競合を追い込みたいときだけ環境変数で増やせるようにしてある。
var (
	burstSize   = testEnvInt("CONCURRENCY_BURST", 8)
	notifRounds = testEnvInt("CONCURRENCY_ROUNDS", 30)
)

// testEnvInt は環境変数 key を正の整数として読む。
// 未設定・解析失敗・0 以下のいずれでも fallback を返す。
func testEnvInt(key string, fallback int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

// burstResult は同時実行 1 回分の結果。goroutine は index 固定でしか書かないので
// 追加の同期は要らない。
type burstResult struct {
	statuses []int
	errs     []error
}

// count は status に一致した応答数を返す。
func (b burstResult) count(status int) int {
	n := 0
	for _, s := range b.statuses {
		if s == status {
			n++
		}
	}
	return n
}

// firstErr は送信自体に失敗した最初のエラーを返す。
func (b burstResult) firstErr() error {
	for _, err := range b.errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// runBurst は n 本の goroutine を閉じたチャネル（スタートバリア）で同時に解き放つ。
// これが無いと goroutine は生成順に走ってしまい、逐次実行と変わらなくなる。
// goroutine 内から t.Fatalf は呼べない（テスト goroutine 以外での runtime.Goexit に
// なる）ので、結果はスライスに書き戻し、判定は呼び出し元で行う。
func runBurst(n int, fn func(i int) (int, error)) burstResult {
	res := burstResult{statuses: make([]int, n), errs: make([]error, n)}
	start := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			res.statuses[i], res.errs[i] = fn(i)
		}(i)
	}
	close(start)
	wg.Wait()
	return res
}

// postJSON は goroutine から呼べる POST。fixture.do は失敗時に t.Fatalf を呼ぶため
// 同時実行からは使えないので、エラーを戻り値で返す版をここに置く。
func postJSON(f *fixture, u testUser, path string, body any) (int, error) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(http.MethodPost, f.srv.URL+path, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(u.cookie)

	resp, err := f.srv.Client().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}

// insertUser はラウンドごとに使い捨てる相手ユーザーを直接 INSERT する。
// POST /register は bcrypt を通るので 1 件あたり数十 ms かかり、数十ラウンド回すと
// 測りたい競合そのものより準備時間のほうが長くなる。作った ID は fixture の
// 後始末対象に加えるので、シードデータには触れない。
func (f *fixture) insertUser(role string) int64 {
	f.t.Helper()

	uniq := fmt.Sprintf("c_%s_%d", role, time.Now().UnixNano())
	res, err := f.db.Exec(
		`INSERT INTO users (username, display_name, email, password_hash)
		 VALUES (?, ?, ?, ?)`,
		uniq, role, uniq+"@example.test", "not-a-real-hash",
	)
	if err != nil {
		f.t.Fatalf("fixture のユーザー INSERT に失敗: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		f.t.Fatalf("LastInsertId: %v", err)
	}
	f.userIDs = append(f.userIDs, id)
	return id
}

// hasRepostUniqueKey は 003_repost_unique.sql の UNIQUE KEY が張られているかを返す。
// initdb マウント経由の SQL はボリューム初回作成時にしか走らないため、本番の DB に
// この索引が無いことは普通に起こりうる。結果の解釈が変わるのでテストから明示する。
func (f *fixture) hasRepostUniqueKey() bool {
	return f.countRows(`
		SELECT COUNT(*) FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = DATABASE()
		  AND TABLE_NAME = 'posts'
		  AND INDEX_NAME = 'uq_posts_user_original'
	`) > 0
}

// TestConcurrentRepostSamePost は「リポストボタンの連打や多重タブが同時に届いたときに、
// リポスト由来の posts 行や通知が二重に増える」障害に対するテスト。逐次版の
// TestRepostIsIdempotent は検査と INSERT の間に別リクエストが割り込まない前提なので、
// 複数プロセス構成でのこの経路はこれまで未検証だった。競合は毎回起きるとは限らないので
// ラウンドを重ねて再現率として出す。重複キーの敗者が 500 として表に出ていないか
// （ユーザーには失敗に見える）も併せて数える。
// 実測 (MariaDB 10.11 / REPEATABLE READ): UNIQUE KEY あり 150 ラウンド × 8 並列、
// なし 90 ラウンド × 8 並列 + 90 ラウンド × 32 並列のいずれも異常 0。
// 効いているのは 003 の UNIQUE ではなく、同じトランザクションで先に走る
// reposts (主キー) の INSERT で、敗者はそこで勝者の commit まで待たされる。
func TestConcurrentRepostSamePost(t *testing.T) {
	f := newFixture(t)

	author := f.registerUser("author")
	reposter := f.registerUser("reposter")

	// 003 の UNIQUE KEY は initdb マウント経由だとボリューム初回作成時にしか流れず、
	// 既存 DB では黙って読み飛ばされる。結果の解釈が変わるので有無を明示する。
	unique := f.hasRepostUniqueKey()
	t.Logf("003_repost_unique の UNIQUE KEY (posts.uq_posts_user_original): %v", unique)

	rounds := notifRounds
	dupRepostRounds, dupPostRounds, dupNotifRounds := 0, 0, 0
	maxPosts, serverErrors := 0, 0
	for round := 1; round <= rounds; round++ {
		postID := f.insertPost(author.id, fmt.Sprintf("同時リポスト %d 回目の元投稿", round), nil)

		res := runBurst(burstSize, func(int) (int, error) {
			return postJSON(f, reposter, "/reposts", map[string]int64{"post_id": postID})
		})
		if err := res.firstErr(); err != nil {
			t.Fatalf("round %d: 同時 POST /reposts の送信に失敗: %v", round, err)
		}
		serverErrors += res.count(500)

		if got := f.countRows(
			`SELECT COUNT(*) FROM reposts WHERE user_id = ? AND post_id = ?`,
			reposter.id, postID,
		); got != 1 {
			dupRepostRounds++
		}

		posts := f.countRows(
			`SELECT COUNT(*) FROM posts
			 WHERE user_id = ? AND original_post_id = ? AND is_repost = TRUE`,
			reposter.id, postID,
		)
		if posts != 1 {
			dupPostRounds++
		}
		if posts > maxPosts {
			maxPosts = posts
		}

		if got := f.countRows(
			`SELECT COUNT(*) FROM notifications
			 WHERE user_id = ? AND actor_id = ? AND type = 'repost' AND post_id = ?`,
			author.id, reposter.id, postID,
		); got != 1 {
			dupNotifRounds++
		}
	}

	t.Logf("%d ラウンド × %d 並列 (uq_posts_user_original=%v): "+
		"reposts が 1 行でないラウンド=%d, リポスト由来 posts が 1 行でないラウンド=%d (最大 %d 行), "+
		"repost 通知が 1 件でないラウンド=%d, 500=%d 件",
		rounds, burstSize, unique, dupRepostRounds, dupPostRounds, maxPosts,
		dupNotifRounds, serverErrors)

	if serverErrors != 0 {
		t.Errorf("500 が %d 件。重複キーの敗者をサーバエラーとして表に出してはならない", serverErrors)
	}
	if dupRepostRounds != 0 {
		t.Errorf("reposts が 1 行でないラウンド = %d/%d", dupRepostRounds, rounds)
	}
	if dupPostRounds != 0 {
		t.Errorf("リポスト由来の posts が 1 行でないラウンド = %d/%d (最大 %d 行)。"+
			"ensureRepostPost の NOT EXISTS と INSERT の間の窓を塞げていない",
			dupPostRounds, rounds, maxPosts)
	}
	if dupNotifRounds != 0 {
		t.Errorf("repost 通知が 1 件でないラウンド = %d/%d", dupNotifRounds, rounds)
	}
}

// TestNotificationDedupRequiresRepeatableRead は「DB の分離レベルを
// READ COMMITTED に変えた（あるいは既定がそうである別サーバに載せ替えた）だけで、
// 通知が大量に重複しはじめる」障害に対するテスト。insertNotificationOnce の
// WHERE NOT EXISTS には UNIQUE 制約の裏付けが無く、同時実行で壊れないのは
// REPEATABLE READ の InnoDB が INSERT ... SELECT の読み取り側に
// 共有ネクストキーロックを掛けて直列化しているからにすぎない。
// 実測: READ COMMITTED では 50 ラウンド × 16 並列で like 47/50 ラウンド (最大 15 件)、
// follow 48/50 ラウンド (最大 11 件) が重複した。
func TestNotificationDedupRequiresRepeatableRead(t *testing.T) {
	f := newFixture(t)

	var level string
	err := f.db.QueryRow(`SELECT @@transaction_isolation`).Scan(&level)
	if err != nil {
		// MariaDB 10.x には transaction_isolation が無く tx_isolation だけがある。
		if err2 := f.db.QueryRow(`SELECT @@tx_isolation`).Scan(&level); err2 != nil {
			t.Fatalf("分離レベルを取得できない: %v / %v", err, err2)
		}
	}
	t.Logf("接続先の分離レベル: %s", level)

	if level != "REPEATABLE-READ" {
		t.Errorf("分離レベル = %s。insertNotificationOnce の重複排除は "+
			"REPEATABLE READ のギャップロックに依存しており、"+
			"READ COMMITTED では同時実行でほぼ確実に通知が重複する", level)
	}
}

// TestConcurrentLikeNotification は「同じ投稿への同時いいねで like 通知が
// 二重に届く」障害に対するテスト。insertNotificationOnce の重複排除は
// WHERE NOT EXISTS だけで、その裏に UNIQUE 制約が無い（follow / footprint の
// post_id が NULL で、一意索引は NULL 同士を別値として扱うため張れない）。
// つまり検査してから INSERT するまでの窓が実在する。1 回の同時実行では再現しない
// ことがあるので、ラウンドを重ねて再現率として報告する。
// 実測 (REPEATABLE READ): 200 ラウンド × 32 並列 = 6400 リクエストで重複 0。
// ただしこれは安全だからではなく、REPEATABLE READ の InnoDB が
// INSERT ... SELECT の読み取り側に共有ネクストキーロックを掛けて直列化しているため。
// 代償として同区間で Innodb_deadlocks が 26 → 332、Innodb_row_lock_waits が
// 329 → 6118 まで増えた。READ COMMITTED では 50 ラウンド × 16 並列のうち
// 47 ラウンドが重複した (最大 15 件) → TestNotificationDedupRequiresRepeatableRead。
func TestConcurrentLikeNotification(t *testing.T) {
	f := newFixtureWithRoutes(t, func(mux *http.ServeMux, h *Handler, auth *middleware.Auth) {
		mux.Handle("POST /likes", auth.Required(http.HandlerFunc(h.Like)))
	})

	author := f.registerUser("liked")
	liker := f.registerUser("liker")

	rounds := notifRounds
	dupRounds, zeroRounds, maxNotifs, serverErrors := 0, 0, 0, 0
	for round := 1; round <= rounds; round++ {
		// 通知の重複排除キーは (user_id, type, actor_id, post_id) なので、
		// ラウンドごとに新しい投稿を作れば毎回まっさらな状態から競わせられる。
		postID := f.insertPost(author.id, fmt.Sprintf("同時いいね %d 回目", round), nil)

		res := runBurst(burstSize, func(int) (int, error) {
			return postJSON(f, liker, "/likes", map[string]int64{"post_id": postID})
		})
		if err := res.firstErr(); err != nil {
			t.Fatalf("round %d: 同時 POST /likes の送信に失敗: %v", round, err)
		}
		serverErrors += res.count(500)

		if got := f.countRows(
			`SELECT COUNT(*) FROM likes WHERE user_id = ? AND post_id = ?`,
			liker.id, postID,
		); got != 1 {
			t.Errorf("round %d: likes の行数 = %d, want 1", round, got)
		}

		n := f.countRows(
			`SELECT COUNT(*) FROM notifications
			 WHERE user_id = ? AND type = 'like' AND actor_id = ? AND post_id = ?`,
			author.id, liker.id, postID,
		)
		if n > 1 {
			dupRounds++
		}
		if n == 0 {
			zeroRounds++
		}
		if n > maxNotifs {
			maxNotifs = n
		}
	}

	t.Logf("%d ラウンド × %d 並列: like 通知が 2 件以上=%d ラウンド, 0 件=%d ラウンド, 最大=%d 件, 500=%d 件",
		rounds, burstSize, dupRounds, zeroRounds, maxNotifs, serverErrors)

	if serverErrors != 0 {
		t.Errorf("500 が %d 件", serverErrors)
	}
	if dupRounds > 0 {
		t.Errorf("like 通知が重複したラウンド = %d/%d (最大 %d 件)。"+
			"insertNotificationOnce の NOT EXISTS と INSERT の間の窓が実際に開いている",
			dupRounds, rounds, maxNotifs)
	}
	if zeroRounds > 0 {
		t.Errorf("like 通知が 1 件も作られなかったラウンド = %d/%d。"+
			"createNotificationOnce がエラーを握り潰しているため、"+
			"デッドロックで負けた側の通知が黙って消えている可能性がある",
			zeroRounds, rounds)
	}
}

// TestConcurrentFollowNotification は「同時フォローで follow 通知が二重に届く」
// 障害に対するテスト。follow 通知は post_id が NULL であり、UNIQUE 索引では
// NULL 同士が別値扱いになるため、DB 側の一意制約では原理的に守れない経路である。
// 素朴に UNIQUE を足す対策では取りこぼす側なので、like とは別に測る必要がある。
// 実測 (REPEATABLE READ): 200 ラウンド × 32 並列で重複 0。
// READ COMMITTED では 50 ラウンド × 16 並列のうち 48 ラウンドが重複 (最大 11 件)。
func TestConcurrentFollowNotification(t *testing.T) {
	f := newFixtureWithRoutes(t, func(mux *http.ServeMux, h *Handler, auth *middleware.Auth) {
		mux.Handle("POST /users/{user_id}/follow", auth.Required(http.HandlerFunc(h.Follow)))
	})

	follower := f.registerUser("follower")

	rounds := notifRounds
	dupRounds, zeroRounds, maxNotifs, serverErrors := 0, 0, 0, 0
	for round := 1; round <= rounds; round++ {
		// 重複排除キーに post_id が効かないので、ラウンドごとに別のフォロー先を作る。
		targetID := f.insertUser("followee")

		res := runBurst(burstSize, func(int) (int, error) {
			return postJSON(f, follower, fmt.Sprintf("/users/%d/follow", targetID), nil)
		})
		if err := res.firstErr(); err != nil {
			t.Fatalf("round %d: 同時 POST /users/{id}/follow の送信に失敗: %v", round, err)
		}
		serverErrors += res.count(500)

		if got := f.countRows(
			`SELECT COUNT(*) FROM follows WHERE follower_id = ? AND followee_id = ?`,
			follower.id, targetID,
		); got != 1 {
			t.Errorf("round %d: follows の行数 = %d, want 1", round, got)
		}

		n := f.countRows(
			`SELECT COUNT(*) FROM notifications
			 WHERE user_id = ? AND type = 'follow' AND actor_id = ? AND post_id IS NULL`,
			targetID, follower.id,
		)
		if n > 1 {
			dupRounds++
		}
		if n == 0 {
			zeroRounds++
		}
		if n > maxNotifs {
			maxNotifs = n
		}
	}

	t.Logf("%d ラウンド × %d 並列: follow 通知が 2 件以上=%d ラウンド, 0 件=%d ラウンド, 最大=%d 件, 500=%d 件",
		rounds, burstSize, dupRounds, zeroRounds, maxNotifs, serverErrors)

	if serverErrors != 0 {
		t.Errorf("500 が %d 件", serverErrors)
	}
	if dupRounds > 0 {
		t.Errorf("follow 通知が重複したラウンド = %d/%d (最大 %d 件)。"+
			"post_id NULL 経路は UNIQUE では塞げないので、"+
			"重複排除はアプリ側の書き方でしか担保できない",
			dupRounds, rounds, maxNotifs)
	}
	if zeroRounds > 0 {
		t.Errorf("follow 通知が 1 件も作られなかったラウンド = %d/%d", zeroRounds, rounds)
	}
}
