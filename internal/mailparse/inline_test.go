package mailparse

import (
	"strings"
	"testing"
)

const inlineMessage = "From: joke@example.com\r\n" +
	"To: alice@example.com\r\n" +
	"Subject: inline\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/related; boundary=\"b1\"\r\n" +
	"\r\n" +
	"--b1\r\n" +
	"Content-Type: text/html; charset=UTF-8\r\n" +
	"\r\n" +
	`<img src="cid:logo123">` + "\r\n" +
	"--b1\r\n" +
	"Content-Type: image/png\r\n" +
	"Content-ID: <logo123>\r\n" +
	"Content-Disposition: inline; filename=\"logo.png\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"aGVsbG8=\r\n" +
	"--b1--\r\n"

// An image embedded in the HTML body arrives as an inline part. It used to be
// dropped, which left the client with an empty <img>.
func TestParseKeepsInlineImage(t *testing.T) {
	msg, err := Parse([]byte(inlineMessage))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.Contains(msg.HTML, "cid:logo123") {
		t.Fatalf("html not captured: %q", msg.HTML)
	}
	if len(msg.Parts) != 1 {
		t.Fatalf("got %d parts, want 1", len(msg.Parts))
	}
	p := msg.Parts[0]
	if p.ContentType != "image/png" {
		t.Fatalf("ContentType = %q", p.ContentType)
	}
	if p.ContentID != "logo123" {
		t.Fatalf("ContentID = %q, want logo123", p.ContentID)
	}
	if p.FileName != "logo.png" {
		t.Fatalf("FileName = %q", p.FileName)
	}
	if !p.Inline {
		t.Fatalf("part not marked inline")
	}
	if string(p.Data) != "hello" {
		t.Fatalf("Data = %q, want the decoded payload", p.Data)
	}
}
