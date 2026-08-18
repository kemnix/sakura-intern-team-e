package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// TestRepliesCountBeyondMaxThreadDepth は「50 階層より深いスレッドで返信数が
// 過少カウントされた」バグの回帰テスト。60 段の直線スレッドを作り、ルート投稿の
// replies_count が 50 で頭打ちにならず 60 を返すことを確認する。
func TestRepliesCountBeyondMaxThreadDepth(t *testing.T) {
	f := newFixture(t)

	user := f.registerUser("thread")

	// maxThreadDepth (=50) より確実に深くする。
	const depth = 60
	if depth <= maxThreadDepth {
		t.Fatalf("テストの前提が壊れている: depth=%d は maxThreadDepth=%d 以下", depth, maxThreadDepth)
	}

	rootID := f.insertPost(user.id, "深いスレッドのルート", nil)
	parentID := rootID
	for i := 1; i <= depth; i++ {
		parentID = f.insertPost(user.id, fmt.Sprintf("返信 %d 段目", i), &parentID)
	}

	status, body, _ := f.do(&user, http.MethodGet, fmt.Sprintf("/posts/%d", rootID), nil)
	if status != http.StatusOK {
		t.Fatalf("GET /posts/%d = %d, body=%s", rootID, status, body)
	}

	var out struct {
		Post struct {
			RepliesCount int `json:"replies_count"`
		} `json:"post"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("レスポンス解析に失敗: %v (body=%s)", err, body)
	}

	if out.Post.RepliesCount == maxThreadDepth {
		t.Fatalf("replies_count = %d で maxThreadDepth と一致しており、深さ上限で打ち切られている（旧バグの再発）",
			out.Post.RepliesCount)
	}
	if out.Post.RepliesCount != depth {
		t.Fatalf("replies_count = %d, want %d", out.Post.RepliesCount, depth)
	}

	// 途中のノードから見ても残りの子孫数が正しいこと（ルートだけの偶然ではない）。
	firstReplyID := rootID
	if err := f.db.QueryRow(
		`SELECT id FROM posts WHERE parent_post_id = ?`, rootID,
	).Scan(&firstReplyID); err != nil {
		t.Fatalf("1 段目の返信 ID の取得に失敗: %v", err)
	}
	if got := f.h.countReplies(mustRequest(t, f.srv.URL), firstReplyID); got != depth-1 {
		t.Errorf("1 段目の返信の countReplies = %d, want %d", got, depth-1)
	}
}

// mustRequest は countReplies に渡すためだけの、コンテキスト付きリクエストを作る。
func mustRequest(t *testing.T, url string) *http.Request {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	return req
}
