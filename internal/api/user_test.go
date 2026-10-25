package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"remoon.net/lifemail/internal/blobstore"
	"remoon.net/lifemail/internal/deliver"
	"remoon.net/lifemail/internal/mailparse"
	"remoon.net/lifemail/internal/store"
)

func newUserAPI(t *testing.T) (*User, *store.Store, *store.User) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "webmail.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	blobs, err := blobstore.Open(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatalf("open blobs: %v", err)
	}
	db.SetBlobs(blobs)

	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	user, err := db.CreateUser("alice", "alice@example.com", "Alice", string(hash))
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	h := &User{
		Store:         db,
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Token:         "admin-token",
		BaseURL:       "https://mail.example.com",
		Version:       "test",
		IsLocalDomain: func(domain string) bool { return domain == "example.com" },
	}
	return h, db, user
}

func userDo(h http.Handler, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func loginUser(t *testing.T, h *User) *http.Cookie {
	t.Helper()
	rec := userDo(h, http.MethodPost, "/api/user/session",
		`{"login":"alice","password":"secret"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d: %s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == userCookieName {
			if !c.HttpOnly {
				t.Fatal("session cookie is not HttpOnly")
			}
			return c
		}
	}
	t.Fatal("session cookie missing")
	return nil
}

func TestUserLoginAndSession(t *testing.T) {
	h, _, _ := newUserAPI(t)

	rec := userDo(h, http.MethodPost, "/api/user/session",
		`{"login":"alice@example.com","password":"wrong"}`, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d", rec.Code)
	}

	// Login accepts the primary address as well as the bare name.
	cookie := loginUser(t, h)

	rec = userDo(h, http.MethodGet, "/api/user/session", "", cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"authenticated":true`) {
		t.Fatalf("session = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"address":"alice@example.com"`) {
		t.Fatalf("session body = %s", rec.Body.String())
	}

	// Without the cookie every data endpoint is closed.
	rec = userDo(h, http.MethodGet, "/api/user/mail/folders", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated folders status = %d", rec.Code)
	}

	// A tampered payload must not be accepted.
	forged := &http.Cookie{Name: userCookieName, Value: cookie.Value[:len(cookie.Value)-1] + "0"}
	rec = userDo(h, http.MethodGet, "/api/user/mail/folders", "", forged)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("forged cookie status = %d", rec.Code)
	}

	rec = userDo(h, http.MethodDelete, "/api/user/session", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout status = %d", rec.Code)
	}
}

func TestUserConversationsAndRead(t *testing.T) {
	h, db, user := newUserAPI(t)
	cookie := loginUser(t, h)

	inbox, err := db.FolderByType(user.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	first, err := db.InsertMessage(&store.Message{
		UserID: user.ID, FolderID: inbox.ID, Subject: "Re: 合同",
		FromName: "张三", FromAddr: "z@example.com", DateReceived: 100,
		ConversationID: "ref:a@x", IsRead: true, BodyText: "第一批",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.InsertMessage(&store.Message{
		UserID: user.ID, FolderID: inbox.ID, Subject: "Re: 合同",
		FromName: "李四", FromAddr: "l@example.com", DateReceived: 200,
		ConversationID: "ref:a@x", BodyText: "收到",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddChange(inbox.ID, "add", "email", first); err != nil {
		t.Fatal(err)
	}

	rec := userDo(h, http.MethodGet, "/api/user/mail/conversations", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("conversations status = %d: %s", rec.Code, rec.Body.String())
	}
	var listing struct {
		FolderID      int64 `json:"folderId"`
		Conversations []struct {
			Key    string `json:"key"`
			Count  int    `json:"count"`
			Unread int    `json:"unread"`
		} `json:"conversations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if listing.FolderID != inbox.ID || len(listing.Conversations) != 1 {
		t.Fatalf("listing = %+v", listing)
	}
	if listing.Conversations[0].Count != 2 || listing.Conversations[0].Unread != 1 {
		t.Fatalf("conversation = %+v", listing.Conversations[0])
	}

	key := listing.Conversations[0].Key
	rec = userDo(h, http.MethodGet, "/api/user/mail/thread?key="+key, "", cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"bodyText":"收到"`) {
		t.Fatalf("thread = %d %s", rec.Code, rec.Body.String())
	}

	// Marking read flips the flag and records a sync change.
	rec = userDo(h, http.MethodPost, "/api/user/mail/messages/"+itoaInt(second)+"/read", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark read status = %d: %s", rec.Code, rec.Body.String())
	}
	reloaded, err := db.MessageMetaByID(second)
	if err != nil || !reloaded.IsRead {
		t.Fatalf("message not marked read: %+v %v", reloaded, err)
	}
	changes, err := db.ChangesSince(inbox.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range changes {
		if c.ItemID == second && c.Kind == "change" {
			found = true
		}
	}
	if !found {
		t.Fatalf("read change missing from %+v", changes)
	}

	// Another account cannot reach the message.
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if _, err := db.CreateUser("bob", "bob@example.com", "Bob", string(hash)); err != nil {
		t.Fatal(err)
	}
	bobCookie := func() *http.Cookie {
		rec := userDo(h, http.MethodPost, "/api/user/session",
			`{"login":"bob","password":"secret"}`, nil)
		for _, c := range rec.Result().Cookies() {
			if c.Name == userCookieName {
				return c
			}
		}
		t.Fatal("bob login failed")
		return nil
	}()
	rec = userDo(h, http.MethodGet, "/api/user/mail/messages/"+itoaInt(second), "", bobCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account message status = %d", rec.Code)
	}
	rec = userDo(h, http.MethodGet, "/api/user/mail/conversations?folder="+itoaInt(inbox.ID), "", bobCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account folder status = %d", rec.Code)
	}
}

func TestUserCalendarsAndContacts(t *testing.T) {
	h, db, user := newUserAPI(t)
	cookie := loginUser(t, h)

	rec := userDo(h, http.MethodPost, "/api/user/calendars", `{"name":"工作"}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create calendar status = %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Calendar struct {
			ID           int64  `json:"id"`
			Name         string `json:"name"`
			SubscribeURL string `json:"subscribeUrl"`
		} `json:"calendar"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Calendar.Name != "工作" || !strings.HasSuffix(created.Calendar.SubscribeURL, ".ics") {
		t.Fatalf("created calendar = %+v", created.Calendar)
	}

	rec = userDo(h, http.MethodPost, "/api/user/calendars", `{"name":"工作"}`, cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate name status = %d", rec.Code)
	}

	id := itoaInt(created.Calendar.ID)
	rec = userDo(h, http.MethodPatch, "/api/user/calendars/"+id, `{"name":"个人"}`, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"个人"`) {
		t.Fatalf("rename = %d %s", rec.Code, rec.Body.String())
	}

	rec = userDo(h, http.MethodPut, "/api/user/calendars/"+id+"/public", `{"public":true}`, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"public":true`) {
		t.Fatalf("publish = %d %s", rec.Code, rec.Body.String())
	}

	// Events are listed in a time window.
	rec = userDo(h, http.MethodGet, "/api/user/calendars/"+id+"/events", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("events status = %d: %s", rec.Code, rec.Body.String())
	}

	contactsFolder, err := db.FolderByType(user.ID, 9)
	if err != nil {
		t.Fatal(err)
	}
	contactID, err := db.InsertContact(&store.Contact{
		UserID: user.ID, FolderID: contactsFolder.ID, FileAs: "张三",
		FirstName: "三", LastName: "张", Email1: "zhang@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	rec = userDo(h, http.MethodGet, "/api/user/contacts?q=zhang", "", cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"fileAs":"张三"`) {
		t.Fatalf("contacts = %d %s", rec.Code, rec.Body.String())
	}
	rec = userDo(h, http.MethodGet, "/api/user/contacts/"+itoaInt(contactID), "", cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"email1":"zhang@example.com"`) {
		t.Fatalf("contact = %d %s", rec.Code, rec.Body.String())
	}

	// Without an admin token the webmail login is unavailable.
	h.Token = ""
	rec = userDo(h, http.MethodPost, "/api/user/session",
		`{"login":"alice","password":"secret"}`, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("login without token status = %d", rec.Code)
	}
}

func itoaInt(v int64) string {
	return strconv.FormatInt(v, 10)
}

func TestUserWritesEventsAndContacts(t *testing.T) {
	h, db, user := newUserAPI(t)
	cookie := loginUser(t, h)

	// Resolve the default calendar.
	rec := userDo(h, http.MethodGet, "/api/user/calendars", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("calendars status = %d: %s", rec.Code, rec.Body.String())
	}
	var calendars struct {
		Calendars []struct {
			ID int64 `json:"id"`
		} `json:"calendars"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &calendars); err != nil {
		t.Fatal(err)
	}
	if len(calendars.Calendars) == 0 {
		t.Fatal("account has no calendar")
	}
	calendarID := calendars.Calendars[0].ID
	base := "/api/user/calendars/" + strconv.FormatInt(calendarID, 10) + "/events"

	// Create a single event.
	rec = userDo(h, http.MethodPost, base,
		`{"subject":"写周报","location":"线上","start":1800000000,"end":1800003600,"reminder":15}`,
		cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create event status = %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Event struct {
			ID      int64  `json:"id"`
			Subject string `json:"subject"`
			UID     string `json:"uid"`
		} `json:"event"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Event.Subject != "写周报" || created.Event.UID == "" {
		t.Fatalf("created event = %+v", created.Event)
	}
	eventID := created.Event.ID

	// Blank subject and inverted times are rejected.
	for _, body := range []string{
		`{"subject":"  ","start":1800000000,"end":1800003600}`,
		`{"subject":"倒序","start":1800003600,"end":1800000000}`,
	} {
		rec = userDo(h, http.MethodPost, base, body, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid event %s status = %d", body, rec.Code)
		}
	}

	// The event shows up in the window and can be edited.
	rec = userDo(h, http.MethodGet,
		base+"?from=1799990000&to=1800010000", "", cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"subject":"写周报"`) {
		t.Fatalf("event list = %d %s", rec.Code, rec.Body.String())
	}
	rec = userDo(h, http.MethodPatch,
		base+"/"+strconv.FormatInt(eventID, 10),
		`{"subject":"写周报（改）","location":"会议室"}`, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"subject":"写周报（改）"`) {
		t.Fatalf("update event = %d %s", rec.Code, rec.Body.String())
	}

	changes, err := db.ChangesSince(calendarID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !hasChange(changes, "add", "event", eventID) || !hasChange(changes, "change", "event", eventID) {
		t.Fatalf("event changes = %+v", changes)
	}

	// Contacts: create, search, edit, delete.
	rec = userDo(h, http.MethodPost, "/api/user/contacts",
		`{"firstName":"三","lastName":"张","company":"示例科技","email1":"zhang@example.com",
		  "mobilePhone":"13800000001","home2Phone":"028-66665555","homeCity":"北京","body":"老同事"}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create contact status = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"home2Phone":"028-66665555"`) {
		t.Fatalf("home2Phone missing from %s", rec.Body.String())
	}
	var createdContact struct {
		Contact struct {
			ID      int64  `json:"id"`
			Display string `json:"display"`
			FileAs  string `json:"fileAs"`
		} `json:"contact"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &createdContact); err != nil {
		t.Fatal(err)
	}
	if createdContact.Contact.FileAs != "张三" {
		t.Fatalf("created contact = %+v", createdContact.Contact)
	}
	contactID := createdContact.Contact.ID

	// An address book entry with no identity is rejected.
	rec = userDo(h, http.MethodPost, "/api/user/contacts", `{"company":""}`, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty contact status = %d", rec.Code)
	}
	// A malformed address is rejected too.
	rec = userDo(h, http.MethodPost, "/api/user/contacts", `{"fileAs":"坏地址","email1":"nope"}`, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad email status = %d", rec.Code)
	}

	rec = userDo(h, http.MethodGet, "/api/user/contacts?q=%E7%A4%BA%E4%BE%8B", "", cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"display":"张三"`) {
		t.Fatalf("contact search = %d %s", rec.Code, rec.Body.String())
	}
	rec = userDo(h, http.MethodPatch, "/api/user/contacts/"+strconv.FormatInt(contactID, 10),
		`{"company":"新公司","email2":"zhang.san@other.test"}`, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"company":"新公司"`) {
		t.Fatalf("update contact = %d %s", rec.Code, rec.Body.String())
	}

	contactsFolder, err := db.FolderByType(user.ID, 9)
	if err != nil {
		t.Fatal(err)
	}
	changes, err = db.ChangesSince(contactsFolder.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !hasChange(changes, "add", "contact", contactID) ||
		!hasChange(changes, "change", "contact", contactID) {
		t.Fatalf("contact changes = %+v", changes)
	}

	// Deletion is soft and recorded for clients.
	rec = userDo(h, http.MethodDelete,
		"/api/user/calendars/"+strconv.FormatInt(calendarID, 10)+"/events/"+strconv.FormatInt(eventID, 10),
		"", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete event status = %d: %s", rec.Code, rec.Body.String())
	}
	rec = userDo(h, http.MethodDelete, "/api/user/contacts/"+strconv.FormatInt(contactID, 10), "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete contact status = %d: %s", rec.Code, rec.Body.String())
	}
	rec = userDo(h, http.MethodGet, "/api/user/contacts?q=%E5%BC%A0", "", cookie)
	if strings.Contains(rec.Body.String(), `"id":`+strconv.FormatInt(contactID, 10)) {
		t.Fatalf("deleted contact still listed: %s", rec.Body.String())
	}
	event, err := db.EventByID(eventID)
	if err != nil || !event.Deleted {
		t.Fatalf("event not soft-deleted: %+v %v", event, err)
	}

	// Nobody else can touch the account's calendar.
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if _, err := db.CreateUser("bob", "bob@example.com", "Bob", string(hash)); err != nil {
		t.Fatal(err)
	}
	rec = userDo(h, http.MethodPost, "/api/user/session",
		`{"login":"bob","password":"secret"}`, nil)
	var bobCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == userCookieName {
			bobCookie = c
		}
	}
	rec = userDo(h, http.MethodPost, base,
		`{"subject":"偷写","start":1800000000,"end":1800003600}`, bobCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account event status = %d", rec.Code)
	}
}

func hasChange(changes []store.Change, kind, itemType string, id int64) bool {
	for _, c := range changes {
		if c.Kind == kind && c.ItemType == itemType && c.ItemID == id {
			return true
		}
	}
	return false
}

func TestUserReply(t *testing.T) {
	h, db, alice := newUserAPI(t)
	cookie := loginUser(t, h)

	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := db.CreateUser("bob", "bob@example.com", "Bob", string(hash))
	if err != nil {
		t.Fatal(err)
	}
	carol, err := db.CreateUser("carol", "carol@example.com", "Carol", string(hash))
	if err != nil {
		t.Fatal(err)
	}

	raw := []byte("From: Bob <bob@example.com>\r\n" +
		"To: Alice <alice@example.com>, Carol <carol@example.com>\r\n" +
		"Subject: 项目排期\r\n" +
		"Date: " + time.Now().Format(time.RFC1123Z) + "\r\n" +
		"Message-ID: <orig@example.com>\r\n" +
		"References: <root@example.com>\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\nhello\r\n")
	parsed, err := mailparse.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	originalID, err := deliver.ToUser(db, alice, deliver.FolderInbox, parsed, raw)
	if err != nil {
		t.Fatal(err)
	}

	// Reply All keeps the other participants.
	rec := userDo(h, http.MethodPost, "/api/user/mail/reply",
		`{"messageId":`+strconv.FormatInt(originalID, 10)+
			`,"body":"收到，明天答复","replyAll":true}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("reply status = %d: %s", rec.Code, rec.Body.String())
	}

	for _, account := range []*store.User{bob, carol} {
		inbox, err := db.FolderByType(account.ID, 2)
		if err != nil {
			t.Fatal(err)
		}
		messages, err := db.MessagesByFolder(inbox.ID, 10)
		if err != nil || len(messages) != 1 {
			t.Fatalf("%s inbox = %d messages (%v), want 1", account.Name, len(messages), err)
		}
		reply := messages[0]
		if reply.Subject != "Re: 项目排期" {
			t.Fatalf("%s subject = %q", account.Name, reply.Subject)
		}
		if !strings.EqualFold(reply.FromAddr, alice.Address) {
			t.Fatalf("%s reply from = %q", account.Name, reply.FromAddr)
		}
	}

	// The sender keeps a copy in Sent, threaded with the original.
	sentFolder, err := db.FolderByType(alice.ID, 5)
	if err != nil {
		t.Fatal(err)
	}
	sentMessages, err := db.MessagesByFolder(sentFolder.ID, 10)
	if err != nil || len(sentMessages) != 1 {
		t.Fatalf("sent = %d messages (%v), want 1", len(sentMessages), err)
	}
	if sentMessages[0].ConversationID != "ref:root@example.com" {
		t.Fatalf("reply conversation = %q", sentMessages[0].ConversationID)
	}

	// The inbox conversation now contains the reply too.
	rec = userDo(h, http.MethodGet, "/api/user/mail/conversations", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("conversations status = %d", rec.Code)
	}
	var listing struct {
		Conversations []struct {
			Key    string `json:"key"`
			Count  int    `json:"count"`
			Unread int    `json:"unread"`
		} `json:"conversations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Conversations) != 1 {
		t.Fatalf("conversations = %+v", listing.Conversations)
	}
	if listing.Conversations[0].Count != 2 || listing.Conversations[0].Unread != 1 {
		t.Fatalf("conversation = %+v", listing.Conversations[0])
	}

	rec = userDo(h, http.MethodGet,
		"/api/user/mail/thread?key="+listing.Conversations[0].Key, "", cookie)
	if rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), "hello") ||
		!strings.Contains(rec.Body.String(), "收到，明天答复") {
		t.Fatalf("thread = %d %s", rec.Code, rec.Body.String())
	}

	// Somebody else cannot reply to Alice's mail.
	bobCookieRec := userDo(h, http.MethodPost, "/api/user/session",
		`{"login":"bob","password":"secret"}`, nil)
	var bobCookie *http.Cookie
	for _, c := range bobCookieRec.Result().Cookies() {
		if c.Name == userCookieName {
			bobCookie = c
		}
	}
	rec = userDo(h, http.MethodPost, "/api/user/mail/reply",
		`{"messageId":`+strconv.FormatInt(originalID, 10)+`,"body":"x"}`, bobCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account reply status = %d", rec.Code)
	}
}

func TestBareAddress(t *testing.T) {
	cases := map[string]string{
		"Bob <bob@example.com>":     "bob@example.com",
		"<carol@example.com>":       "carol@example.com",
		"dave@example.com":          "dave@example.com",
		"  张三 <zhang@example.com> ": "zhang@example.com",
		"":                          "",
		"   ":                       "",
	}
	for input, want := range cases {
		if got := bareAddress(input); got != want {
			t.Fatalf("bareAddress(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestUserSearchMail(t *testing.T) {
	h, db, alice := newUserAPI(t)
	cookie := loginUser(t, h)

	inbox, err := db.FolderByType(alice.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	sent, err := db.FolderByType(alice.ID, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []*store.Message{
		{
			UserID: alice.ID, FolderID: inbox.ID, Subject: "合同附件",
			FromAddr: "bob@example.com", FromName: "Bob", ToAddr: alice.Address,
			ConversationID: "ref:c@x", DateReceived: 100,
			BodyText: "附件里是最终版合同。",
		},
		{
			UserID: alice.ID, FolderID: sent.ID, Subject: "Re: 合同附件",
			FromAddr: alice.Address, ToAddr: "bob@example.com",
			ConversationID: "ref:c@x", DateReceived: 200, IsRead: true,
			BodyText: "已确认，谢谢。",
		},
	} {
		if _, err := db.InsertMessage(m); err != nil {
			t.Fatal(err)
		}
	}

	rec := userDo(h, http.MethodGet, "/api/user/mail/search?q=%E5%90%88%E5%90%8C", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("search status = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Query string `json:"query"`
		Total int    `json:"total"`
		Hits  []struct {
			ID         int64  `json:"id"`
			FolderID   int64  `json:"folderId"`
			FolderName string `json:"folderName"`
			Key        string `json:"key"`
			Subject    string `json:"subject"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Total != 2 || len(out.Hits) != 2 {
		t.Fatalf("search hits = %+v", out)
	}
	if out.Hits[0].FolderName == "" || out.Hits[0].Key == "" {
		t.Fatalf("hit is missing folder or key: %+v", out.Hits[0])
	}

	// An empty query is a no-op rather than an error.
	rec = userDo(h, http.MethodGet, "/api/user/mail/search?q=", "", cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total":0`) {
		t.Fatalf("empty search = %d %s", rec.Code, rec.Body.String())
	}

	// Someone else's folder id is rejected.
	other, err := db.CreateUser("bob", "bob@example.com", "Bob", "hash")
	if err != nil {
		t.Fatal(err)
	}
	otherInbox, err := db.FolderByType(other.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	rec = userDo(h, http.MethodGet,
		"/api/user/mail/search?q=x&folder="+strconv.FormatInt(otherInbox.ID, 10), "", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign folder status = %d", rec.Code)
	}
}
