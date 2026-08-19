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

// WriteHeader はステータスコードを記録してから本来の処理に渡す
func (r *statusRecoder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush は SSE が接続を維持したままデータを流せるよう、元の ResponseWriter に委譲する。
// これが無いと SSE ハンドラの型アサーションが失敗し、500 を返して接続が張れない。
func (r *statusRecoder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
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