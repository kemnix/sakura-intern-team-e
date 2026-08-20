package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"sakuravel/internal/middleware"
)

// TestUserPostsRepliesMatchSinglePost は GET /users/{id}/posts?type=replies が返す返信が、
// GET /posts/{id} が返す同じ返信と JSON として一致することを固定する。
// reply_to_username は一覧経路でしか通らない分岐なので、親を消した返信も併せて確かめる。
func TestUserPostsRepliesMatchSinglePost(t *testing.T) {
	f := newFixtureWithRoutes(t, func(mux *http.ServeMux, h *Handler, auth *middleware.Auth) {
		mux.Handle("GET /users/{user_id}/posts", auth.Optional(http.HandlerFunc(h.GetUserPosts)))
	})

	author := f.registerUser("parent")
	replier := f.registerUser("replier")

	liveParentID := f.insertPost(author.id, "生きている親", nil)
	liveReplyID := f.insertPost(replier.id, "生きている親への返信", &liveParentID)

	goneParentID := f.insertPost(author.id, "消える親", nil)
	orphanReplyID := f.insertPost(replier.id, "親が消えた返信", &goneParentID)
	if _, err := f.db.Exec(`DELETE FROM posts WHERE id = ?`, goneParentID); err != nil {
		t.Fatalf("親投稿の削除に失敗: %v", err)
	}

	var parentUsername string
	if err := f.db.QueryRow(`SELECT username FROM users WHERE id = ?`, author.id).Scan(&parentUsername); err != nil {
		t.Fatalf("親投稿の投稿者名の取得に失敗: %v", err)
	}

	listed := f.listReplies(&replier, replier.id)
	if len(listed) != 2 {
		t.Fatalf("返信一覧の件数 = %d, want 2", len(listed))
	}

	if got := listed[liveReplyID]["reply_to_username"]; got != parentUsername {
		t.Errorf("生きている親への返信の reply_to_username = %v, want %q", got, parentUsername)
	}
	if _, found := listed[orphanReplyID]["reply_to_username"]; found {
		t.Errorf("親が消えた返信に reply_to_username が付いている: %v", listed[orphanReplyID])
	}

	for _, id := range []int64{liveReplyID, orphanReplyID} {
		if single := f.getPost(&replier, id); !reflect.DeepEqual(listed[id], single) {
			t.Errorf("post %d: 一覧経路と単発経路の JSON が違う\n一覧 = %v\n単発 = %v", id, listed[id], single)
		}
	}
}

// listReplies は GET /users/{id}/posts?type=replies を叩き、投稿 ID をキーにした素の JSON を返す。
func (f *fixture) listReplies(u *testUser, userID int64) map[int64]map[string]any {
	f.t.Helper()

	status, body, _ := f.do(u, http.MethodGet, fmt.Sprintf("/users/%d/posts?type=replies", userID), nil)
	if status != http.StatusOK {
		f.t.Fatalf("GET /users/%d/posts?type=replies = %d, body=%s", userID, status, body)
	}
	var out struct {
		Posts []map[string]any `json:"posts"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		f.t.Fatalf("一覧レスポンスの解析に失敗: %v (body=%s)", err, body)
	}
	byID := make(map[int64]map[string]any, len(out.Posts))
	for _, p := range out.Posts {
		id, ok := p["id"].(float64)
		if !ok {
			f.t.Fatalf("一覧の要素に id が無い: %v", p)
		}
		byID[int64(id)] = p
	}
	return byID
}

// getPost は GET /posts/{id} を叩き、post オブジェクトの素の JSON を返す。
func (f *fixture) getPost(u *testUser, postID int64) map[string]any {
	f.t.Helper()

	status, body, _ := f.do(u, http.MethodGet, fmt.Sprintf("/posts/%d", postID), nil)
	if status != http.StatusOK {
		f.t.Fatalf("GET /posts/%d = %d, body=%s", postID, status, body)
	}
	var out struct {
		Post map[string]any `json:"post"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		f.t.Fatalf("単発レスポンスの解析に失敗: %v (body=%s)", err, body)
	}
	return out.Post
}
