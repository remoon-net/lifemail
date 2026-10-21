package store

import (
	"path/filepath"
	"testing"
)

func TestOpenInfersDialectFromDSN(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "infer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if db.Dialect() != "sqlite" {
		t.Fatalf("dialect = %q, want sqlite", db.Dialect())
	}
}

func TestDialectForDSN(t *testing.T) {
	cases := map[string]bool{
		"postgres://u:p@localhost/db":   true,
		"POSTGRESQL://u:p@localhost/db": true,
		"lifemail.db":                   false,
		"/data/lifemail.db":             false,
		"file:/data/lifemail.db":        false,
		"":                              false,
	}
	for dsn, want := range cases {
		got := dialectFor(dsn) == "postgres"
		if got != want {
			t.Fatalf("dialectFor(%q) is postgres = %v, want %v", dsn, got, want)
		}
	}
}
