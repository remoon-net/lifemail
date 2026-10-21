package store

import (
	"os"
	"path/filepath"
	"testing"

	"remoon.net/lifemail/internal/blobstore"
)

func TestMessageRawStoredOutsideDatabase(t *testing.T) {
	db, dir := openTestStore(t)
	blobs, err := blobstore.Open(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetBlobs(blobs)

	user, err := db.CreateUser("alice", "alice@example.com", "Alice", "hash")
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := db.FolderByType(user.ID, 2)
	if err != nil {
		t.Fatal(err)
	}

	raw := []byte("From: bob@example.org\r\nSubject: invoice march\r\n\r\nplease pay the invoice\r\n")
	msg := &Message{
		UserID:   user.ID,
		FolderID: inbox.ID,
		Subject:  "invoice march",
		FromAddr: "bob@example.org",
		ToAddr:   "alice@example.com",
		BodyText: "please pay the invoice",
		Raw:      raw,
	}
	if _, err := db.InsertMessage(msg); err != nil {
		t.Fatal(err)
	}
	if msg.RawPath == "" {
		t.Fatal("message has no raw path")
	}

	if _, err := os.Stat(blobs.Path(msg.RawPath)); err != nil {
		t.Fatalf("raw file missing: %v", err)
	}

	// Reading a single message brings the payload back.
	loaded, err := db.MessageByID(msg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(loaded.Raw) != string(raw) {
		t.Fatalf("raw = %q", loaded.Raw)
	}

	// Searchable fields stay in the database.
	found, total, err := db.SearchMessages(user.ID, "invoice", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(found) != 1 || found[0].ID != msg.ID {
		t.Fatalf("search = %d results (total %d)", len(found), total)
	}

	// Updating flags keeps the payload reference intact.
	loaded.IsRead = true
	if err := db.UpdateMessage(loaded); err != nil {
		t.Fatal(err)
	}
	reloaded, err := db.MessageByID(msg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.IsRead || string(reloaded.Raw) != string(raw) {
		t.Fatalf("update lost data: read=%v raw=%d bytes", reloaded.IsRead, len(reloaded.Raw))
	}

	// Deleting the account removes the file too.
	if err := db.DeleteUser(user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(blobs.Path(msg.RawPath)); !os.IsNotExist(err) {
		t.Fatalf("raw file survived account deletion: %v", err)
	}
}

func TestMessageRequiresBlobDirectory(t *testing.T) {
	db, _ := openTestStore(t)
	msg := &Message{UserID: "u1", FolderID: 1, Subject: "inline", Raw: []byte("Subject: x\r\n\r\nbody")}
	if _, err := db.InsertMessage(msg); err == nil {
		t.Fatal("expected an error without a blob directory")
	}
}
