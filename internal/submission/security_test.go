package submission

import (
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"remoon.net/lifemail/internal/authlimit"
	"remoon.net/lifemail/internal/store"
)

func TestCheckCredentialsLocksAfterRepeatedFailures(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "submission.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateUser("alice", "alice@example.com", "Alice", string(hash)); err != nil {
		t.Fatal(err)
	}

	srv := &Server{
		Store:       db,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuthLimiter: authlimit.New(2, time.Minute, time.Minute),
	}
	for i := 0; i < 2; i++ {
		if _, err := srv.checkCredentials("alice@example.com", "wrong", "203.0.113.9:1234"); err == nil {
			t.Fatal("wrong password accepted")
		}
	}
	if _, err := srv.checkCredentials("alice@example.com", "secret", "203.0.113.9:1234"); !errors.Is(err, errAuthLocked) {
		t.Fatalf("err = %v, want errAuthLocked", err)
	}

	// Another client address is unaffected by the lock.
	if _, err := srv.checkCredentials("alice@example.com", "secret", "198.51.100.7:1234"); err != nil {
		t.Fatalf("second client rejected: %v", err)
	}
}
