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
		UserID: user.ID, FolderID: inbox.ID, Subject: "Re: 合同",
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
	// Another folder must not leak into the inbox thread list.
	insertTestMessage(t, db, &Message{
		UserID: user.ID, FolderID: sent.ID, Subject: "Re: 合同",
		DateReceived: 400, ConversationID: "ref:a@x",
	})

	sums, err := db.ConversationSummaries(inbox.ID, 50)
	if err != nil {
		t.Fatalf("summaries: %v", err)
	}
	if len(sums) != 3 {
		t.Fatalf("summaries = %d, want 3", len(sums))
	}
	// Newest thread first: 周报 (300), 无会话标识 (150), 合同 (200) — actually
	// 合同's latest is 200, so order is 300, 200, 150.
	if sums[0].Key != "ref:b@x" || sums[0].Count != 1 || sums[0].Unread != 1 {
		t.Fatalf("first summary = %+v", sums[0])
	}
	if sums[1].Key != "ref:a@x" {
		t.Fatalf("second summary key = %q, want ref:a@x", sums[1].Key)
	}
	if sums[1].Count != 2 || sums[1].Unread != 1 {
		t.Fatalf("thread count/unread = %d/%d, want 2/1", sums[1].Count, sums[1].Unread)
	}
	if sums[1].FromName != "李四" || !sums[1].HasAttachments {
		t.Fatalf("thread should describe its latest message: %+v", sums[1])
	}
	if sums[1].Preview != "收到，明天回复。" {
		t.Fatalf("preview = %q", sums[1].Preview)
	}
	wantKey := "m:" + itoa(lonely.ID)
	if sums[2].Key != wantKey {
		t.Fatalf("fallback key = %q, want %q", sums[2].Key, wantKey)
	}

	thread, err := db.ConversationMessages(inbox.ID, "ref:a@x", 50)
	if err != nil {
		t.Fatalf("thread: %v", err)
	}
	if len(thread) != 2 || thread[0].DateReceived != 100 || thread[1].DateReceived != 200 {
		t.Fatalf("thread order = %+v", thread)
	}

	single, err := db.ConversationMessages(inbox.ID, wantKey, 50)
	if err != nil {
		t.Fatalf("fallback thread: %v", err)
	}
	if len(single) != 1 || single[0].ID != lonely.ID {
		t.Fatalf("fallback thread = %+v", single)
	}

	// A key from another folder must not resolve.
	if _, err := db.ConversationMessages(sent.ID, wantKey, 50); err == nil {
		t.Fatal("fallback key resolved in the wrong folder")
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
