// Package realtime は SSE 配信のための購読管理を提供する。
package realtime

import "sync"

// subBuffer はサブスクライバーごとのバッファ数。あふれた場合そのイベントは捨てられる。
const subBuffer = 8

type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// Hub は key（ユーザーID / スレッドのルート投稿ID）ごとにサブスクライバーを束ねる。
type Hub struct {
	mu   sync.Mutex
	subs map[int64]map[chan Event]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: make(map[int64]map[chan Event]struct{})}
}

// Subscribe は key 宛てのイベントを受け取るチャネルと、購読解除する関数を返す。
func (h *Hub) Subscribe(key int64) (<-chan Event, func()) {
	ch := make(chan Event, subBuffer)

	h.mu.Lock()
	if h.subs[key] == nil {
		h.subs[key] = make(map[chan Event]struct{})
	}
	h.subs[key][ch] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if subs, ok := h.subs[key]; ok {
				delete(subs, ch)
				if len(subs) == 0 {
					delete(h.subs, key)
				}
			}
			close(ch)
		})
	}
	return ch, unsubscribe
}

// HasSubscribers は key 宛てのサブスクライバーがこのプロセスにいるかを返す。
// サブスクライバーのいない宛先に対する配信処理を呼び出し側が丸ごと省くために使う。
func (h *Hub) HasSubscribers(key int64) bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs[key]) > 0
}

// Publish は key を購読している全員にイベントを送る。サブスクライバーがいなければ何もしない。
func (h *Hub) Publish(key int64, ev Event) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[key] {
		select {
		case ch <- ev:
		default: // バッファがあふれているサブスクライバーは取りこぼす
		}
	}
}
