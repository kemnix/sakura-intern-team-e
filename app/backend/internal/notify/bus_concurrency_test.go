package notify

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"os"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"sakuravel/internal/realtime"

	_ "github.com/go-sql-driver/mysql"
)

// このファイルは複数プロセス構成の心臓部である sse_events バスの
// 同時実行テストである。これまでのテストは handler パッケージの逐次テストだけで、
// 「複数プロセスが同時に通知を書いたとき、どのプロセスのサブスクライバーにも漏れなく届くか」
// と「GC のロックが漏れて全プロセスの掃除が恒久的に止まらないか」は未検証だった。

// testDBEnv は回帰テストが使う DB の DSN を渡す環境変数。
// internal/handler/dbtest_test.go と同じ規約（未設定なら skip）にそろえてある。
const testDBEnv = "TEST_DATABASE_URL"

// openTestDB は TEST_DATABASE_URL の指す DB を開く。未設定なら実行方法を示して skip する。
// handler パッケージの同名ヘルパーはテストコードなので別パッケージからは参照できず、
// そのためだけに非テストコードへ公開ヘルパーを足すのは避けてここに置いている。
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv(testDBEnv)
	if dsn == "" {
		t.Skipf("%s が未設定のため skip。実行するには DB を用意して次を実行する:\n"+
			"  %s='sakuravel:password@tcp(127.0.0.1:3306)/sakuravel?parseTime=true&charset=utf8mb4' \\\n"+
			"    go test ./internal/notify/ -v",
			testDBEnv, testDBEnv)
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for attempt := 1; ; attempt++ {
		err = db.PingContext(ctx)
		if err == nil {
			break
		}
		if attempt >= 3 {
			db.Close()
			t.Fatalf("%s の DB に ping できない: %v", testDBEnv, err)
		}
		time.Sleep(time.Second)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// markerUserID はこのテスト専用の宛先 ID を返す。sse_events に外部キーは
// 無いので、シードデータや他テストの行と衝突しない大きな値を使い、
// 後始末もこの値の行だけに限定する。
func markerUserID(t *testing.T) int64 {
	t.Helper()
	return 9_000_000_000_000 + time.Now().UnixNano()%1_000_000_000
}

// runBurst は n 本の goroutine をスタートバリアで同時に解き放ち、各々のエラーを返す。
// goroutine 内から t.Fatalf は呼べないので、判定は呼び出し元で行う。
func runBurst(n int, fn func(i int) error) []error {
	errs := make([]error, n)
	start := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = fn(i)
		}(i)
	}
	close(start)
	wg.Wait()
	return errs
}

// syncBuffer は log の出力を取り込むための、複数 goroutine から書ける bytes.Buffer。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLog は log の出力を横取りする。GC はエラーをログにしか出さないので、
// 「エラーが出ていないこと」を確かめる唯一の手段になる。
func captureLog(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// recorder はイベントを 1 件も落とさない配信先。realtime.Hub は購読バッファが
// 8 件しかなく、あふれた分を黙って捨てるので、そのまま使うと「バスが取りこぼしたのか」
// 「hub が捨てたのか」を切り分けられない。バス単体の欠落を測るためのもの。
type recorder struct {
	mu      sync.Mutex
	byPost  map[int64]int
	updated chan struct{}
}

func newRecorder() *recorder {
	return &recorder{byPost: make(map[int64]int), updated: make(chan struct{}, 1)}
}

func (r *recorder) Publish(key int64, ev realtime.Event) {
	data, ok := ev.Data.(map[string]any)
	if !ok {
		return
	}
	postID, ok := data["post_id"].(*int64)
	if !ok || postID == nil {
		return
	}
	r.mu.Lock()
	r.byPost[*postID]++
	r.mu.Unlock()
	select {
	case r.updated <- struct{}{}:
	default:
	}
}

// counts は現時点で受け取ったイベントを post_id ごとに数えて返す。
func (r *recorder) counts() map[int64]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[int64]int, len(r.byPost))
	for k, v := range r.byPost {
		out[k] = v
	}
	return out
}

// TestBusDeliversEveryConcurrentEvent は「複数プロセスが同時に通知を書くと、SSE のサブスクライバーに
// 一生届かない通知が出る」障害に対するテスト。id は commit 時ではなく INSERT 時に採番される
// ので、同時書き込みでは後から可視になった小さい id をカーソルが飛び越えうる。欠落は許さない。
func TestBusDeliversEveryConcurrentEvent(t *testing.T) {
	t.Setenv("NOTIFY_POLL_INTERVAL_MS", "50")
	t.Setenv("NOTIFY_GC_INTERVAL_SEC", "3600") // このテスト中に GC を走らせない

	db := openTestDB(t)
	userID := markerUserID(t)
	cleanupEvents(t, db, userID)

	rec := newRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	bus := New(db, rec, nil, nil)
	if err := bus.Start(ctx); err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	defer func() {
		cancel()
		bus.Wait()
	}()

	// ポーラの初期カーソルは起動時の MAX(id) なので、起動と同時に書くと
	// 「起動前の行」とみなされて配られない。番兵が届くまで待ってから本番に入る。
	if !waitForSentinel(t, db, rec, userID) {
		t.Fatal("ポーラが番兵イベントを配らないまま制限時間に達した")
	}

	// 本番: N 本の goroutine が同時に 1 件ずつイベント行を書く。
	events := testEnvInt("BUS_EVENTS", 16)
	writeErrs := runBurst(events, func(i int) error {
		postID := int64(i + 1)
		return Publish(context.Background(), db, userID, "like", &postID)
	})
	for i, err := range writeErrs {
		if err != nil {
			t.Fatalf("イベント %d の Publish に失敗: %v", i+1, err)
		}
	}

	// pollLag + ポーリング間隔ぶんの遅延があるので待ちは多めに取る。
	got := waitForCounts(t, rec, events, 30*time.Second)
	delivered, total, dup := 0, 0, 0
	var missing []int64
	for i := int64(1); i <= int64(events); i++ {
		n := got[i]
		total += n
		switch {
		case n == 0:
			missing = append(missing, i)
		default:
			delivered++
			if n > 1 {
				dup++
			}
		}
	}
	t.Logf("投入 %d 件 / 到達 %d 種 (延べ %d 件, 重複 %d 種)", events, delivered, total, dup)
	if len(missing) > 0 {
		t.Errorf("%d/%d 件が届かなかった。欠落した post_id=%v。"+
			"at-least-once を満たしていない（同時 INSERT で採番された id の穴を"+
			"カーソルが飛び越えている）", len(missing), events, missing)
	}
}

// TestHubDeliversBurstToSubscriber は「同時に通知が集中したとき、SSE でつながっている
// サブスクライバーにだけ届かない」障害に対するテスト。realtime.Hub は購読バッファ (8 件) の超過分を
// 捨てるが、イベントは「再取得の合図」でしかないので、守るのは全件到達ではなく沈黙しないこと。
func TestHubDeliversBurstToSubscriber(t *testing.T) {
	t.Setenv("NOTIFY_POLL_INTERVAL_MS", "50")
	t.Setenv("NOTIFY_GC_INTERVAL_SEC", "3600")

	db := openTestDB(t)
	userID := markerUserID(t)
	cleanupEvents(t, db, userID)

	hub := realtime.NewHub()
	ch, unsubscribe := hub.Subscribe(userID)

	// 受信側は即座に吸い出す。ここで溜め込むと、測りたい hub のバッファあふれではなく
	// テストの遅さを測ることになる。
	received := make(chan int64, 4096)
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for ev := range ch {
			data, ok := ev.Data.(map[string]any)
			if !ok {
				continue
			}
			postID, ok := data["post_id"].(*int64)
			if !ok || postID == nil {
				continue
			}
			received <- *postID
		}
	}()

	rec := newRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	bus := New(db, teeDeliverer{hub, rec}, nil, nil)
	if err := bus.Start(ctx); err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	defer func() {
		cancel()
		bus.Wait()
		// 購読を切ると ch が閉じ、取り出し用の goroutine が抜ける。
		// 先に <-drained を待つと ch が閉じられず自分で自分を止めてしまう。
		unsubscribe()
		<-drained
	}()

	if !waitForSentinel(t, db, rec, userID) {
		t.Fatal("ポーラが番兵イベントを配らないまま制限時間に達した")
	}
	// 番兵の分をサブスクライバー側からも捨てておく。
	drainReceived(received, 200*time.Millisecond)

	events := testEnvInt("BUS_EVENTS", 16)
	writeErrs := runBurst(events, func(i int) error {
		postID := int64(i + 1)
		return Publish(context.Background(), db, userID, "like", &postID)
	})
	for i, err := range writeErrs {
		if err != nil {
			t.Fatalf("イベント %d の Publish に失敗: %v", i+1, err)
		}
	}

	// バス側に全部届いたことを先に確かめてから、サブスクライバー側の取りこぼしを数える。
	busCounts := waitForCounts(t, rec, events, 30*time.Second)
	busGot := 0
	for i := int64(1); i <= int64(events); i++ {
		if busCounts[i] > 0 {
			busGot++
		}
	}
	deadline := time.After(5 * time.Second)
	subGot := make(map[int64]int, events)
collect:
	for len(subGot) < events {
		select {
		case postID := <-received:
			if postID >= 1 && postID <= int64(events) {
				subGot[postID]++
			}
		case <-deadline:
			break collect
		}
	}
	if busGot < events {
		t.Errorf("バスに %d/%d 件しか届いていない。サブスクライバー側の取りこぼしとは別の欠陥である", busGot, events)
	}
	if len(subGot) == 0 {
		t.Errorf("バスには %d/%d 件届いたのにサブスクライバーには 1 件も届かなかった。"+
			"合図が 1 件も出なければクライアントは再取得せず、通知は画面に出ない", busGot, events)
	}
	t.Logf("投入 %d 件 / バス到達 %d 種 / サブスクライバー到達 %d 種（差の %d 件は realtime.Hub の"+
		"購読バッファ 8 件超過分で、Publish の default 節が捨てている）",
		events, busGot, len(subGot), busGot-len(subGot))
}

// teeDeliverer は同じイベントを 2 つの配信先に流す。サブスクライバーに届いた数と
// バスが配った数を同じ 1 回の実行で比べるために使う。
type teeDeliverer struct {
	hub *realtime.Hub
	rec *recorder
}

func (d teeDeliverer) Publish(key int64, ev realtime.Event) {
	d.rec.Publish(key, ev)
	d.hub.Publish(key, ev)
}

// TestReplyReachesSubscriberOnAnotherProcess は「返信を書いたプロセスと、そのスレッドを
// 開いているサブスクライバーがつないでいるプロセスが別だと、返信が一生届かない」障害に対するテスト。
// イベント行には返信の post_id しか載らないので、サブスクライバーを抱えている側のプロセスが本文を
// 組み立て直し、1 プロセス構成のときと同じイベントを配れなければならない。
func TestReplyReachesSubscriberOnAnotherProcess(t *testing.T) {
	t.Setenv("NOTIFY_POLL_INTERVAL_MS", "50")
	t.Setenv("NOTIFY_GC_INTERVAL_SEC", "3600")

	db := openTestDB(t)
	sentinelUserID := markerUserID(t)
	rootID := sentinelUserID + 1 // スレッドの根の投稿 ID。sse_events に外部キーは無い
	cleanupEvents(t, db, sentinelUserID, rootID)

	// プロセス A: 返信を書く側。このスレッドのサブスクライバーは抱えていない。
	hydratedOnA := make(chan int64, 4)
	busA := New(db, nil, realtime.NewHub(),
		func(ctx context.Context, postID int64) (realtime.Event, error) {
			hydratedOnA <- postID
			return realtime.Event{}, nil
		})

	// プロセス B: このスレッドのサブスクライバーを抱えている側。hub も Bus も A とは別物である。
	hubB := realtime.NewHub()
	ch, unsubscribe := hubB.Subscribe(rootID)
	defer unsubscribe()

	// 本文の引き直しは handler 側の責務なので、ここでは「配る側で組み立てた」ことが
	// 分かる固定のイベントを返し、それがそのままサブスクライバーに届くかだけを見る。
	const replyPostID = int64(-4242) // 他テストの post_id と衝突しない目印
	want := realtime.Event{Type: "reply", Data: map[string]any{"id": replyPostID}}
	hydratedOnB := make(chan int64, 4)
	rec := newRecorder()
	busB := New(db, rec, hubB,
		func(ctx context.Context, postID int64) (realtime.Event, error) {
			hydratedOnB <- postID
			return want, nil
		})

	ctx, cancel := context.WithCancel(context.Background())
	for i, bus := range []*Bus{busA, busB} {
		if err := bus.Start(ctx); err != nil {
			t.Fatalf("bus[%d].Start: %v", i, err)
		}
	}
	defer func() {
		cancel()
		busA.Wait()
		busB.Wait()
	}()

	// 番兵が届くまではポーラの初期カーソルと競る（既存テストと同じ理由）。
	if !waitForSentinel(t, db, rec, sentinelUserID) {
		t.Fatal("ポーラが番兵イベントを配らないまま制限時間に達した")
	}

	// プロセス A が返信のイベント行を書く。
	if err := PublishReply(context.Background(), db, rootID, replyPostID); err != nil {
		t.Fatalf("PublishReply: %v", err)
	}

	select {
	case got := <-ch:
		if !reflect.DeepEqual(got, want) {
			t.Errorf("サブスクライバーが受け取ったイベント = %+v, want %+v（ワイヤ形式が変わっている）",
				got, want)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("別プロセスで書かれた返信がサブスクライバーに届かない")
	}

	select {
	case got := <-hydratedOnB:
		if got != replyPostID {
			t.Errorf("組み立て直した post_id = %d, want %d", got, replyPostID)
		}
	default:
		t.Error("配信したプロセスが本文を組み立て直していない（行には id しか無い）")
	}
	if n := len(hydratedOnA); n > 0 {
		t.Errorf("サブスクライバーを抱えていないプロセスが本文を %d 件引き直している", n)
	}
}

// drainReceived は残っている受信済みイベントを捨てる。
func drainReceived(received <-chan int64, wait time.Duration) {
	timeout := time.After(wait)
	for {
		select {
		case <-received:
		case <-timeout:
			return
		}
	}
}

// waitForCounts は post_id 1..events がすべて届くか制限時間に達するまで待ち、
// その時点の集計を返す。
func waitForCounts(t *testing.T, rec *recorder, events int, wait time.Duration) map[int64]int {
	t.Helper()

	deadline := time.After(wait)
	for {
		got := rec.counts()
		complete := true
		for i := int64(1); i <= int64(events); i++ {
			if got[i] == 0 {
				complete = false
				break
			}
		}
		if complete {
			return got
		}
		select {
		case <-rec.updated:
		case <-deadline:
			return rec.counts()
		}
	}
}

// waitForSentinel は番兵イベントがバスから配られるまで待つ。ポーラのカーソル
// 初期化と競ると最初の数件が「起動前の行」として無視されるため、
// 届かなければ書き直して再試行する。
func waitForSentinel(t *testing.T, db *sql.DB, rec *recorder, userID int64) bool {
	t.Helper()

	const sentinelPostID = int64(-1)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		postID := sentinelPostID
		if err := Publish(context.Background(), db, userID, "like", &postID); err != nil {
			t.Fatalf("番兵イベントの Publish に失敗: %v", err)
		}
		retry := time.After(3 * time.Second)
		for waiting := true; waiting; {
			if rec.counts()[sentinelPostID] > 0 {
				return true
			}
			select {
			case <-rec.updated:
			case <-retry:
				waiting = false
			}
		}
	}
	return false
}

// cleanupEvents はテストが書いたイベント行だけを後始末する。
func cleanupEvents(t *testing.T, db *sql.DB, subjectIDs ...int64) {
	t.Helper()

	t.Cleanup(func() {
		for _, id := range subjectIDs {
			if _, err := db.Exec(
				`DELETE FROM sse_events WHERE subject_id = ?`, id,
			); err != nil {
				t.Errorf("cleanup sse_events: %v", err)
			}
		}
	})
}

// testEnvInt は再現率を上げたいときだけ環境変数で回数を増やすためのもの。
// 未設定・不正値・0 以下なら fallback を返す。
func testEnvInt(key string, fallback int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

// TestConcurrentGCHoldsLockAndReleasesIt は「複数プロセスの GC が同時に走り、
// 名前付きロックが解放されないまま漏れて、全プロセスの掃除が恒久的に止まる」
// 障害に対するテスト。gc.go 自身が「GET_LOCK / DELETE / RELEASE_LOCK は
// 必ず同一の *sql.Conn 上で」と警告している経路で、そこが崩れても
// 単体では気付けない（GC は失敗をログに出すだけで動き続ける）。
func TestConcurrentGCHoldsLockAndReleasesIt(t *testing.T) {
	// タイミングは意図的に決め打ちしている。既定は保持期間 3600 秒 / GC 間隔 60 秒なので、
	// 素直に Start して待つと 1 時間経った行が 1 分後に消えるのを待つことになり、
	// テストは何も観測できないまま終わる。ここでは (1) 保持期間を 60 秒に縮め、
	// (2) 消される側の行は最初から 1 時間前の created_at で INSERT して即対象にし、
	// (3) ticker を待たず gcOnce を直接叩く、の 3 点で待ち時間を消している。
	// 保持期間を 1 秒などにしないのは、並走している他パッケージのテストが
	// 書いたばかりのイベント行まで巻き込んで消してしまうため。
	t.Setenv("NOTIFY_RETENTION_SEC", "60")
	// バッチを小さくして、1 回の gcOnce の中で DELETE を複数周させる。
	t.Setenv("NOTIFY_GC_BATCH", "2")

	db := openTestDB(t)
	oldUserID := markerUserID(t)
	freshUserID := oldUserID + 1
	cleanupEvents(t, db, oldUserID, freshUserID)

	const oldRows, freshRows = 7, 3
	for i := 0; i < oldRows; i++ {
		if _, err := db.Exec(
			`INSERT INTO sse_events (kind, subject_id, type, post_id, created_at)
			 VALUES ('notification', ?, 'like', NULL, NOW(6) - INTERVAL 3600 SECOND)`, oldUserID,
		); err != nil {
			t.Fatalf("保持期間切れ行の INSERT に失敗: %v", err)
		}
	}
	for i := 0; i < freshRows; i++ {
		if err := Publish(context.Background(), db, freshUserID, "like", nil); err != nil {
			t.Fatalf("保持期間内の行の INSERT に失敗: %v", err)
		}
	}

	logs := captureLog(t)

	// 2 つの Bus (= 2 プロセス相当) が同じ DB に対して同時に GC を回す。
	buses := []*Bus{New(db, nil, nil, nil), New(db, nil, nil, nil)}
	const gcCallers = 8
	errs := runBurst(gcCallers, func(i int) error {
		return buses[i%len(buses)].gcOnce(context.Background())
	})
	for i, err := range errs {
		if err != nil {
			t.Errorf("gcOnce[%d] がエラーを返した: %v", i, err)
		}
	}

	if got := countRows(t, db,
		`SELECT COUNT(*) FROM sse_events WHERE subject_id = ?`, oldUserID,
	); got != 0 {
		t.Errorf("保持期間切れの行が %d 件残っている, want 0", got)
	}
	if got := countRows(t, db,
		`SELECT COUNT(*) FROM sse_events WHERE subject_id = ?`, freshUserID,
	); got != freshRows {
		t.Errorf("保持期間内の行が %d 件, want %d（GC が消しすぎている）", got, freshRows)
	}

	// ロックが解放されていること。ここが 0 のまま返ると、以降どのプロセスの
	// GC も GET_LOCK を取れず、イベント表が無限に育つ。
	var free sql.NullInt64
	if err := db.QueryRow(`SELECT IS_FREE_LOCK(?)`, gcLockName).Scan(&free); err != nil {
		t.Fatalf("IS_FREE_LOCK: %v", err)
	}
	if !free.Valid || free.Int64 != 1 {
		t.Errorf("IS_FREE_LOCK(%q) = %v, want 1（ロックが漏れている）", gcLockName, free)
	}

	if out := logs.String(); bytes.Contains([]byte(out), []byte("notify: gc:")) {
		t.Errorf("GC がエラーをログに出している:\n%s", out)
	}
}

// TestGCSkipsWhenLockHeldElsewhere は「他プロセスが GC 中でも自分の GC が
// 待たずに諦め、ロックを壊さずに戻る」ことを確かめる。GET_LOCK の第 2 引数 0 は
// 待たない指定であり、ここが待つ実装に変わると全プロセスの GC が直列に詰まる。
func TestGCSkipsWhenLockHeldElsewhere(t *testing.T) {
	// ここも ticker は使わず gcOnce を直接叩き、削除対象の行は
	// 1 時間前の created_at で入れて保持期間 60 秒に対して即対象にしている。
	t.Setenv("NOTIFY_RETENTION_SEC", "60")

	db := openTestDB(t)
	oldUserID := markerUserID(t)
	cleanupEvents(t, db, oldUserID)
	if _, err := db.Exec(
		`INSERT INTO sse_events (kind, subject_id, type, post_id, created_at)
		 VALUES ('notification', ?, 'like', NULL, NOW(6) - INTERVAL 3600 SECOND)`, oldUserID,
	); err != nil {
		t.Fatalf("保持期間切れ行の INSERT に失敗: %v", err)
	}

	// 別プロセスが GC 中の状態を、専用コネクションでロックを握って再現する。
	ctx := context.Background()
	holder, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn: %v", err)
	}
	var locked sql.NullInt64
	if err := holder.QueryRowContext(ctx, `SELECT GET_LOCK(?, 5)`, gcLockName).Scan(&locked); err != nil {
		t.Fatalf("GET_LOCK: %v", err)
	}
	if !locked.Valid || locked.Int64 != 1 {
		t.Fatalf("テスト側が GET_LOCK を取れなかった: %v", locked)
	}

	logs := captureLog(t)
	done := make(chan error, 1)
	go func() { done <- New(db, nil, nil, nil).gcOnce(ctx) }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("ロック取得に失敗した gcOnce がエラーを返した: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("gcOnce がロックを待って戻ってこない（GET_LOCK の第 2 引数 0 が効いていない）")
	}

	if got := countRows(t, db,
		`SELECT COUNT(*) FROM sse_events WHERE subject_id = ?`, oldUserID,
	); got != 1 {
		t.Errorf("保持期間切れの行が %d 件, want 1。"+
			"ロックを取れなかった GC が削除まで進んでいる", got)
	}

	var released sql.NullInt64
	if err := holder.QueryRowContext(ctx, `SELECT RELEASE_LOCK(?)`, gcLockName).Scan(&released); err != nil {
		t.Fatalf("RELEASE_LOCK: %v", err)
	}
	if !released.Valid || released.Int64 != 1 {
		t.Errorf("RELEASE_LOCK = %v, want 1（GC 側がロックを横取りしている）", released)
	}
	holder.Close()

	if out := logs.String(); bytes.Contains([]byte(out), []byte("notify: gc:")) {
		t.Errorf("GC がエラーをログに出している:\n%s", out)
	}
}

// countRows は 1 件の COUNT(*) を取る小さなヘルパー。
func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()

	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}
