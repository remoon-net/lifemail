package store

import (
	"os"
	"path/filepath"
	"testing"

	"remoon.net/lifemail/internal/blobstore"
)

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, dir
}

func TestAttachmentPayloadStoredOutsideDatabase(t *testing.T) {
	db, dir := openTestStore(t)
	blobs, err := blobstore.Open(filepath.Join(dir, "attachments"))
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
	msgID, err := db.InsertMessage(&Message{UserID: user.ID, FolderID: inbox.ID, Subject: "with attachment"})
	if err != nil {
		t.Fatal(err)
	}

	payload := []byte("file body")
	att := &Attachment{
		MessageID:   msgID,
		FileName:    "notes.txt",
		ContentType: "text/plain",
		Data:        payload,
	}
	if _, err := db.InsertAttachment(att); err != nil {
		t.Fatal(err)
	}
	if att.Path == "" {
		t.Fatal("attachment has no blob path")
	}

	if _, err := os.Stat(blobs.Path(att.Path)); err != nil {
		t.Fatalf("blob file missing: %v", err)
	}

	full, err := db.AttachmentByID(att.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(full.Data) != string(payload) {
		t.Fatalf("payload = %q", full.Data)
	}
	if full.Size != int64(len(payload)) {
		t.Fatalf("size = %d", full.Size)
	}

	// Deleting the account also removes the files.
	if err := db.DeleteUser(user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(blobs.Path(att.Path)); !os.IsNotExist(err) {
		t.Fatalf("blob survived account deletion: %v", err)
	}
}

func TestAttachmentRequiresBlobDirectory(t *testing.T) {
	db, _ := openTestStore(t)

	att := &Attachment{MessageID: 1, FileName: "a.txt", Data: []byte("payload")}
	if _, err := db.InsertAttachment(att); err == nil {
		t.Fatal("expected an error without a blob directory")
	}
}
