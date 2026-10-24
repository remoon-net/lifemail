package api

import (
	"errors"
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

	body := `{"domains":[{"name":"Example.com"}," example.org "],"catchall":{"example.com":" Archive@Example.net "},"smtp":{"host":"smtp.internal","port":587,"username":"u","password":"p","from":"noreply@example.com"}}`
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
	if !strings.Contains(rec.Body.String(), `"catchall":{"example.com":"archive@example.net"}`) {
		t.Fatalf("catch-all missing from response: %s", rec.Body.String())
	}

	cfg := runtime.Snapshot()
	if len(cfg.Domains) != 2 || cfg.Domains[0].Name != "example.com" || cfg.Domains[1].Name != "example.org" {
		t.Fatalf("domains = %#v", cfg.Domains)
	}
	if dest, ok := cfg.CatchAllFor("example.com"); !ok || dest != "archive@example.net" {
		t.Fatalf("catch-all = %q %v", dest, ok)
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

func TestAdminSMTPTest(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	runtime, err := settings.New(db)
	if err != nil {
		t.Fatal(err)
	}
	sender := &fakeSender{}
	h := &Admin{
		Settings: runtime,
		Token:    "secret",
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Sender:   sender,
	}

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/smtp/test", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer secret")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// Nothing saved yet: the endpoint points at the missing configuration
	// instead of dialling a placeholder relay.
	if rec := post(`{"to":"box@example.net"}`); rec.Code != http.StatusConflict {
		t.Fatalf("unconfigured status = %d: %s", rec.Code, rec.Body.String())
	}

	host, port, pass, from := "smtp.internal", 465, "token", "noreply@example.com"
	domains := []settings.Domain{{Name: "example.com"}}
	if _, err := runtime.Update(settings.Patch{
		Domains:      &domains,
		SMTPHost:     &host,
		SMTPPort:     &port,
		SMTPPassword: &pass,
		SMTPFrom:     &from,
	}); err != nil {
		t.Fatal(err)
	}

	if rec := post(`{"to":"not-an-address"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid recipient status = %d: %s", rec.Code, rec.Body.String())
	}

	rec := post(`{"to":"Box@Example.net"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("test send status = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"to":"box@example.net"`) {
		t.Fatalf("response = %s", rec.Body.String())
	}
	if sender.calls != 1 {
		t.Fatalf("send calls = %d, want 1", sender.calls)
	}
	if sender.from != "noreply@example.com" {
		t.Errorf("envelope from = %q", sender.from)
	}
	if len(sender.to) != 1 || sender.to[0] != "box@example.net" {
		t.Errorf("recipients = %v", sender.to)
	}
	if !strings.Contains(string(sender.raw), "Subject: lifemail SMTP test") {
		t.Errorf("unexpected test message: %s", sender.raw)
	}

	// Relay rejects: the console must see the failure, not a silent success.
	sender.err = errors.New("relay down")
	if rec := post(`{"to":"box@example.net"}`); rec.Code != http.StatusBadGateway {
		t.Fatalf("relay failure status = %d: %s", rec.Code, rec.Body.String())
	}

	// The endpoint requires the admin token like the rest of /api/admin.
	req := httptest.NewRequest(http.MethodPost, "/api/admin/smtp/test",
		strings.NewReader(`{"to":"box@example.net"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", rec.Code)
	}
}
