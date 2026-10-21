package submission

import (
	"errors"
	"path/filepath"
	"testing"

	"remoon.net/lifemail/internal/blobstore"
	"remoon.net/lifemail/internal/deliver"
	"remoon.net/lifemail/internal/mailparse"
	"remoon.net/lifemail/internal/store"
)

const localMessage = "From: Alice <alice@example.com>\r\n" +
	"To: Bob <bob@example.com>\r\n" +
	"Cc: Outside <outside@example.org>\r\n" +
	"Subject: local routing\r\n" +
	"Message-Id: <local-1@example.com>\r\n" +
	"Date: Mon, 20 Oct 2026 10:00:00 +0000\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"hello\r\n"

func TestDeliverLocalSplitsInternalAndExternalRecipients(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "submission.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	blobs, blobErr := blobstore.Open(filepath.Join(t.TempDir(), "blobs"))
	if blobErr != nil {
		t.Fatal(blobErr)
	}
	db.SetBlobs(blobs)

	if _, err := db.CreateUser("alice", "alice@example.com", "Alice", "hash"); err != nil {
		t.Fatal(err)
	}
	bob, err := db.CreateUser("bob", "bob@example.com", "Bob", "hash")
	if err != nil {
		t.Fatal(err)
	}

	srv := &Server{Store: db}
	raw := []byte(localMessage)
	parsed, err := mailparse.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}

	external, err := srv.deliverLocal(parsed, raw, []string{"bob@example.com", "outside@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	if len(external) != 1 || external[0] != "outside@example.org" {
		t.Fatalf("external = %v, want [outside@example.org]", external)
	}

	inbox, err := db.FolderByType(bob.ID, deliver.FolderInbox)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := db.MessagesByFolder(inbox.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Subject != "local routing" {
		t.Fatalf("inbox messages = %+v", msgs)
	}

	// Retrying the same Message-ID must not duplicate the local copy.
	if _, err := srv.deliverLocal(parsed, raw, []string{"bob@example.com"}); err != nil {
		t.Fatal(err)
	}
	msgs, _ = db.MessagesByFolder(inbox.ID, 0)
	if len(msgs) != 1 {
		t.Fatalf("duplicate delivered, inbox has %d messages", len(msgs))
	}
}

func TestDeliverLocalRejectsUnknownLocalRecipient(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "unknown.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	blobs, blobErr := blobstore.Open(filepath.Join(t.TempDir(), "blobs"))
	if blobErr != nil {
		t.Fatal(blobErr)
	}
	db.SetBlobs(blobs)

	raw := []byte(localMessage)
	parsed, err := mailparse.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{Store: db, IsLocalDomain: func(domain string) bool {
		return domain == "example.com"
	}}
	if _, err := srv.deliverLocal(parsed, raw, []string{"nobody@example.com"}); !errors.Is(err, errUnknownLocalRecipient) {
		t.Fatalf("err = %v, want unknown local recipient", err)
	}
}
