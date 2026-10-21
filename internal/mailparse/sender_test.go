package mailparse

import "testing"

// Sender names the mailbox that actually submitted the message.
func TestParseSenderHeader(t *testing.T) {
	raw := []byte("From: Boss <boss@example.com>\r\n" +
		"Sender: Assistant <assistant@example.com>\r\n" +
		"To: alice@example.com\r\n" +
		"Subject: delegated\r\n" +
		"\r\n" +
		"body\r\n")
	msg, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if msg.FromAddr != "boss@example.com" {
		t.Fatalf("FromAddr = %q", msg.FromAddr)
	}
	if msg.SenderAddr != "assistant@example.com" {
		t.Fatalf("SenderAddr = %q", msg.SenderAddr)
	}
	if msg.SenderName != "Assistant" {
		t.Fatalf("SenderName = %q", msg.SenderName)
	}
}

// Without a Sender header both fields stay empty.
func TestParseNoSenderHeader(t *testing.T) {
	raw := []byte("From: alice@example.com\r\nSubject: x\r\n\r\nbody\r\n")
	msg, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if msg.SenderAddr != "" || msg.SenderName != "" {
		t.Fatalf("unexpected sender %q %q", msg.SenderName, msg.SenderAddr)
	}
}
