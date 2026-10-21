package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"remoon.net/lifemail/internal/authlimit"
)

func TestAdminLoginThrottled(t *testing.T) {
	h := &Admin{
		Token:       "secret",
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuthLimiter: authlimit.New(2, time.Minute, time.Minute),
	}

	attempt := func(token, remote string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/login",
			strings.NewReader(`{"token":"`+token+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	for i := 0; i < 2; i++ {
		if rec := attempt("nope", "203.0.113.9:1234"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d", i+1, rec.Code)
		}
	}
	rec := attempt("secret", "203.0.113.9:1234")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("locked status = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 must carry Retry-After")
	}

	if rec := attempt("secret", "198.51.100.7:1234"); rec.Code != http.StatusOK {
		t.Fatalf("other client status = %d, want 200", rec.Code)
	}
}
