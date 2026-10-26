package store

import (
	"path/filepath"
	"testing"

	"remoon.net/lifemail/internal/blobstore"
)

func mailFixture(t *testing.T) (*Store, *User, *Folder, *Folder) {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "mail.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	blobs, err := blobstore.Open(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatalf("blobs: %v", err)
	}
	db.SetBlobs(blobs)

	user, err := db.CreateUser("alice", "alice@example.com", "Alice", "hash")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	inbox, err := db.FolderByType(user.ID, 2)
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	sent, err := db.FolderByType(user.ID, 5)
	if err != nil {
		t.Fatalf("sent: %v", err)
	}
	return db, user, inbox, sent
}

func insertTestMessage(t *testing.T, db *Store, m *Message) *Message {
	t.Helper()
	id, err := db.InsertMessage(m)
	if err != nil {
		t.Fatalf("insert message: %v", err)
	}
	got, err := db.MessageMetaByID(id)
	if err != nil {
		t.Fatalf("reload message: %v", err)
	}
	return got
}

func TestConversationSummariesGroupByThread(t *testing.T) {
	db, user, inbox, sent := mailFixture(t)

	insertTestMessage(t, db, &Message{
		UserID: user.ID, FolderID: inbox.ID, Subject: "合同",
		FromName: "张三", FromAddr: "z@example.com", ToAddr: user.Address,
		DateReceived: 100, ConversationID: "ref:a@x", IsRead: true,
		BodyText: "第一批\n附件见上",
	})
	insertTestMessage(t, db, &Message{
		UserID: user.ID, FolderID: inbox.ID, Subject: "Re: 合同",
		FromName: "李四", FromAddr: "l@example.com", ToAddr: user.Address,
		DateReceived: 200, ConversationID: "ref:a@x", IsRead: false,
		BodyText: "收到，明天回复。", HasAttachments: true,
	})
	insertTestMessage(t, db, &Message{
		UserID: user.ID, FolderID: inbox.ID, Subject: "周报",
		FromName: "王五", FromAddr: "w@example.com", ToAddr: user.Address,
		DateReceived: 300, ConversationID: "ref:b@x", IsRead: false,
	})
	lonely := insertTestMessage(t, db, &Message{
		UserID: user.ID, FolderID: inbox.ID, Subject: "无会话标识",
		FromName: "赵六", FromAddr: "zh@example.com", ToAddr: user.Address,
		DateReceived: 150,
	})
	// A reply stored in Sent joins the same conversation.
	insertTestMessage(t, db, &Message{
		UserID: user.ID, FolderID: sent.ID, Subject: "Re: 合同",
		FromName: "Alice", FromAddr: user.Address, ToAddr: "l@example.com",
		DateReceived: 400, ConversationID: "ref:a@x", IsRead: true,
		BodyText: "好的，确认",
	})

	sums, err := db.ConversationSummaries(user.ID, inbox.ID, 50)
	if err != nil {
		t.Fatalf("summaries: %v", err)
	}
	if len(sums) != 3 {
		t.Fatalf("summaries = %d, want 3", len(sums))
	}
	// The sent reply makes 合同 the newest thread (400), then 周报 (300).
	if sums[0].Key != "ref:a@x" {
		t.Fatalf("first summary key = %q, want ref:a@x", sums[0].Key)
	}
	if sums[0].Count != 3 || sums[0].Unread != 1 {
		t.Fatalf("thread count/unread = %d/%d, want 3/1", sums[0].Count, sums[0].Unread)
	}
	if !sums[0].HasInbox || !sums[0].HasSent {
		t.Fatalf("thread should span inbox and sent: %+v", sums[0])
	}
	if sums[0].FromName != "李四" || sums[0].Preview != "好的，确认" {
		t.Fatalf("thread should name its newest inbound sender but preview the latest message: %+v",
			sums[0])
	}
	// The thread keeps the subject of its oldest message, not the "Re:" reply.
	if sums[0].Subject != "合同" {
		t.Fatalf("thread subject = %q, want the oldest message's subject", sums[0].Subject)
	}
	if sums[1].Key != "ref:b@x" || sums[1].Count != 1 || sums[1].Unread != 1 {
		t.Fatalf("second summary = %+v", sums[1])
	}
	wantKey := "m:" + itoa(lonely.ID)
	if sums[2].Key != wantKey {
		t.Fatalf("fallback key = %q, want %q", sums[2].Key, wantKey)
	}

	// The Sent view only shows threads that contain a sent message.
	sentSums, err := db.ConversationSummaries(user.ID, sent.ID, 50)
	if err != nil {
		t.Fatalf("sent summaries: %v", err)
	}
	if len(sentSums) != 1 || sentSums[0].Key != "ref:a@x" {
		t.Fatalf("sent summaries = %+v", sentSums)
	}

	thread, err := db.ConversationMessages(user.ID, "ref:a@x", inbox.ID, 50)
	if err != nil {
		t.Fatalf("thread: %v", err)
	}
	if len(thread) != 3 || thread[0].DateReceived != 100 ||
		thread[1].DateReceived != 200 || thread[2].DateReceived != 400 {
		t.Fatalf("thread order = %+v", thread)
	}
	if thread[2].FolderID != sent.ID {
		t.Fatalf("reply is not marked as sent: %+v", thread[2])
	}

	// The fallback key is readable from either side of the shared scope.
	single, err := db.ConversationMessages(user.ID, wantKey, sent.ID, 50)
	if err != nil {
		t.Fatalf("fallback thread: %v", err)
	}
	if len(single) != 1 || single[0].ID != lonely.ID {
		t.Fatalf("fallback thread = %+v", single)
	}

	// Other folders stay isolated: the fallback key is not visible in Spam.
	spam, err := db.FolderByType(user.ID, 12)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ConversationMessages(user.ID, wantKey, spam.ID, 50); err == nil {
		t.Fatal("fallback key resolved outside the inbox/sent scope")
	}
}

func TestFolderCountsAndContactSearch(t *testing.T) {
	db, user, inbox, _ := mailFixture(t)

	insertTestMessage(t, db, &Message{UserID: user.ID, FolderID: inbox.ID, IsRead: false})
	insertTestMessage(t, db, &Message{UserID: user.ID, FolderID: inbox.ID, IsRead: true})
	deleted := insertTestMessage(t, db, &Message{UserID: user.ID, FolderID: inbox.ID})
	if err := db.SoftDeleteMessage(deleted.ID); err != nil {
		t.Fatal(err)
	}

	counts, err := db.FolderCounts(user.ID)
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if len(counts) != 1 || counts[0].FolderID != inbox.ID {
		t.Fatalf("counts = %+v", counts)
	}
	if counts[0].Total != 2 || counts[0].Unread != 1 {
		t.Fatalf("inbox total/unread = %d/%d, want 2/1", counts[0].Total, counts[0].Unread)
	}

	contactsFolder, err := db.FolderByType(user.ID, 9)
	if err != nil {
		t.Fatalf("contacts folder: %v", err)
	}
	for _, c := range []*Contact{
		{UserID: user.ID, FolderID: contactsFolder.ID, FirstName: "张", LastName: "三", FileAs: "张三", Company: "示例科技", Email1: "zhang@example.com"},
		{UserID: user.ID, FolderID: contactsFolder.ID, FirstName: "李", LastName: "四", FileAs: "李四", Company: "其他公司", Email1: "li@other.test"},
	} {
		if _, err := db.InsertContact(c); err != nil {
			t.Fatalf("insert contact: %v", err)
		}
	}

	all, err := db.SearchContacts(user.ID, "", 100)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("contacts = %d, want 2", len(all))
	}
	hits, err := db.SearchContacts(user.ID, "示例", 100)
	if err != nil {
		t.Fatalf("search by company: %v", err)
	}
	if len(hits) != 1 || hits[0].FileAs != "张三" {
		t.Fatalf("company search = %+v", hits)
	}
	hits, err = db.SearchContacts(user.ID, "OTHER.TEST", 100)
	if err != nil {
		t.Fatalf("search by email: %v", err)
	}
	if len(hits) != 1 || hits[0].Email1 != "li@other.test" {
		t.Fatalf("email search = %+v", hits)
	}
	// Contacts belong to one account only.
	other, err := db.CreateUser("bob", "bob@example.com", "Bob", "hash")
	if err != nil {
		t.Fatal(err)
	}
	hits, err = db.SearchContacts(other.ID, "", 100)
	if err != nil {
		t.Fatalf("other search: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("other account saw %d contacts", len(hits))
	}
}

func TestEventsByFolderBetween(t *testing.T) {
	db, user, _, _ := mailFixture(t)
	cal, err := db.FolderByType(user.ID, FolderTypeCalendar)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []*Event{
		{UserID: user.ID, FolderID: cal.ID, UID: "e1@x", Subject: "早会", StartTime: 1000, EndTime: 1600, DtStamp: 1000},
		{UserID: user.ID, FolderID: cal.ID, UID: "e2@x", Subject: "周会", StartTime: 5000, EndTime: 5600, DtStamp: 5000},
	} {
		if _, err := db.InsertEvent(e); err != nil {
			t.Fatalf("insert event: %v", err)
		}
	}
	got, err := db.EventsByFolderBetween(cal.ID, 900, 1100)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(got) != 1 || got[0].Subject != "早会" {
		t.Fatalf("range events = %+v", got)
	}
	all, err := db.EventsByFolderBetween(cal.ID, 0, 0)
	if err != nil {
		t.Fatalf("all events: %v", err)
	}
	if len(all) != 2 || all[0].StartTime >= all[1].StartTime {
		t.Fatalf("unbounded events = %+v", all)
	}
}

// itoa avoids importing strconv just for one assertion.
func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

func TestSearchMailSpansFolders(t *testing.T) {
	db, user, inbox, sent := mailFixture(t)

	insertTestMessage(t, db, &Message{
		UserID: user.ID, FolderID: inbox.ID, Subject: "季度报告",
		FromName: "张三", FromAddr: "z@example.com", ToAddr: user.Address,
		ConversationID: "ref:q@x", DateReceived: 100,
		BodyText: "请查收附件，下周开会讨论。",
	})
	insertTestMessage(t, db, &Message{
		UserID: user.ID, FolderID: sent.ID, Subject: "Re: 季度报告",
		FromName: "Alice", FromAddr: user.Address, ToAddr: "z@example.com",
		ConversationID: "ref:q@x", DateReceived: 200, IsRead: true,
		BodyText: "收到，我会准备材料。",
	})
	insertTestMessage(t, db, &Message{
		UserID: user.ID, FolderID: inbox.ID, Subject: "会议纪要",
		FromName: "李四", FromAddr: "li@example.com", ToAddr: user.Address,
		ConversationID: "ref:m@x", DateReceived: 300,
		BodyText: "纪要见正文。",
	})

	// The body match reaches both folders.
	hits, total, err := db.SearchMail(user.ID, "准备材料", 0, 20, 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if total != 1 || len(hits) != 1 {
		t.Fatalf("body search = %d hits (total %d)", len(hits), total)
	}
	if hits[0].Message.FolderID != sent.ID {
		t.Fatalf("hit folder = %d, want sent", hits[0].Message.FolderID)
	}
	if hits[0].FolderName == "" || hits[0].ConversationKey != "ref:q@x" {
		t.Fatalf("hit metadata = %+v", hits[0])
	}

	// Subject and recipient matches are covered too.
	for _, term := range []string{"季度", "li@example.com"} {
		if _, total, err := db.SearchMail(user.ID, term, 0, 20, 0); err != nil || total == 0 {
			t.Fatalf("search %q: total %d, err %v", term, total, err)
		}
	}

	// Limiting to one folder hides the other one.
	hits, total, err = db.SearchMail(user.ID, "季度", inbox.ID, 20, 0)
	if err != nil {
		t.Fatalf("scoped search: %v", err)
	}
	if total != 1 || len(hits) != 1 || hits[0].Message.FolderID != inbox.ID {
		t.Fatalf("scoped hits = %+v (total %d)", hits, total)
	}

	// Wildcards in the term are literal, and an empty term matches nothing.
	if _, total, err := db.SearchMail(user.ID, "%", 0, 20, 0); err != nil || total != 0 {
		t.Fatalf("wildcard search: total %d, err %v", total, err)
	}
	if hits, total, err := db.SearchMail(user.ID, "  ", 0, 20, 0); err != nil || total != 0 || len(hits) != 0 {
		t.Fatalf("empty search: %d hits (total %d), err %v", len(hits), total, err)
	}
}
