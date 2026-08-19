package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	appdb "sakuravel/internal/db"
	"sakuravel/internal/handler"
	"sakuravel/internal/middleware"
	"sakuravel/internal/notify"
	"sakuravel/internal/realtime"
)

func main() {
	//構造化ログ（JSON）を stdout へ、モニタリングスイートで検索、絞り込みしやすくするため
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	db := appdb.New()
	defer db.Close()

	h := &handler.Handler{
		DB:            db,
		CookieSecure:  os.Getenv("COOKIE_SECURE") == "true",
		Notifications: realtime.NewHub(),
		Threads:       realtime.NewHub(),
	}
	auth := &middleware.Auth{DB: db}

	// SSE バス。複数の API プロセスが 1 つの DB を共有する構成で、他プロセスで発生した通知と
	// 返信を自分がつないでいるサブスクライバーへ届ける。Start はポーラのカーソルを確定して
	// から戻るので、HTTP の受付はその後に始める（確定前につないだ分は取りこぼすため）。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	bus := notify.New(db, h.Notifications, h.Threads, h.ReplyEvent)
	if err := bus.Start(ctx); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()

	// CORS ミドルウェア
	// すべてのパスがこの行を通るのでここに logging を挟むだけで全エンドポイントがログとして出力される
	// CORS より外側に置くのは CORS や認証で弾かれたリクエストも記録する必要があるため。
	mux.Handle("/", middleware.Logging(corsMiddleware(routes(h, auth))))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	// ListenAndServe には戻る経路が無いので待つのはシグナル側。main が戻れば
	// プロセスは終了する（HTTP の graceful shutdown はここでは扱わない）。
	go func() {
		log.Printf("starting server on :%s", port)
		log.Fatal(http.ListenAndServe(":"+port, mux))
	}()

	<-ctx.Done()
	log.Println("shutting down: stopping SSE bus")
	bus.Wait()
}

func routes(h *handler.Handler, auth *middleware.Auth) http.Handler {
	mux := http.NewServeMux()

	// 死活監視（認証不要。proxy 経由では /api/healthz として見える）
	mux.HandleFunc("GET /healthz", h.Healthz)

	// 認証
	mux.HandleFunc("POST /register", h.Register)
	mux.HandleFunc("POST /login", h.Login)
	mux.Handle("POST /logout", auth.Required(http.HandlerFunc(h.Logout)))

	// 自分のプロフィール
	mux.Handle("GET /me", auth.Required(http.HandlerFunc(h.GetMe)))

	// 足跡
	mux.Handle("GET /me/footprints", auth.Required(http.HandlerFunc(h.GetFootprints)))

	// ユーザー
	mux.Handle("GET /profile/{user_id}", auth.Optional(http.HandlerFunc(h.GetProfile)))
	mux.Handle("PUT /profile", auth.Required(http.HandlerFunc(h.UpdateProfile)))
	mux.Handle("GET /users/{user_id}/followers", auth.Optional(http.HandlerFunc(h.GetFollowers)))
	mux.Handle("GET /users/{user_id}/following", auth.Optional(http.HandlerFunc(h.GetFollowing)))
	mux.Handle("POST /users/{user_id}/follow", auth.Required(http.HandlerFunc(h.Follow)))
	mux.Handle("DELETE /users/{user_id}/follow", auth.Required(http.HandlerFunc(h.Unfollow)))

	// 投稿
	mux.Handle("GET /posts", auth.Required(http.HandlerFunc(h.GetTimeline)))
	mux.Handle("POST /posts", auth.Required(http.HandlerFunc(h.CreatePost)))
	mux.Handle("GET /posts/{id}", auth.Optional(http.HandlerFunc(h.GetPost)))
	mux.Handle("GET /users/{user_id}/posts", auth.Optional(http.HandlerFunc(h.GetUserPosts)))
	mux.Handle("DELETE /posts/{id}", auth.Required(http.HandlerFunc(h.DeletePost)))

	// 返信（スレッド）
	mux.Handle("POST /replies", auth.Required(http.HandlerFunc(h.CreateReply)))
	mux.Handle("GET /posts/{id}/thread", auth.Optional(http.HandlerFunc(h.GetThread)))
	mux.Handle("GET /posts/{id}/thread/stream", auth.Optional(http.HandlerFunc(h.ThreadStream)))

	// いいね
	mux.HandleFunc("GET /posts/{id}/likes", h.GetLikes)
	mux.Handle("POST /likes", auth.Required(http.HandlerFunc(h.Like)))
	mux.Handle("DELETE /likes/{post_id}", auth.Required(http.HandlerFunc(h.Unlike)))

	// リポスト
	mux.Handle("POST /reposts", auth.Required(http.HandlerFunc(h.Repost)))
	mux.Handle("DELETE /reposts/{post_id}", auth.Required(http.HandlerFunc(h.UnRepost)))

	// 検索
	mux.HandleFunc("GET /search", h.Search)

	// 通知
	mux.Handle("GET /notifications", auth.Required(http.HandlerFunc(h.GetNotifications)))
	mux.Handle("POST /notifications/read", auth.Required(http.HandlerFunc(h.MarkNotificationsRead)))
	mux.Handle("GET /notifications/unread_count", auth.Required(http.HandlerFunc(h.GetUnreadCount)))
	mux.Handle("GET /notifications/stream", auth.Required(http.HandlerFunc(h.NotificationStream)))

	// トレンド
	mux.Handle("GET /trending", auth.Optional(http.HandlerFunc(h.GetTrending)))

	return mux
}

func corsMiddleware(next http.Handler) http.Handler {
	allowedOrigin := os.Getenv("ALLOWED_ORIGIN")
	if allowedOrigin == "" {
		allowedOrigin = "http://localhost:3000"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
