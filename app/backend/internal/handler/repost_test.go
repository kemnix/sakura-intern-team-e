package handler

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestRepostIsIdempotent は「リポストが原子的でも冪等でもなかった」バグの回帰テスト。
//
// 修正前の Repost は reposts / posts / notifications への書き込みが別々の文で、
// posts への INSERT ... ON DUPLICATE KEY UPDATE id = id は死んだコードだった
// （posts は PRIMARY KEY (id) の AUTO_INCREMENT しか持たず、INSERT が id を
// 指定しないためキー衝突が起きえない — migrations/003_repost_unique.sql 参照）。
// その結果 POST /reposts を 3 回叩くと reposts は 1 行なのに posts は 3 行、
// notifications も 3 行になり、reposts_count（reposts 由来）とタイムライン表示
// （posts 由来）が食い違った。
//
// このテストは同じ (user, post) への連打後に 3 テーブルとも 1 行だけであること、
// および毎回のレスポンスの reposts_count が 1 のままであることを保証する。
func TestRepostIsIdempotent(t *testing.T) {
	f := newFixture(t)

	author := f.registerUser("author")
	reposter := f.registerUser("reposter")
	postID := f.insertPost(author.id, "リポスト冪等性テストの元投稿", nil)

	const calls = 3
	for i := 1; i <= calls; i++ {
		status, body, _ := f.do(&reposter, http.MethodPost, "/reposts",
			map[string]int64{"post_id": postID})
		if status != http.StatusOK {
			t.Fatalf("%d 回目の POST /reposts = %d, body=%s", i, status, body)
		}
		var out struct {
			RepostsCount int `json:"reposts_count"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("%d 回目のレスポンス解析に失敗: %v (body=%s)", i, err, body)
		}
		if out.RepostsCount != 1 {
			t.Errorf("%d 回目の reposts_count = %d, want 1", i, out.RepostsCount)
		}
	}

	if got := f.countRows(
		`SELECT COUNT(*) FROM reposts WHERE user_id = ? AND post_id = ?`,
		reposter.id, postID,
	); got != 1 {
		t.Errorf("reposts の行数 = %d, want 1 (%d 回リポスト後)", got, calls)
	}

	if got := f.countRows(
		`SELECT COUNT(*) FROM posts
		 WHERE user_id = ? AND original_post_id = ? AND is_repost = TRUE`,
		reposter.id, postID,
	); got != 1 {
		t.Errorf("リポスト由来の posts の行数 = %d, want 1 (%d 回リポスト後)", got, calls)
	}

	if got := f.countRows(
		`SELECT COUNT(*) FROM notifications
		 WHERE user_id = ? AND actor_id = ? AND type = 'repost' AND post_id = ?`,
		author.id, reposter.id, postID,
	); got != 1 {
		t.Errorf("repost 通知の行数 = %d, want 1 (%d 回リポスト後)", got, calls)
	}
}

// TestRepostNonexistentPostReturns404 も同じバグの回帰テスト。
//
// posts には外部キーが無いため、修正前は存在しない post_id へのリポストでも
// reposts / posts / notifications に孤児行が書き込まれ、200 が返っていた。
// 修正後は元投稿の存在確認が書き込みより先に走り、404 を返して一切書き込まない。
func TestRepostNonexistentPostReturns404(t *testing.T) {
	f := newFixture(t)

	reposter := f.registerUser("ghost")

	// 確実に存在しない投稿 ID。
	missingPostID := int64(f.countRows(`SELECT COALESCE(MAX(id), 0) + 1000000 FROM posts`))

	status, body, _ := f.do(&reposter, http.MethodPost, "/reposts",
		map[string]int64{"post_id": missingPostID})
	if status != http.StatusNotFound {
		t.Fatalf("存在しない投稿への POST /reposts = %d, want 404 (body=%s)", status, body)
	}

	if got := f.countRows(
		`SELECT COUNT(*) FROM reposts WHERE user_id = ?`, reposter.id,
	); got != 0 {
		t.Errorf("reposts の行数 = %d, want 0（404 では書き込まない）", got)
	}
	if got := f.countRows(
		`SELECT COUNT(*) FROM posts WHERE user_id = ?`, reposter.id,
	); got != 0 {
		t.Errorf("posts の行数 = %d, want 0（404 では書き込まない）", got)
	}
	if got := f.countRows(
		`SELECT COUNT(*) FROM notifications WHERE actor_id = ?`, reposter.id,
	); got != 0 {
		t.Errorf("notifications の行数 = %d, want 0（404 では書き込まない）", got)
	}
}
