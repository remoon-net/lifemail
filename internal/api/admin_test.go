package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"remoon.net/lifemail/internal/settings"
	"remoon.net/lifemail/internal/store"
)

func TestAdminLoginCookie(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	runtime, err := settings.New(db)
	if err != nil {
		t.Fatal(err)
	}
	h := &Admin{
		Settings: runtime,
		Token:    "secret",
		Version:  "v1.2.3",
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	// Wrong token is rejected.
	req := httptest.NewRequest(http.MethodPost, "/api/admin/login",
		strings.NewReader(`{"token":"nope"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d", rec.Code)
	}

	// Correct token issues an HttpOnly session cookie.
	req = httptest.NewRequest(http.MethodPost, "/api/admin/login",
		strings.NewReader(`{"token":"secret"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"version":"v1.2.3"`) {
		t.Fatalf("login response = %s", rec.Body.String())
	}
	var session *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == adminCookieName {
			session = cookie
		}
	}
	if session == nil || !session.HttpOnly {
		t.Fatalf("session cookie missing or not HttpOnly: %#v", session)
	}

	// The cookie authenticates both session and settings requests.
	req = httptest.NewRequest(http.MethodGet, "/api/admin/session", nil)
	req.AddCookie(session)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"authenticated":true`) {
		t.Fatalf("session response = %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil)
	req.AddCookie(session)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings with cookie status = %d", rec.Code)
	}

	// Logout expires the cookie.
	req = httptest.NewRequest(http.MethodPost, "/api/admin/logout", nil)
	req.AddCookie(session)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout status = %d", rec.Code)
	}
	cleared := false
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == adminCookieName && cookie.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("logout did not clear the session cookie")
	}
}

func TestAdminSettingsUpdate(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	runtime, err := settings.New(db)
	if err != nil {
		t.Fatal(err)
	}
	h := &Admin{
		Settings: runtime,
		Token:    "secret",
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	body := `{"domains":[{"name":"Example.com"}," example.org "],"smtp":{"host":"smtp.internal","port":587,"username":"u","password":"p","from":"noreply@example.com"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/admin/settings", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"password"`) {
		t.Fatalf("response leaks the SMTP password: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"password_set":true`) {
		t.Fatalf("password_set missing: %s", rec.Body.String())
	}

	cfg := runtime.Snapshot()
	if len(cfg.Domains) != 2 || cfg.Domains[0].Name != "example.com" || cfg.Domains[1].Name != "example.org" {
		t.Fatalf("domains = %#v", cfg.Domains)
	}
	if cfg.SMTPHost != "smtp.internal" || cfg.SMTPPort != 587 ||
		cfg.SMTPUser != "u" || cfg.SMTPPassword != "p" || cfg.SMTPFrom != "noreply@example.com" {
		t.Fatalf("smtp = %+v", cfg)
	}

	// Persisted settings must be visible to a fresh loader.
	reloaded, err := settings.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Snapshot(); got.SMTPHost != "smtp.internal" || got.Domains[0].Name != "example.com" {
		t.Fatalf("reloaded = %+v", got)
	}

	// Invalid values are rejected.
	req = httptest.NewRequest(http.MethodPut, "/api/admin/settings",
		strings.NewReader(`{"smtp":{"port":70000}}`))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid port status = %d", rec.Code)
	}

	// Missing token is rejected.
	req = httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", rec.Code)
	}
}
