package mailparse

import "testing"

// Every message of a thread must end up with the same identifier, whether it
// carries a References chain or only a reply subject.
func TestConversationIDFromReferences(t *testing.T) {
	first, err := Parse([]byte("From: a@example.com\r\nSubject: Invoice\r\n" +
		"Message-ID: <root@example.com>\r\n\r\nbody\r\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	reply, err := Parse([]byte("From: b@example.com\r\nSubject: Re: Invoice\r\n" +
		"Message-ID: <reply@example.com>\r\nReferences: <root@example.com> <mid@example.com>\r\n\r\nbody\r\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	rootID := ConversationID(first)
	replyID := ConversationID(reply)
	if rootID == "" || replyID == "" {
		t.Fatalf("empty ids: %q %q", rootID, replyID)
	}
	// The root names itself, the reply points at the chain root: same thread.
	if rootID != "ref:root@example.com" {
		t.Fatalf("root id = %q, want ref:root@example.com", rootID)
	}
	if replyID != rootID {
		t.Fatalf("reply id %q does not match root %q", replyID, rootID)
	}
}

// Without a Message-ID or References a subject hash has to stand in.
func TestConversationIDFallsBackToSubject(t *testing.T) {
	a, _ := Parse([]byte("From: a@example.com\r\nSubject: No id here\r\n\r\nbody\r\n"))
	b, _ := Parse([]byte("From: b@example.com\r\nSubject: Re: No id here\r\n\r\nbody\r\n"))
	if id := ConversationID(a); id[:4] != "sub:" {
		t.Fatalf("id = %q, want a subject hash", id)
	}
	if ConversationID(a) != ConversationID(b) {
		t.Fatalf("subject fallback split the thread")
	}
}

// Without a References chain the normalised subject decides, so "Re: Re: X"
// and "X" group together.
func TestConversationIDFromSubject(t *testing.T) {
	a, _ := Parse([]byte("From: a@example.com\r\nSubject: 项目进度\r\n\r\nbody\r\n"))
	b, _ := Parse([]byte("From: b@example.com\r\nSubject: Re: 项目进度\r\n\r\nbody\r\n"))
	c, _ := Parse([]byte("From: c@example.com\r\nSubject: RE:  Re:  项目进度\r\n\r\nbody\r\n"))
	d, _ := Parse([]byte("From: d@example.com\r\nSubject: 别的主题\r\n\r\nbody\r\n"))

	idA, idB, idC, idD := ConversationID(a), ConversationID(b), ConversationID(c), ConversationID(d)
	if idA != idB || idA != idC {
		t.Fatalf("thread split: %q %q %q", idA, idB, idC)
	}
	if idA == idD {
		t.Fatalf("unrelated subjects share an id: %q", idA)
	}
}

// In-Reply-To alone is enough to join a thread.
func TestConversationIDFromInReplyTo(t *testing.T) {
	m, _ := Parse([]byte("From: a@example.com\r\nSubject: hello\r\n" +
		"Message-ID: <m@x>\r\nIn-Reply-To: <parent@example.com>\r\n\r\nbody\r\n"))
	if got := ConversationID(m); got != "ref:parent@example.com" {
		t.Fatalf("id = %q", got)
	}
}

func TestNormalizeSubject(t *testing.T) {
	cases := map[string]string{
		"Re: Invoice":      "invoice",
		"RE:  re: Invoice": "invoice",
		"Fwd: 回复: 会议纪要":    "会议纪要",
		"  普通主题  ":         "普通主题",
		"Re:":              "",
	}
	for in, want := range cases {
		if got := normalizeSubject(in); got != want {
			t.Fatalf("normalizeSubject(%q) = %q, want %q", in, got, want)
		}
	}
}
