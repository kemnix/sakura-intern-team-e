// Package realtime は SSE 配信のための購読管理を提供する。
package realtime

import "sync"

// subBuffer は購読者ごとのバッファ数。あふれた場合そのイベントは捨てられる。
const subBuffer = 8

type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// Hub は key（ユーザーID / スレッドのルート投稿ID）ごとに購読者を束ねる。
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

// Publish は key を購読している全員にイベントを送る。購読者がいなければ何もしない。
func (h *Hub) Publish(key int64, ev Event) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[key] {
		select {
		case ch <- ev:
		default: // バッファがあふれている購読者は取りこぼす
		}
	}
}
