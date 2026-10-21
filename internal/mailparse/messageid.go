package mailparse

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
)

// EnsureMessageID returns the message with a Message-ID header when it lacks
// one, along with the identifier in use. Clients reconcile their Sent copy with
// the server copy by this header, so outbound mail without one is generated
// here.
func EnsureMessageID(raw []byte, domain string) ([]byte, string) {
	if id := headerMessageID(raw); id != "" {
		return raw, id
	}
	if domain == "" {
		domain = "lifemail.local"
	}
	id := "<" + newMessageID(domain) + ">"

	header := "Message-ID: " + id + "\r\n"
	text := string(raw)
	if i := strings.Index(text, "\r\n\r\n"); i >= 0 {
		return []byte(text[:i+2] + header + text[i+2:]), id
	}
	if i := strings.Index(text, "\n\n"); i >= 0 {
		return []byte(text[:i+1] + header + text[i+1:]), id
	}
	// No body separator: append the header at the end of the header block.
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\r\n"
	}
	return []byte(text + header), id
}

func headerMessageID(raw []byte) string {
	text := string(raw)
	if i := strings.Index(text, "\r\n\r\n"); i >= 0 {
		text = text[:i]
	} else if i := strings.Index(text, "\n\n"); i >= 0 {
		text = text[:i]
	}
	for _, line := range strings.Split(text, "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "message-id") {
			continue
		}
		return strings.TrimSpace(value)
	}
	return ""
}

func newMessageID(domain string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("20060102150405"))) + "@" + domain
	}
	return hex.EncodeToString(b[:]) + "@" + domain
}
