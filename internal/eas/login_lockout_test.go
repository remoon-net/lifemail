package eas

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"remoon.net/lifemail/internal/authlimit"
)

func loginRequest(t *testing.T, srv *Server, password string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost,
		"/Microsoft-Server-ActiveSync?Cmd=FolderSync&DeviceId=dev1", bytes.NewReader(nil))
	req.SetBasicAuth("alice@example.com", password)
	req.Header.Set("MS-ASProtocolVersion", "14.1")
	req.RemoteAddr = "203.0.113.9:5555"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestEASLoginLocksAfterRepeatedFailures(t *testing.T) {
	srv, _, _ := testServer(t)
	srv.AuthLimiter = authlimit.New(3, time.Minute, time.Minute)

	for i := 0; i < 3; i++ {
		if rec := loginRequest(t, srv, "wrong"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d", i+1, rec.Code)
		}
	}

	rec := loginRequest(t, srv, "secret")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("locked login status = %d, want 401", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("locked response must carry Retry-After")
	}

	// A different client address keeps its own budget.
	req := httptest.NewRequest(http.MethodPost,
		"/Microsoft-Server-ActiveSync?Cmd=Options&DeviceId=dev1", bytes.NewReader(nil))
	req.SetBasicAuth("alice@example.com", "secret")
	req.RemoteAddr = "198.51.100.7:5555"
	reqRec := httptest.NewRecorder()
	srv.ServeHTTP(reqRec, req)
	if reqRec.Code != http.StatusOK {
		t.Fatalf("second client status = %d, want 200", reqRec.Code)
	}
}
