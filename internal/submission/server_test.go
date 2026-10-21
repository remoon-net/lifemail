package submission

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"remoon.net/lifemail/internal/blobstore"
	"remoon.net/lifemail/internal/store"
)

func TestMaySendAsAllowsOwnAliases(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "submission.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	blobs, err := blobstore.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetBlobs(blobs)

	alice, err := db.CreateUser("alice", "alice@example.com", "Alice", "hash")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := db.CreateUser("bob", "bob@example.com", "Bob", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAlias("info@example.com", alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAlias("support@example.com", bob.ID); err != nil {
		t.Fatal(err)
	}

	s := &Server{Store: db, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	cases := []struct {
		sender string
		want   bool
	}{
		{"alice@example.com", true},
		{"ALICE@example.com", true},
		{"info@example.com", true},
		{"alice+tag@example.com", true},
		{"support@example.com", false},
		{"bob@example.com", false},
		{"nobody@example.com", false},
	}
	for _, tc := range cases {
		if got := s.maySendAs(alice, tc.sender); got != tc.want {
			t.Errorf("maySendAs(%q) = %v, want %v", tc.sender, got, tc.want)
		}
	}
}
