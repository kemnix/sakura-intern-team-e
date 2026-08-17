package middleware

import (
	"context"
	"database/sql"
	"net/http"
)

type contextKey string

const UserIDKey contextKey = "userID"

type Auth struct {
	DB *sql.DB
}

func (a *Auth) Required(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session_id")
		if err != nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		var userID int64
		err = a.DB.QueryRowContext(r.Context(),
			`SELECT user_id FROM sessions WHERE id = ? AND expires_at > NOW()`,
			cookie.Value,
		).Scan(&userID)
		if err != nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), UserIDKey, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *Auth) Optional(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session_id")
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}

		var userID int64
		err = a.DB.QueryRowContext(r.Context(),
			`SELECT user_id FROM sessions WHERE id = ? AND expires_at > NOW()`,
			cookie.Value,
		).Scan(&userID)
		if err == nil {
			ctx := context.WithValue(r.Context(), UserIDKey, userID)
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}
