package handler

import (
	"net/http"
	"testing"
	"time"

	"sakuravel/internal/middleware"
)

// TestConcurrentWriteToDeletedPostReturnsNotFound は「操作している投稿が処理の途中で消えた」ときの応答を測る。
// 存在検査と INSERT が別文である以上 006 の外部キーが 1452 で拒む窓があり、そこは 404 になる。
func TestConcurrentWriteToDeletedPostReturnsNotFound(t *testing.T) {
	f := newFixtureWithRoutes(t, func(mux *http.ServeMux, h *Handler, auth *middleware.Auth) {
		mux.Handle("POST /likes", auth.Required(http.HandlerFunc(h.Like)))
	})
	author := f.registerUser("dead-author")
	actor := f.registerUser("dead-actor")

	for _, path := range []string{"/reposts", "/likes"} {
		t.Run(path, func(t *testing.T) {
			postID := f.insertPost(author.id, "削除と競合する投稿", nil)

			// 未コミットの DELETE で押さえる。存在検査は通り、INSERT が外部キー検査で待たされる。
			tx, err := f.db.Begin()
			if err != nil {
				t.Fatalf("Begin: %v", err)
			}
			defer tx.Rollback()
			if _, err := tx.Exec(`DELETE FROM posts WHERE id = ?`, postID); err != nil {
				t.Fatalf("DELETE: %v", err)
			}

			res := runBurst(2, func(i int) (int, error) {
				if i == 0 {
					return postJSON(f, actor, path, map[string]int64{"post_id": postID})
				}
				// 待たせている INSERT を、投稿が消えた状態で再開させる。
				time.Sleep(300 * time.Millisecond)
				return 0, tx.Commit()
			})
			if err := res.firstErr(); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			if got := res.statuses[0]; got != http.StatusNotFound {
				t.Errorf("POST %s = %d, want %d（消えた投稿への書き込みは 404）",
					path, got, http.StatusNotFound)
			}
		})
	}
}
