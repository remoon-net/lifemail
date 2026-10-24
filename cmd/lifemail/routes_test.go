package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
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
		Calendar:     stub("calendar"),
		Metrics:      stub("metrics"),
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
		{http.MethodPost, "/api/inbound", "inbound", http.StatusOK},
		{http.MethodGet, "/Microsoft-Server-ActiveSync", "eas", http.StatusOK},
		{http.MethodGet, "/microsoft-server-activesync", "eas", http.StatusOK},
		{http.MethodPost, "/Autodiscover/Autodiscover.xml", "autodiscover", http.StatusOK},
		{http.MethodGet, "/autodiscover/autodiscover.xml", "autodiscover", http.StatusOK},
		{http.MethodGet, "/calendar/abc.ics", "calendar", http.StatusOK},
		{http.MethodGet, "/api/admin/metrics", "metrics", http.StatusOK},
		{http.MethodGet, "/healthz", "ok\n", http.StatusOK},
		{http.MethodGet, "/nope", "", http.StatusNotFound},
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

// The console lives under /admin/ and is served from the embedded build.
func TestHandlerServesConsole(t *testing.T) {
	if _, err := os.Stat("../../web/build/index.html"); err != nil {
		t.Skip("web/build is not present; run make web")
	}
	rec := httptest.NewRecorder()
	newHTTPHandler(stubRouteDeps()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/: status %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<!doctype html>") {
		t.Fatalf("GET /admin/: unexpected body %q", rec.Body.String())
	}
}
