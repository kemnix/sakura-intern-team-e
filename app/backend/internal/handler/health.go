package handler

import (
	"context"
	"net/http"
	"time"
)

// Healthz は死活監視用エンドポイント。DB への疎通まで確認して 200 を返す。
// AppRun のヘルスチェック・シンプル監視・CD のスモークテストが使用する。
func (h *Handler) Healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if err := h.DB.PingContext(ctx); err != nil {
		h.respondError(w, http.StatusServiceUnavailable, "db unreachable")
		return
	}
	h.respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
