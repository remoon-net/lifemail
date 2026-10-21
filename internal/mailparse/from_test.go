package mailparse

import (
	"strings"
	"testing"
)

func TestEnsureFromReplacesEmptyHeader(t *testing.T) {
	raw := []byte("Date: Tue, 21 Oct 2026 05:05:07 UTC\r\nSubject: invite\r\nFrom: \r\nTo: b@example.com\r\n\r\nbody\r\n")
	out := string(EnsureFrom(raw, "Alice", "alice@example.com"))
	if !strings.Contains(out, `From: "Alice" <alice@example.com>`) {
		t.Fatalf("From not filled in:\n%s", out)
	}
	if strings.Count(out, "From:") != 1 {
		t.Fatalf("duplicate From headers:\n%s", out)
	}
	if !strings.HasSuffix(out, "\r\n\r\nbody\r\n") {
		t.Fatalf("body damaged:\n%s", out)
	}
}

func TestEnsureFromAddsMissingHeader(t *testing.T) {
	raw := []byte("Subject: x\r\n\r\nbody\r\n")
	out := string(EnsureFrom(raw, "", "alice@example.com"))
	if !strings.HasPrefix(out, "From: alice@example.com\r\n") {
		t.Fatalf("header not added:\n%s", out)
	}
}

// A real sender must never be overwritten.
func TestEnsureFromKeepsExistingSender(t *testing.T) {
	raw := []byte("From: bob@example.com\r\nSubject: x\r\n\r\nbody\r\n")
	out := EnsureFrom(raw, "Alice", "alice@example.com")
	if string(out) != string(raw) {
		t.Fatalf("existing From was rewritten:\n%s", out)
	}
}
