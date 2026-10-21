package eas

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"remoon.net/lifemail/internal/wbxml"
)

// pingStatus performs a Ping through ServeHTTP without failing from the calling
// goroutine, so it is safe to use inside a test goroutine.
func pingStatus(srv *Server, email string, doc *wbxml.Node) int {
	raw, err := wbxml.Encode(doc, codec)
	if err != nil {
		return -1
	}
	req := httptest.NewRequest(http.MethodPost,
		"/Microsoft-Server-ActiveSync?Cmd=Ping&DeviceId=dev1", bytes.NewReader(raw))
	req.SetBasicAuth(email, "secret")
	req.Header.Set("MS-ASProtocolVersion", "14.1")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	resp, err := wbxml.Decode(rec.Body.Bytes(), codec)
	if err != nil {
		return -1
	}
	return atoiDefault(resp.ChildText("Status"), -1)
}

func TestNewPingCancelsPreviousOne(t *testing.T) {
	srv, _, inbox := testServer(t)

	// The first long poll would otherwise be held for its whole heartbeat.
	first := make(chan int, 1)
	go func() {
		first <- pingStatus(srv, "alice@example.com", pingDocument("60", inbox.ID))
	}()
	time.Sleep(150 * time.Millisecond)

	// A second poll for the same account supersedes the first one.
	second := make(chan int, 1)
	go func() {
		second <- pingStatus(srv, "alice@example.com", pingDocument("1", inbox.ID))
	}()

	select {
	case status := <-first:
		if status != 1 {
			t.Fatalf("cancelled ping status = %d, want 1", status)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("previous ping was not cancelled by the new one")
	}
	<-second
}

func TestPingReleasesDeviceSlot(t *testing.T) {
	srv, _, inbox := testServer(t)
	if status := pingStatus(srv, "alice@example.com", pingDocument("1", inbox.ID)); status != 1 {
		t.Fatalf("status = %d, want 1", status)
	}

	srv.pingMu.Lock()
	pending := len(srv.pingWait)
	srv.pingMu.Unlock()
	if pending != 0 {
		t.Fatalf("ping registry left %d entries", pending)
	}
}

func TestTwoAccountsOnOneDeviceKeepSeparatePolls(t *testing.T) {
	srv, user, inbox := testServer(t)
	other, err := srv.Store.CreateUser("bob", "bob@example.com", "Bob", user.PasswordHash)
	if err != nil {
		t.Fatal(err)
	}
	otherInbox, err := srv.Store.FolderByType(other.ID, 2)
	if err != nil {
		t.Fatal(err)
	}

	// First account holds a long poll.
	first := make(chan int, 1)
	go func() {
		first <- pingStatus(srv, "alice@example.com", pingDocument("60", inbox.ID))
	}()
	time.Sleep(150 * time.Millisecond)

	// A poll for the second account on the same device must not cancel it.
	second := make(chan int, 1)
	go func() {
		second <- pingStatus(srv, "bob@example.com", pingDocument("1", otherInbox.ID))
	}()
	<-second

	select {
	case status := <-first:
		t.Fatalf("first account's ping was cancelled by the second account (status %d)", status)
	case <-time.After(400 * time.Millisecond):
	}
}
