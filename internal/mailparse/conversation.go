package mailparse

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
)

// ConversationID returns an identifier shared by every message of one thread,
// so a client can group them without the server maintaining a conversation
// index.
//
// The References chain identifies a thread exactly; when a message has none,
// a hash of its normalised subject is used, which is what mail clients
// themselves fall back to.
func ConversationID(m *Message) string {
	if m == nil {
		return ""
	}
	// A reply points at the chain root, so the root message must identify
	// itself by its own id — otherwise the first message of a thread and its
	// replies would land in different conversations.
	if len(m.References) > 0 {
		return "ref:" + strings.Trim(m.References[0], "<>")
	}
	if id := strings.Trim(m.InReplyTo, "<> "); id != "" {
		return "ref:" + id
	}
	if id := strings.Trim(m.MessageID, "<> "); id != "" {
		return "ref:" + id
	}
	// Neither a chain nor an id: a normalised subject is all that is left.
	if subject := normalizeSubject(m.Subject); subject != "" {
		sum := sha1.Sum([]byte(subject))
		return "sub:" + hex.EncodeToString(sum[:8])
	}
	return ""
}

// subjectPrefixes are stripped when normalising a subject, in the languages
// this server is likely to see.
var subjectPrefixes = []string{
	"re:", "re：", "re ", "aw:", "sv:",
	"fwd:", "fwd：", "fw:", "fw：", "wg:",
	"回复:", "回复：", "答复:", "答复：",
	"转发:", "转发：", "转发 ",
}

// normalizeSubject lower-cases a subject and removes reply/forward prefixes so
// that "Re: Re: Invoice" and "Invoice" share one conversation.
func normalizeSubject(subject string) string {
	s := strings.ToLower(strings.TrimSpace(subject))
	for {
		trimmed := s
		for _, prefix := range subjectPrefixes {
			if strings.HasPrefix(trimmed, prefix) {
				trimmed = strings.TrimSpace(trimmed[len(prefix):])
				break
			}
		}
		if trimmed == s {
			break
		}
		s = trimmed
	}
	// Collapse runs of whitespace so "a  b" and "a b" match.
	return strings.Join(strings.Fields(s), " ")
}

// messageIDs extracts every <...> token from a header value.
func messageIDs(value string) []string {
	var out []string
	rest := value
	for {
		open := strings.IndexByte(rest, '<')
		if open < 0 {
			break
		}
		close := strings.IndexByte(rest[open:], '>')
		if close < 0 {
			break
		}
		id := strings.TrimSpace(rest[open+1 : open+close])
		if id != "" {
			out = append(out, id)
		}
		rest = rest[open+close+1:]
	}
	// Some clients omit the angle brackets; fall back to whitespace splitting.
	if len(out) == 0 {
		for _, field := range strings.Fields(value) {
			if id := strings.Trim(field, "<> "); id != "" {
				out = append(out, id)
			}
		}
	}
	return out
}

// firstMessageID returns the first message-id in a header value.
func firstMessageID(value string) string {
	if ids := messageIDs(value); len(ids) > 0 {
		return ids[0]
	}
	return ""
}
