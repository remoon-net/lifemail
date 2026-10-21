package blobstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPutGetRemove(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	payload := []byte("attachment body")
	key := Key("att", 42, "Report.PDF", payload)
	if !strings.HasPrefix(key, "att/2a/42-") || !strings.HasSuffix(key, ".pdf") {
		t.Fatalf("unexpected key %q", key)
	}
	if err := s.Put(key, payload); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload = %q", got)
	}

	info, err := os.Stat(s.Path(key))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}

	if err := s.Remove(key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(key); !os.IsNotExist(err) {
		t.Fatalf("file still present: %v", err)
	}
	// Removing a missing blob is not an error.
	if err := s.Remove(key); err != nil {
		t.Fatal(err)
	}
}

func TestKeyShardsAndFallsBackToBin(t *testing.T) {
	key := Key("raw", 256, "noext", []byte("x"))
	if !strings.HasPrefix(key, "raw/00/256-") {
		t.Fatalf("shard prefix = %q", key)
	}
	if !strings.HasSuffix(key, ".bin") {
		t.Fatalf("expected .bin suffix, got %q", key)
	}
	// Same content and message produce the same key, so re-delivery overwrites.
	a := Key("att", 7, "a.txt", []byte("same"))
	b := Key("att", 7, "a.txt", []byte("same"))
	if a != b {
		t.Fatalf("keys differ: %q vs %q", a, b)
	}
	if filepath.Dir(a) != "att/07" {
		t.Fatalf("shard dir = %q", filepath.Dir(a))
	}
}

func TestOpenRequiresDirectory(t *testing.T) {
	if _, err := Open("  "); err == nil {
		t.Fatal("empty directory must fail")
	}
}
