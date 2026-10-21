package eas

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"remoon.net/lifemail/internal/wbxml"
)

func loginAs(t *testing.T, srv *Server, username string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := wbxml.Encode(wbxml.N(pageFolderHierarchy, "FolderSync",
		wbxml.T(pageFolderHierarchy, "SyncKey", "0")), codec)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost,
		"/Microsoft-Server-ActiveSync?Cmd=FolderSync&DeviceId=dev1", bytes.NewReader(body))
	req.SetBasicAuth(username, "secret")
	req.Header.Set("MS-ASProtocolVersion", "14.1")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestEASLoginAcceptsBareUsername(t *testing.T) {
	srv, _, _ := testServer(t)

	// MIUI sends just "alice" when the address is configured without a domain.
	rec := loginAs(t, srv, "alice")
	if rec.Code != http.StatusOK {
		t.Fatalf("bare username status = %d: %s", rec.Code, rec.Body.String())
	}

	// Wrong passwords are still rejected.
	req := httptest.NewRequest(http.MethodPost,
		"/Microsoft-Server-ActiveSync?Cmd=FolderSync&DeviceId=dev1", bytes.NewReader(nil))
	req.SetBasicAuth("alice", "wrong")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d", rec.Code)
	}
}

func TestEASLoginAcceptsDomainPrefix(t *testing.T) {
	srv, _, _ := testServer(t)
	if rec := loginAs(t, srv, `EXAMPLE\alice`); rec.Code != http.StatusOK {
		t.Fatalf("domain\\user status = %d: %s", rec.Code, rec.Body.String())
	}
}
