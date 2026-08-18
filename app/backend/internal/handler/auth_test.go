package handler

import (
	"net/http"
	"strings"
	"testing"
)

// TestRegisterDuplicateReturnsConflict は本物の重複登録だけが 409 になることを確かめる。
func TestRegisterDuplicateReturnsConflict(t *testing.T) {
	f := newFixture(t)
	u := f.registerUser("dup")

	var username, email string
	if err := f.db.QueryRow(`SELECT username, email FROM users WHERE id = ?`, u.id).
		Scan(&username, &email); err != nil {
		t.Fatalf("登録済みユーザーの取得に失敗: %v", err)
	}
	status, body, _ := f.do(nil, http.MethodPost, "/register", map[string]string{
		"username": username, "email": email, "password": "regression-test-password",
	})
	if status != http.StatusConflict {
		t.Fatalf("重複登録 = %d, want 409 (body=%s)", status, body)
	}
}

// TestRegisterPasswordTooLongIsClientError は bcrypt の 72 バイト上限超過が 500 でなく 4xx になることを確かめる。
func TestRegisterPasswordTooLongIsClientError(t *testing.T) {
	f := newFixture(t)

	status, body, _ := f.do(nil, http.MethodPost, "/register", map[string]string{
		"username": "toolong_" + t.Name(), "email": "toolong@example.test",
		"password": strings.Repeat("あ", 25), // 75 バイト
	})
	if status < 400 || status >= 500 {
		t.Fatalf("72 バイト超のパスワード = %d, want 4xx (body=%s)", status, body)
	}
}
