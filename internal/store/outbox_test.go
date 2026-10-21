package store_test

import (
	"path/filepath"
	"testing"

	"remoon.net/lifemail/internal/blobstore"
	"remoon.net/lifemail/internal/store"
)

func TestOutboundQueue(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "outbox.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	blobs, err := blobstore.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetBlobs(blobs)

	id, err := db.EnqueueOutbound("alice@example.com", []string{"a@x.test", "b@y.test"}, []byte("raw mime"))
	if err != nil {
		t.Fatal(err)
	}

	items, err := db.DueOutbound(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != id {
		t.Fatalf("due = %+v", items)
	}
	if len(items[0].Recipients) != 2 || items[0].Recipients[1] != "b@y.test" {
		t.Fatalf("recipients = %v", items[0].Recipients)
	}
	if string(items[0].Raw) != "raw mime" {
		t.Fatalf("raw = %q", items[0].Raw)
	}

	if err := db.RetryOutbound(id, 1, 1000, "smtp down", false); err != nil {
		t.Fatal(err)
	}
	if items, _ := db.DueOutbound(0, 10); len(items) != 0 {
		t.Fatalf("retry time not respected: %+v", items)
	}
	items, _ = db.DueOutbound(1001, 10)
	if len(items) != 1 || items[0].Attempts != 1 || items[0].LastError != "smtp down" {
		t.Fatalf("after retry = %+v", items)
	}

	if err := db.RetryOutbound(id, 20, 0, "gave up", true); err != nil {
		t.Fatal(err)
	}
	if items, _ := db.DueOutbound(1<<62, 10); len(items) != 0 {
		t.Fatalf("failed message still queued: %+v", items)
	}
	if n, err := db.CountOutbound(true); err != nil || n != 1 {
		t.Fatalf("failed count = %d, %v", n, err)
	}

	if err := db.DeleteOutbound(id); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.CountOutbound(true); n != 0 {
		t.Fatalf("row not deleted")
	}
}
