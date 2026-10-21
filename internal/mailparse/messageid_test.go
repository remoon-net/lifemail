package mailparse

import (
	"strings"
	"testing"
)

func TestEnsureMessageIDAddsHeader(t *testing.T) {
	raw := []byte("From: a@example.com\r\nTo: b@example.com\r\nSubject: hi\r\n\r\nbody\r\n")
	out, id := EnsureMessageID(raw, "example.com")
	if id == "" || !strings.HasPrefix(id, "<") || !strings.HasSuffix(id, ">") {
		t.Fatalf("id = %q", id)
	}
	if !strings.Contains(string(out), "Message-ID: "+id+"\r\n") {
		t.Fatalf("header missing: %s", out)
	}
	if !strings.Contains(string(out), "\r\n\r\nbody") {
		t.Fatalf("body separator damaged: %q", out)
	}
}

func TestEnsureMessageIDKeepsExisting(t *testing.T) {
	raw := []byte("From: a@example.com\nMessage-Id: <keep@example.com>\n\nbody")
	out, id := EnsureMessageID(raw, "example.com")
	if id != "<keep@example.com>" {
		t.Fatalf("id = %q", id)
	}
	if string(out) != string(raw) {
		t.Fatalf("raw modified: %q", out)
	}
}

func TestEnsureMessageIDNoBody(t *testing.T) {
	raw := []byte("From: a@example.com\r\nSubject: hi\r\n")
	out, id := EnsureMessageID(raw, "example.com")
	if !strings.Contains(string(out), "Message-ID: "+id) {
		t.Fatalf("header missing: %q", out)
	}
}
