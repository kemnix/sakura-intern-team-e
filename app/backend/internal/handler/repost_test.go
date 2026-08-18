package handler

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestRepostIsIdempotent は「リポストが原子的でも冪等でもなかった」バグの回帰テスト。
// 同じ (user, post) への連打後に reposts / posts / notifications がいずれも 1 行だけで、
// 毎回のレスポンスの reposts_count も 1 のままであることを保証する。
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

// TestRepostNonexistentPostReturns404 も同じバグの回帰テスト。posts に外部キーが
// 無いため、存在しない post_id へのリポストが孤児行を書き込まないことを確認する。
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
