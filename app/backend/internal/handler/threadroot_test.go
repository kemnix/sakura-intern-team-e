package handler

import (
	"fmt"
	"testing"
)

// TestThreadRootIDBeyondMaxThreadDepth は深さ上限より深いスレッドでも、末端の返信から見た根が
// 本当の根で、サブスクライブしている側と配信側が同じ根に解決されることを見る。
func TestThreadRootIDBeyondMaxThreadDepth(t *testing.T) {
	f := newFixture(t)

	user := f.registerUser("threadroot")

	const depth = 60
	if depth <= maxThreadDepth {
		t.Fatalf("テストの前提が壊れている: depth=%d は maxThreadDepth=%d 以下", depth, maxThreadDepth)
	}

	rootID := f.insertPost(user.id, "深いスレッドのルート", nil)
	ids := []int64{rootID}
	parentID := rootID
	for i := 1; i <= depth; i++ {
		parentID = f.insertPost(user.id, fmt.Sprintf("返信 %d 段目", i), &parentID)
		ids = append(ids, parentID)
	}

	req := mustRequest(t, f.srv.URL)
	got := f.h.threadRootID(req, parentID)
	if got == ids[depth-maxThreadDepth] && got != rootID {
		t.Fatalf("threadRootID = %d は末端から %d 段上の投稿で、深さ上限で打ち切られている",
			got, maxThreadDepth)
	}
	if got != rootID {
		t.Fatalf("threadRootID = %d, want %d", got, rootID)
	}

	// サブスクライブ側のキー（末端の返信）と配信キー（その親）が食い違うと、無言で何も届かなくなる。
	if sub, pub := got, f.h.threadRootID(req, ids[depth-1]); sub != pub {
		t.Errorf("サブスクライブ側のキー %d と配信キー %d が食い違っている", sub, pub)
	}
}
