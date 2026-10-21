package mailparse

import (
	"net/mail"
	"strings"
)

// EnsureFrom fills in an empty From header.
//
// MIUI Mail submits meeting replies and invitations with "From: " left blank.
// The stored and delivered MIME keeps that blank line, so recipients see no
// sender and mail clients show an empty from field even though the account is
// known. Only an empty value is replaced: a real From is never rewritten.
func EnsureFrom(raw []byte, name, addr string) []byte {
	if strings.TrimSpace(addr) == "" {
		return raw
	}
	value := addr
	if name != "" {
		value = (&mail.Address{Name: name, Address: addr}).String()
	}
	line := "From: " + value

	text := string(raw)
	end := -1
	if i := strings.Index(text, "\r\n\r\n"); i >= 0 {
		end = i
	} else if i := strings.Index(text, "\n\n"); i >= 0 {
		end = i
	}
	header := text
	if end >= 0 {
		header = text[:end]
	}

	// Replace an existing empty From header in place so no duplicate appears.
	lines := strings.Split(header, "\n")
	for i, l := range lines {
		trimmed := strings.TrimRight(l, "\r")
		if len(trimmed) < 5 || !strings.EqualFold(trimmed[:5], "From:") {
			continue
		}
		if strings.TrimSpace(trimmed[5:]) != "" {
			return raw // already has a sender
		}
		keepCR := strings.HasSuffix(l, "\r")
		lines[i] = line
		if keepCR {
			lines[i] += "\r"
		}
		newHeader := strings.Join(lines, "\n")
		if end >= 0 {
			return []byte(newHeader + text[end:])
		}
		return []byte(newHeader)
	}

	// No From header at all: add one at the top of the header block.
	if end >= 0 {
		return []byte(line + "\r\n" + text)
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\r\n"
	}
	return []byte(text + line + "\r\n")
}
