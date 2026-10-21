package eas

import (
	"testing"

	"remoon.net/lifemail/internal/store"
)

// A message sent on someone's behalf carries the delegate in email2:Sender and
// the mailbox owner in From.
func TestEmailAppDataSenderAndBcc(t *testing.T) {
	m := &store.Message{
		ID:            1,
		Subject:       "on behalf of",
		FromAddr:      "boss@example.com",
		FromName:      "Boss",
		SenderAddr:    "assistant@example.com",
		SenderName:    "Assistant",
		ReceivedAsBcc: true,
		Importance:    1,
		MessageClass:  "IPM.Note",
	}
	app := emailAppData(m, nil, bodyPref{Type: 1})

	if got := app.ChildText("Sender"); got != `"Assistant" <assistant@example.com>` {
		t.Fatalf("Sender = %q", got)
	}
	if got := app.ChildText("ReceivedAsBcc"); got != "1" {
		t.Fatalf("ReceivedAsBcc = %q, want 1", got)
	}
	if got := app.ChildText("From"); got != `"Boss" <boss@example.com>` {
		t.Fatalf("From = %q", got)
	}
}

// When Sender equals From the element is omitted, as the spec requires, and a
// normal delivery is not marked as Bcc.
func TestEmailAppDataOmitsRedundantSender(t *testing.T) {
	m := &store.Message{
		ID:           2,
		Subject:      "plain",
		FromAddr:     "alice@example.com",
		FromName:     "Alice",
		SenderAddr:   "alice@example.com",
		Importance:   1,
		MessageClass: "IPM.Note",
	}
	app := emailAppData(m, nil, bodyPref{Type: 1})
	if got := app.ChildText("Sender"); got != "" {
		t.Fatalf("Sender should be omitted, got %q", got)
	}
	if got := app.ChildText("ReceivedAsBcc"); got != "" {
		t.Fatalf("ReceivedAsBcc should be omitted, got %q", got)
	}
}
