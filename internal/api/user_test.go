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

	"golang.org/x/crypto/bcrypt"

	"remoon.net/lifemail/internal/blobstore"
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
		Store:   db,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Token:   "admin-token",
		BaseURL: "https://mail.example.com",
		Version: "test",
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
