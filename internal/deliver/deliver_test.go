package deliver

import (
	"path/filepath"
	"testing"

	"remoon.net/lifemail/internal/blobstore"
	"remoon.net/lifemail/internal/mailparse"
	"remoon.net/lifemail/internal/store"
)

func deliverFixture(t *testing.T) (*store.Store, *store.User) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "mail.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	blobs, err := blobstore.Open(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatalf("blobs: %v", err)
	}
	st.SetBlobs(blobs)
	user, err := st.CreateUser("alice", "alice@example.com", "Alice", "hash")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	return st, user
}

func parseMail(t *testing.T, raw string) *mailparse.Message {
	t.Helper()
	m, err := mailparse.Parse([]byte(raw))
	if err != nil || m == nil {
		t.Fatalf("parse: %v", err)
	}
	return m
}

// MIUI Mail replies with only a "Re:" subject and no References/In-Reply-To.
// Such a reply must still land in the thread it answers instead of starting a
// new one, otherwise the message being replied to disappears from the
// conversation.
func TestToUserKeepsHeaderlessReplyInThread(t *testing.T) {
	st, user := deliverFixture(t)

	root := "From: Alice <alice@example.com>\r\nTo: bob@example.com\r\n" +
		"Subject: =?utf-8?q?=E5=91=A8=E6=9C=AB=E8=AE=A1=E5=88=92?=\r\n" +
		"Message-ID: <root@example.com>\r\nDate: Mon, 26 Oct 2026 10:00:00 +0800\r\n" +
		"\r\nbody\r\n"
	if _, err := ToUser(st, user, FolderSent, parseMail(t, root), []byte(root)); err != nil {
		t.Fatalf("store sent copy: %v", err)
	}

	reply := "From: bob@example.com\r\nTo: alice@example.com\r\nSubject: Re: 周末计划\r\n" +
		"Message-ID: <reply@example.com>\r\nDate: Mon, 26 Oct 2026 11:00:00 +0800\r\n" +
		"\r\nbody\r\n"
	id, err := ToUser(st, user, FolderInbox, parseMail(t, reply), []byte(reply))
	if err != nil {
		t.Fatalf("store reply: %v", err)
	}

	m, err := st.MessageMetaByID(id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if m.ConversationID != "ref:root@example.com" {
		t.Fatalf("conversation = %q, want the thread it replies to", m.ConversationID)
	}
}

// Replies that do carry References keep using the header-derived key.
func TestToUserPrefersThreadingHeaders(t *testing.T) {
	st, user := deliverFixture(t)

	root := "From: Alice <alice@example.com>\r\nTo: bob@example.com\r\nSubject: Plan\r\n" +
		"Message-ID: <root@example.com>\r\nDate: Mon, 26 Oct 2026 10:00:00 +0800\r\n" +
		"\r\nbody\r\n"
	if _, err := ToUser(st, user, FolderSent, parseMail(t, root), []byte(root)); err != nil {
		t.Fatalf("store sent copy: %v", err)
	}

	// References points at another thread, and that wins over the subject.
	reply := "From: bob@example.com\r\nTo: alice@example.com\r\nSubject: Re: Plan\r\n" +
		"Message-ID: <reply@example.com>\r\nReferences: <elsewhere@example.com>\r\n" +
		"Date: Mon, 26 Oct 2026 11:00:00 +0800\r\n\r\nbody\r\n"
	id, err := ToUser(st, user, FolderInbox, parseMail(t, reply), []byte(reply))
	if err != nil {
		t.Fatalf("store reply: %v", err)
	}
	m, err := st.MessageMetaByID(id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if m.ConversationID != "ref:elsewhere@example.com" {
		t.Fatalf("conversation = %q, want the References root", m.ConversationID)
	}
}

// The same fallback applies to the Sent copy of a reply sent from a phone: the
// thread lives in the sender's own Inbox, so the lookup has to search both
// folders.
func TestToUserFindsThreadFromSentSide(t *testing.T) {
	st, user := deliverFixture(t)

	incoming := "From: bob@example.com\r\nTo: alice@example.com\r\nSubject: 周末计划\r\n" +
		"Message-ID: <root@example.com>\r\nDate: Mon, 26 Oct 2026 10:00:00 +0800\r\n" +
		"\r\nbody\r\n"
	if _, err := ToUser(st, user, FolderInbox, parseMail(t, incoming), []byte(incoming)); err != nil {
		t.Fatalf("store inbox copy: %v", err)
	}

	reply := "From: alice@example.com\r\nTo: bob@example.com\r\nSubject: Re: 周末计划\r\n" +
		"Message-ID: <phone-reply@example.com>\r\nDate: Mon, 26 Oct 2026 11:00:00 +0800\r\n" +
		"\r\nbody\r\n"
	id, err := ToUser(st, user, FolderSent, parseMail(t, reply), []byte(reply))
	if err != nil {
		t.Fatalf("store sent reply: %v", err)
	}
	m, err := st.MessageMetaByID(id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if m.ConversationID != "ref:root@example.com" {
		t.Fatalf("conversation = %q, want the thread from the inbox", m.ConversationID)
	}
}
