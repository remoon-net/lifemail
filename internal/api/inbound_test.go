package api

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"remoon.net/lifemail/internal/blobstore"
	"remoon.net/lifemail/internal/store"
)

const testMessage = `From: Bob <bob@example.com>
To: Alice <alice@example.com>
Subject: Test =?utf-8?q?h=C3=A9llo?=
Message-Id: <abc123@example.com>
Date: Mon, 20 Oct 2026 10:00:00 +0000
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="b1"

--b1
Content-Type: text/plain; charset=utf-8

body text
--b1
Content-Type: text/plain; name="a.txt"
Content-Disposition: attachment; filename="a.txt"
Content-Transfer-Encoding: base64

aGVsbG8=
--b1--
`

func TestInboundDelivers(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	blobs, blobErr := blobstore.Open(filepath.Join(t.TempDir(), "blobs"))
	if blobErr != nil {
		t.Fatal(blobErr)
	}
	db.SetBlobs(blobs)

	hash, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	user, err := db.CreateUser("alice", "alice@example.com", "Alice", string(hash))
	if err != nil {
		t.Fatal(err)
	}

	h := &Inbound{
		Store: db,
		Token: "token",
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/inbound", bytes.NewReader([]byte(testMessage)))
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-Envelope-To", "alice@example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	inbox, err := db.FolderByType(user.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := db.MessagesByFolder(inbox.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	m := msgs[0]
	if m.Subject != "Test héllo" {
		t.Errorf("subject = %q", m.Subject)
	}
	if m.BodyText != "body text\n" && m.BodyText != "body text" {
		t.Errorf("body = %q", m.BodyText)
	}
	if m.FromAddr != "bob@example.com" || m.FromName != "Bob" {
		t.Errorf("from = %q <%q>", m.FromName, m.FromAddr)
	}
	atts, err := db.AttachmentsByMessage(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 1 || atts[0].FileName != "a.txt" {
		t.Fatalf("attachments = %+v", atts)
	}
	full, err := db.AttachmentByID(atts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(full.Data) != "hello" {
		t.Errorf("attachment data = %q", full.Data)
	}

	// Cloudflare may retry; duplicates must be dropped.
	req = httptest.NewRequest(http.MethodPost, "/api/inbound", bytes.NewReader([]byte(testMessage)))
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-Envelope-To", "alice@example.com")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	msgs, _ = db.MessagesByFolder(inbox.ID, 0)
	if len(msgs) != 1 {
		t.Fatalf("duplicate stored, got %d messages", len(msgs))
	}
}

func TestInboundDeliversToAliasAndPlusAddress(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "alias.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	blobs, blobErr := blobstore.Open(filepath.Join(t.TempDir(), "blobs"))
	if blobErr != nil {
		t.Fatal(blobErr)
	}
	db.SetBlobs(blobs)

	hash, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	user, err := db.CreateUser("alice", "alice@example.com", "Alice", string(hash))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAlias("info@example.com", user.ID); err != nil {
		t.Fatal(err)
	}

	h := &Inbound{
		Store: db,
		Token: "token",
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	inbox, err := db.FolderByType(user.ID, 2)
	if err != nil {
		t.Fatal(err)
	}

	deliver := func(envelopeTo string, seq int) int {
		msg := strings.Replace(testMessage, "<abc123@example.com>",
			fmt.Sprintf("<alias%d@example.com>", seq), 1)
		req := httptest.NewRequest(http.MethodPost, "/api/inbound", bytes.NewReader([]byte(msg)))
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("X-Envelope-To", envelopeTo)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"delivered"`) ||
			strings.Contains(rec.Body.String(), `"skipped"`) {
			t.Fatalf("envelope %s not delivered: %s", envelopeTo, rec.Body.String())
		}
		return rec.Code
	}

	deliver("info@example.com", 1)       // alias
	deliver("alice+news@example.com", 2) // plus addressing

	msgs, err := db.MessagesByFolder(inbox.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2", len(msgs))
	}
}
