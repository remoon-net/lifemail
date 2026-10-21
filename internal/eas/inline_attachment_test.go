package eas

import (
	"testing"

	"remoon.net/lifemail/internal/store"
)

// An inline image is referenced from the HTML body as cid:<ContentId>. The
// attachment entry has to carry that id and be flagged inline, otherwise the
// client renders a blank placeholder.
func TestEmailAppDataExposesInlineContentId(t *testing.T) {
	msg := &store.Message{
		ID:         7,
		Subject:    "with inline image",
		FromAddr:   "joke@example.com",
		ToAddr:     "alice@example.com",
		BodyHTML:   `<img src="cid:logo123">`,
		Size:       100,
		Importance: 1,
	}
	atts := []*store.Attachment{
		{ID: 11, MessageID: 7, FileName: "logo.png", ContentType: "image/png", Size: 42, ContentID: "logo123"},
		{ID: 12, MessageID: 7, FileName: "report.pdf", ContentType: "application/pdf", Size: 99},
	}
	app := emailAppData(msg, atts, bodyPref{Type: 2})

	list := app.Child("Attachments")
	if list == nil {
		t.Fatalf("no Attachments element")
	}
	if len(list.Children) != 2 {
		t.Fatalf("got %d attachments, want 2", len(list.Children))
	}

	inline := list.Children[0]
	if got := inline.ChildText("ContentId"); got != "logo123" {
		t.Fatalf("ContentId = %q, want logo123", got)
	}
	if got := inline.ChildText("IsInline"); got != "1" {
		t.Fatalf("IsInline = %q, want 1", got)
	}

	// A normal attachment gets neither field.
	plain := list.Children[1]
	if got := plain.ChildText("ContentId"); got != "" {
		t.Fatalf("plain attachment has ContentId %q", got)
	}
	if got := plain.ChildText("IsInline"); got != "" {
		t.Fatalf("plain attachment marked inline")
	}
}

// A part with a Content-ID but no disposition still counts as inline.
func TestEmailAppDataTreatsContentIdAsInline(t *testing.T) {
	msg := &store.Message{ID: 8, Subject: "x", Size: 10, Importance: 1}
	atts := []*store.Attachment{
		{ID: 21, MessageID: 8, FileName: "a.png", ContentType: "image/png", ContentID: "abc"},
	}
	app := emailAppData(msg, atts, bodyPref{Type: 2})
	list := app.Child("Attachments")
	if list == nil || len(list.Children) != 1 {
		t.Fatalf("attachment list missing")
	}
	if got := list.Children[0].ChildText("IsInline"); got != "1" {
		t.Fatalf("IsInline = %q, want 1", got)
	}
}
