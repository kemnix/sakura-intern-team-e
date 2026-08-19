package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"sakuravel/internal/middleware"
	"sakuravel/internal/realtime"

	_ "github.com/go-sql-driver/mysql"
)

// testDBEnv は回帰テストが使う DB の DSN を渡す環境変数。
// 未設定ならテストは skip されるので、DB の無いマシンでも `go test ./...` は通る。
const testDBEnv = "TEST_DATABASE_URL"

// openTestDB は TEST_DATABASE_URL の指す DB を開く。未設定なら実行方法を示して skip する。
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv(testDBEnv)
	if dsn == "" {
		t.Skipf("%s が未設定のため skip。DB はホストに公開していないので、"+
			"docker compose up -d のうえ app-network 内から実行する:\n"+
			"  docker run --rm --network app-network -v \"$PWD\":/src -w /src \\\n"+
			"    -e %s='sakuravel:password@tcp(db:3306)/sakuravel?parseTime=true&charset=utf8mb4' \\\n"+
			"    golang:1.25 go test -count=1 -race ./internal/...\n"+
			"DSN には parseTime=true が必須（created_at を time.Time で受けるため）。",
			testDBEnv, testDBEnv)
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	// 再起動直後の DB で掴んだ死んだコネクションを踏むことがあるので数回試す。
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

// fixture は 1 テスト分の HTTP サーバと、テストが自分で作った行の後始末をまとめる。
// 共有 DB を汚さないよう、削除するのは自分が作成したユーザーと
// そのユーザーに紐づく行だけに限定する（TRUNCATE は行わない）。
type fixture struct {
	t       *testing.T
	db      *sql.DB
	h       *Handler
	srv     *httptest.Server
	userIDs []int64
}

// newFixture は cmd/api/main.go の routes() と同じ配線で
// 本物のミドルウェア + 本物のハンドラを httptest サーバに載せる。
func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWithRoutes(t, nil)
}

// newFixtureWithRoutes は newFixture の実体で、既定のルートに続けて
// テスト固有のルートを登録できる。同時実行テストが /likes や
// /users/{user_id}/follow を必要とするので配線だけを差し替え可能にしてある。
func newFixtureWithRoutes(
	t *testing.T,
	extra func(mux *http.ServeMux, h *Handler, auth *middleware.Auth),
) *fixture {
	t.Helper()

	db := openTestDB(t)
	h := &Handler{
		DB:            db,
		Notifications: realtime.NewHub(),
		Threads:       realtime.NewHub(),
	}
	auth := &middleware.Auth{DB: db}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /register", h.Register)
	mux.Handle("POST /reposts", auth.Required(http.HandlerFunc(h.Repost)))
	mux.Handle("GET /posts/{id}", auth.Optional(http.HandlerFunc(h.GetPost)))
	if extra != nil {
		extra(mux, h, auth)
	}

	srv := httptest.NewServer(mux)
	f := &fixture{t: t, db: db, h: h, srv: srv}
	t.Cleanup(srv.Close)
	t.Cleanup(f.cleanup)
	return f
}

// cleanup はテストが作成したユーザーと、そのユーザーに紐づく行だけを削除する。
func (f *fixture) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, id := range f.userIDs {
		stmts := []struct {
			query string
			args  []any
		}{
			{`DELETE FROM notifications WHERE user_id = ? OR actor_id = ?`, []any{id, id}},
			// fan-out 用のイベント行もテストが作る側なので一緒に消す。返信のイベントは
			// 宛先がスレッドの根の投稿 ID なので、投稿を消す前に投稿側から辿って消す。
			{`DELETE FROM sse_events
			  WHERE (kind = 'notification' AND subject_id = ?)
			     OR (kind = 'reply' AND post_id IN (SELECT id FROM posts WHERE user_id = ?))`,
				[]any{id, id}},
			{`DELETE FROM reposts WHERE user_id = ?`, []any{id}},
			// フォローは自分が張った側も張られた側も、このテストが作った行なので消す。
			{`DELETE FROM follows WHERE follower_id = ? OR followee_id = ?`, []any{id, id}},
			{`DELETE FROM likes WHERE user_id = ?`, []any{id}},
			{`DELETE FROM footprints WHERE user_id = ? OR visitor_id = ?`, []any{id, id}},
			{`DELETE FROM posts WHERE user_id = ?`, []any{id}},
			{`DELETE FROM sessions WHERE user_id = ?`, []any{id}},
			{`DELETE FROM users WHERE id = ?`, []any{id}},
		}
		for _, s := range stmts {
			if _, err := f.db.ExecContext(ctx, s.query, s.args...); err != nil {
				f.t.Errorf("cleanup %q: %v", s.query, err)
			}
		}
	}
}

// testUser はテスト用に登録したユーザーとそのセッション Cookie。
type testUser struct {
	id     int64
	cookie *http.Cookie
}

// registerUser は POST /register を叩いて実ユーザーとセッションを作る。
// ユーザー名はナノ秒付きなので、既存の投入済みデータとは衝突しない。
func (f *fixture) registerUser(role string) testUser {
	f.t.Helper()

	uniq := fmt.Sprintf("t_%s_%d", role, time.Now().UnixNano())
	status, body, resp := f.do(nil, http.MethodPost, "/register", map[string]string{
		"username":     uniq,
		"display_name": role,
		"email":        uniq + "@example.test",
		"password":     "regression-test-password",
	})
	if status != http.StatusCreated {
		f.t.Fatalf("POST /register = %d, body=%s", status, body)
	}

	var out struct {
		User struct {
			ID int64 `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		f.t.Fatalf("register response の解析に失敗: %v (body=%s)", err, body)
	}
	if out.User.ID == 0 {
		f.t.Fatalf("register response に user.id が無い: %s", body)
	}
	f.userIDs = append(f.userIDs, out.User.ID)

	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "session_id" {
			cookie = c
		}
	}
	if cookie == nil {
		f.t.Fatalf("register が session_id Cookie を返さなかった")
	}
	return testUser{id: out.User.ID, cookie: cookie}
}

// do はテストサーバにリクエストを送る。body が nil ならボディ無し。
func (f *fixture) do(u *testUser, method, path string, body any) (int, []byte, *http.Response) {
	f.t.Helper()

	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			f.t.Fatalf("リクエストボディの marshal に失敗: %v", err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, f.srv.URL+path, reader)
	if err != nil {
		f.t.Fatalf("http.NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if u != nil {
		req.AddCookie(u.cookie)
	}

	resp, err := f.srv.Client().Do(req)
	if err != nil {
		f.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		f.t.Fatalf("レスポンスの読み取りに失敗: %v", err)
	}
	return resp.StatusCode, respBody, resp
}

// insertPost はテスト用の投稿行を直接 INSERT する（テスト対象ではなく前提データの用意）。
func (f *fixture) insertPost(userID int64, content string, parentID *int64) int64 {
	f.t.Helper()

	res, err := f.db.Exec(
		`INSERT INTO posts (user_id, content, parent_post_id) VALUES (?, ?, ?)`,
		userID, content, parentID,
	)
	if err != nil {
		f.t.Fatalf("fixture の投稿 INSERT に失敗: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		f.t.Fatalf("LastInsertId: %v", err)
	}
	return id
}

// countRows は 1 件の COUNT(*) を取る小さなヘルパー。
func (f *fixture) countRows(query string, args ...any) int {
	f.t.Helper()

	var n int
	if err := f.db.QueryRow(query, args...).Scan(&n); err != nil {
		f.t.Fatalf("count %q: %v", query, err)
	}
	return n
}
