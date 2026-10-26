package store

import "testing"

// A reply without References/In-Reply-To keeps the thread of the message it
// answers: same normalised subject plus a shared participant. Subject alone is
// not enough, because unrelated senders reuse generic subjects.
func TestThreadForReplyMatchesSubjectAndParticipants(t *testing.T) {
	db, user, inbox, sent := mailFixture(t)

	root := insertTestMessage(t, db, &Message{
		UserID: user.ID, FolderID: sent.ID, Subject: "周末计划",
		FromAddr: user.Address, ToAddr: "bob@example.com",
		DateReceived: 100, ConversationID: "ref:root@example.com", IsRead: true,
	})
	// Same subject, but nobody in common with the reply below.
	unrelated := insertTestMessage(t, db, &Message{
		UserID: user.ID, FolderID: inbox.ID, Subject: "周末计划",
		FromAddr: "other@example.com", ToAddr: "someone@example.com",
		DateReceived: 200, ConversationID: "ref:other@example.com",
	})
	// An earlier headerless reply that already got a key of its own: the oldest
	// message of the thread must still win, or the stray key becomes the anchor.
	stray := insertTestMessage(t, db, &Message{
		UserID: user.ID, FolderID: inbox.ID, Subject: "Re: 周末计划",
		FromAddr: "bob@example.com", ToAddr: user.Address,
		DateReceived: 300, ConversationID: "ref:stray@example.com",
	})

	got, err := db.ThreadForReply(user.ID, "Re: 周末计划", "bob@example.com", []string{user.Address})
	if err != nil {
		t.Fatalf("thread for reply: %v", err)
	}
	if got != "ref:root@example.com" {
		t.Fatalf("thread = %q, want the thread the sender wrote to", got)
	}

	// The unrelated sender must not be matched even though the subject agrees.
	if got, err := db.ThreadForReply(user.ID, "Re: 周末计划", "stranger@example.com",
		[]string{"nobody@example.com"}); err != nil || got != "" {
		t.Fatalf("unrelated thread matched: %q, err %v", got, err)
	}

	// A different subject never matches.
	if got, err := db.ThreadForReply(user.ID, "Re: 别的主题", "bob@example.com",
		[]string{user.Address}); err != nil || got != "" {
		t.Fatalf("other subject matched: %q, err %v", got, err)
	}

	// Deleted messages do not anchor threads.
	if _, err := db.exec(`UPDATE messages SET deleted = 1 WHERE id IN (?, ?, ?)`,
		root.ID, unrelated.ID, stray.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if got, err := db.ThreadForReply(user.ID, "Re: 周末计划", "bob@example.com",
		[]string{user.Address}); err != nil || got != "" {
		t.Fatalf("deleted thread matched: %q, err %v", got, err)
	}
}
