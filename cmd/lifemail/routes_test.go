package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	webui "remoon.net/lifemail/web"
)

func stubRouteDeps() routeDeps {
	stub := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, name)
		})
	}
	return routeDeps{
		EAS:          stub("eas"),
		Autodiscover: stub("autodiscover"),
		Inbound:      stub("inbound"),
		Admin:        stub("admin"),
		Me:           stub("me"),
		User:         stub("user"),
		Calendar:     stub("calendar"),
		Metrics:      stub("metrics"),
		Web:          http.NotFoundHandler(),
	}
}

func TestHandlerRoutesByPath(t *testing.T) {
	h := newHTTPHandler(stubRouteDeps())

	tests := []struct {
		method string
		path   string
		want   string
		status int
	}{
		{http.MethodGet, "/api/admin/session", "admin", http.StatusOK},
		{http.MethodGet, "/api/me/calendars/", "me", http.StatusOK},
		{http.MethodGet, "/api/user/session", "user", http.StatusOK},
		{http.MethodGet, "/api/user/mail/folders", "user", http.StatusOK},
		{http.MethodPost, "/api/inbound", "inbound", http.StatusOK},
		{http.MethodGet, "/Microsoft-Server-ActiveSync", "eas", http.StatusOK},
		{http.MethodGet, "/microsoft-server-activesync", "eas", http.StatusOK},
		{http.MethodPost, "/Autodiscover/Autodiscover.xml", "autodiscover", http.StatusOK},
		{http.MethodGet, "/autodiscover/autodiscover.xml", "autodiscover", http.StatusOK},
		{http.MethodGet, "/calendar/abc.ics", "calendar", http.StatusOK},
		{http.MethodGet, "/api/admin/metrics", "metrics", http.StatusOK},
		{http.MethodGet, "/healthz", "ok\n", http.StatusOK},
		{http.MethodGet, "/nope", "", http.StatusNotFound},
		{http.MethodGet, "/api/nope", "", http.StatusNotFound},
	}
	for _, tc := range tests {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.status {
			t.Errorf("%s %s: status %d, want %d", tc.method, tc.path, rec.Code, tc.status)
			continue
		}
		if tc.want != "" && !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s %s: body %q does not contain %q", tc.method, tc.path, rec.Body.String(), tc.want)
		}
	}
}

// Only /api/admin/* is the operator API; near misses must not fall through.
func TestHandlerDoesNotWidenAdminPrefix(t *testing.T) {
	h := newHTTPHandler(stubRouteDeps())

	for _, path := range []string{"/api/adminx/users", "/api/admins", "/adminx/", "/metrics"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", path, rec.Code)
		}
	}
}

// The site root sends visitors to the account UI.
func TestHandlerRedirectsRootToApp(t *testing.T) {
	rec := httptest.NewRecorder()
	newHTTPHandler(stubRouteDeps()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("GET /: status %d, want 302", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/app/" {
		t.Fatalf("GET /: Location %q, want /app/", got)
	}
}

// Both UI trees are prerendered directories in the build on disk.
func TestHandlerServesUI(t *testing.T) {
	if _, err := os.Stat("../../web/build/admin/index.html"); err != nil {
		t.Skip("web/build is not present; run make web")
	}
	ui, err := webui.DirHandler("../../web/build")
	if err != nil {
		t.Fatalf("web handler: %v", err)
	}
	deps := stubRouteDeps()
	deps.Web = ui
	h := newHTTPHandler(deps)
	for _, path := range []string{"/admin/", "/app/"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status %d, want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "<!doctype html>") {
			t.Fatalf("GET %s: unexpected body %q", path, rec.Body.String())
		}
	}
}

// A missing build directory degrades to 404 instead of failing startup.
func TestHandlerWithoutWebDir(t *testing.T) {
	if _, err := webui.DirHandler("../../web/does-not-exist"); err == nil {
		t.Fatal("DirHandler accepted a missing directory")
	}
	rec := httptest.NewRecorder()
	newHTTPHandler(stubRouteDeps()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /app/ without a build: status %d, want 404", rec.Code)
	}
}
