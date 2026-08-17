package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sakuravel/internal/realtime"
	"time"
)

// pingInterval は接続を維持するためのコメント行を送る間隔。
const pingInterval = 25 * time.Second

// NotificationStream は自分宛ての通知を SSE で配信する。
func (h *Handler) NotificationStream(w http.ResponseWriter, r *http.Request) {
	myID, _ := h.currentUserID(r)
	ch, unsubscribe := h.Notifications.Subscribe(myID)
	defer unsubscribe()
	h.streamEvents(w, r, ch)
}

// ThreadStream は対象投稿が属するスレッドへの新規返信を SSE で配信する。
func (h *Handler) ThreadStream(w http.ResponseWriter, r *http.Request) {
	postID, err := pathID(r, "id")
	if err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ch, unsubscribe := h.Threads.Subscribe(h.threadRootID(r, postID))
	defer unsubscribe()
	h.streamEvents(w, r, ch)
}

func (h *Handler) streamEvents(w http.ResponseWriter, r *http.Request, ch <-chan realtime.Event) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.respondError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// リバースプロキシ経由でもバッファされず即座に流れるようにする
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ping := time.NewTicker(pingInterval)
	defer ping.Stop()

	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			b, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
