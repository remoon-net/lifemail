package eas

import (
	"encoding/base64"
	"testing"

	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/wbxml"
)

func partPrefRequest(partType, truncation int) bodyPref {
	opts := wbxml.N(pageItemOperations, "Options",
		wbxml.N(pageAirSyncBase, "BodyPartPreference",
			wbxml.T(pageAirSyncBase, "Type", itoa(partType)),
			wbxml.T(pageAirSyncBase, "Truncation", itoa(truncation))))
	return bodyPrefFrom(opts)
}

// A request carrying BodyPartPreference must be answered with a BodyPart
// element, not a Body element.
func TestBodyPartPreferenceProducesBodyPart(t *testing.T) {
	msg := &store.Message{
		ID:         1,
		Subject:    "x",
		BodyText:   "plain text body",
		BodyHTML:   "<p>html body</p>",
		Importance: 1,
	}
	pref := partPrefRequest(1, 0)
	if pref.PartType != 1 {
		t.Fatalf("PartType = %d, want 1", pref.PartType)
	}
	app := emailAppData(msg, nil, pref)

	part := app.Child("BodyPart")
	if part == nil {
		t.Fatalf("no BodyPart element")
	}
	if app.Child("Body") != nil {
		t.Fatalf("Body must not be sent when BodyPartPreference is used")
	}
	if got := part.ChildText("Status"); got != "1" {
		t.Fatalf("Status = %q, want 1", got)
	}
	if got := part.ChildText("Type"); got != "1" {
		t.Fatalf("Type = %q, want 1", got)
	}
	if got := part.ChildText("Data"); got != "plain text body" {
		t.Fatalf("Data = %q", got)
	}
	if part.Child("Truncated") != nil {
		t.Fatalf("unexpected Truncated")
	}
}

// HTML is selected by Type 2.
func TestBodyPartHTML(t *testing.T) {
	msg := &store.Message{ID: 2, BodyText: "text", BodyHTML: "<p>html</p>", Importance: 1}
	part := emailAppData(msg, nil, partPrefRequest(2, 0)).Child("BodyPart")
	if part == nil {
		t.Fatalf("no BodyPart")
	}
	if got := part.ChildText("Data"); got != "<p>html</p>" {
		t.Fatalf("Data = %q", got)
	}
}

// A truncated MIME part must be cut on the raw bytes and then encoded; cutting
// the base64 text would leave the client with an undecodable payload.
func TestBodyPartMIMEtruncatesRawBeforeEncoding(t *testing.T) {
	raw := []byte("Subject: x\r\n\r\n" + string(make([]byte, 200)))
	msg := &store.Message{ID: 3, Subject: "x", Raw: raw, Importance: 1}

	part := emailAppData(msg, nil, partPrefRequest(4, 40)).Child("BodyPart")
	if part == nil {
		t.Fatalf("no BodyPart")
	}
	if got := part.ChildText("Truncated"); got != "1" {
		t.Fatalf("Truncated = %q, want 1", got)
	}
	decoded, err := base64.StdEncoding.DecodeString(part.ChildText("Data"))
	if err != nil {
		t.Fatalf("payload is not valid base64: %v", err)
	}
	if len(decoded) != 40 {
		t.Fatalf("decoded %d bytes, want 40", len(decoded))
	}
	if string(decoded) != string(raw[:40]) {
		t.Fatalf("payload does not match the first 40 raw bytes")
	}
}

// A truncation larger than the message leaves it whole and unmarked.
func TestBodyPartMIMEnotTruncatedWhenSmall(t *testing.T) {
	raw := []byte("Subject: x\r\n\r\nbody")
	msg := &store.Message{ID: 4, Subject: "x", Raw: raw, Importance: 1}
	part := emailAppData(msg, nil, partPrefRequest(4, 4096)).Child("BodyPart")
	if part == nil {
		t.Fatalf("no BodyPart")
	}
	if part.Child("Truncated") != nil {
		t.Fatalf("Truncated should be absent")
	}
	decoded, err := base64.StdEncoding.DecodeString(part.ChildText("Data"))
	if err != nil {
		t.Fatalf("payload is not valid base64: %v", err)
	}
	if string(decoded) != string(raw) {
		t.Fatalf("payload mismatch")
	}
}
