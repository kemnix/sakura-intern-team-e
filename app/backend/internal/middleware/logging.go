package middleware

import (
	"log/slog"
	"net/http"
	"time"
)

// statusRecoder は  ResponseWriter をラップして書き込まれたステータスコードを記録
// http.ResponseWriter 単体では書き込み後のステータスコードを読み出せない
type statusRecoder struct {
	http.ResponseWriter
	status int
}

func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request){
			start := time.Now()
			rec := &statusRecoder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			slog.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"remote_addr", r.RemoteAddr,
			)
	})
}