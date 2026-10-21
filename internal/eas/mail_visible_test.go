package eas

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"remoon.net/lifemail/internal/deliver"
	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/wbxml"
)

// postAsUser sends a command authenticating as the given account.
func postAsUser(t *testing.T, srv *Server, user *store.User, cmd string, doc *wbxml.Node) *wbxml.Node {
	t.Helper()
	raw, err := wbxml.Encode(doc, codec)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost,
		"/Microsoft-Server-ActiveSync?Cmd="+cmd+"&DeviceId=dev1", bytes.NewReader(raw))
	req.SetBasicAuth(user.Name, "secret")
	req.Header.Set("MS-ASProtocolVersion", "14.1")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d: %s", cmd, rec.Code, rec.Body.String())
	}
	resp, err := wbxml.Decode(rec.Body.Bytes(), codec)
	if err != nil {
		t.Fatalf("%s decode: %v", cmd, err)
	}
	return resp
}

func syncAsUser(t *testing.T, srv *Server, user *store.User, key string, folderID int64, options *wbxml.Node) *wbxml.Node {
	t.Helper()
	req := syncRequest(key, folderID)
	if options != nil {
		if collection := req.Child("Collections").Child("Collection"); collection != nil {
			collection.Children = append(collection.Children, options)
		}
	}
	doc := postAsUser(t, srv, user, "Sync", req)
	coll := doc.Path("Collections", "Collection")
	if coll == nil {
		t.Fatalf("no collection in response: %+v", doc)
	}
	if got := coll.ChildText("Status"); got != "1" {
		t.Fatalf("sync status = %q", got)
	}
	return coll
}

// postSendMail sends a ComposeMail document the way clients do with
// Cmd=SendMail. EAS 14 answers with an empty body, so the response is not
// decoded.
func postSendMail(t *testing.T, srv *Server, user *store.User, sendMail *wbxml.Node) {
	t.Helper()
	raw, err := wbxml.Encode(sendMail, codec)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost,
		"/Microsoft-Server-ActiveSync?Cmd=SendMail&DeviceId=dev1", bytes.NewReader(raw))
	req.SetBasicAuth(user.Name, "secret")
	req.Header.Set("MS-ASProtocolVersion", "14.1")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("SendMail: status %d: %s", rec.Code, rec.Body.String())
	}
}

// MIUI sends FilterType 3 (one week) on its first sync. The initial filter
// must not drop a message that arrived a moment before the sync, otherwise the
// very first mail of a new account is lost.
func TestFirstMailSurvivesInitialFilter(t *testing.T) {
	srv, user, inbox := testServer(t)
	options := wbxml.N(pageAirSync, "Options",
		wbxml.T(pageAirSync, "FilterType", "3"))
	key := syncAsUser(t, srv, user, "0", inbox.ID, options).ChildText("SyncKey")

	id, err := srv.Store.InsertMessage(&store.Message{
		UserID:       user.ID,
		FolderID:     inbox.ID,
		Subject:      "first mail",
		FromAddr:     "alice@example.com",
		DateReceived: time.Now().Add(-time.Second).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.AddChange(inbox.ID, "add", "email", id); err != nil {
		t.Fatal(err)
	}

	coll := syncAsUser(t, srv, user, key, inbox.ID, options)
	adds := coll.Path("Commands").ChildrenNamed("Add")
	if len(adds) != 1 {
		t.Fatalf("adds = %d, want 1 (key %q)", len(adds), coll.ChildText("SyncKey"))
	}
	if got := adds[0].ChildText("ServerId"); got != i64toa(id) {
		t.Fatalf("server id = %q, want %d", got, id)
	}
}

func TestFilterWindowCoversEveryFilterType(t *testing.T) {
	for ft := 1; ft <= 8; ft++ {
		if got := filterWindow(ft); got <= 0 {
			t.Fatalf("filterWindow(%d) = %v, want a positive window", ft, got)
		}
	}
	if filterWindow(3) != 7*24*time.Hour {
		t.Fatalf("filterWindow(3) = %v, want one week", filterWindow(3))
	}
	if filterWindow(8) < 365*24*time.Hour {
		t.Fatalf("filterWindow(8) = %v, want at least a year", filterWindow(8))
	}
}

// A brand new account syncs its (empty) folders, then sends a message to
// itself. The Inbox and Sent copies must show up on the next Sync; dropping
// them while still advancing the SyncKey loses the mail for good.
func TestFirstSentMailAppearsInSync(t *testing.T) {
	srv, alice, _ := testServer(t)
	bob, err := srv.Store.CreateUser("bob", "bob@example.com", "Bob", alice.PasswordHash)
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := srv.Store.FolderByType(bob.ID, deliver.FolderInbox)
	if err != nil {
		t.Fatal(err)
	}
	sent, err := srv.Store.FolderByType(bob.ID, deliver.FolderSent)
	if err != nil {
		t.Fatal(err)
	}
	// MIUI asks for a bounded window and a body preference on every sync.
	options := wbxml.N(pageAirSync, "Options",
		wbxml.T(pageAirSync, "FilterType", "4"),
		wbxml.N(pageAirSyncBase, "BodyPreference",
			wbxml.T(pageAirSyncBase, "Type", "2"),
			wbxml.T(pageAirSyncBase, "Truncation", "1024")))
	inboxKey := syncAsUser(t, srv, bob, "0", inbox.ID, options).ChildText("SyncKey")
	sentKey := syncAsUser(t, srv, bob, "0", sent.ID, options).ChildText("SyncKey")

	mime := "From: Bob <bob@example.com>\r\n" +
		"To: Bob <bob@example.com>\r\n" +
		"Subject: to myself\r\n" +
		"Message-Id: <self-1@example.com>\r\n" +
		"Date: Tue, 21 Oct 2026 08:18:13 +0800\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"hello me\r\n"
	body := base64.StdEncoding.EncodeToString([]byte(mime))
	postSendMail(t, srv, bob, wbxml.N(pageComposeMail, "SendMail",
		wbxml.T(pageComposeMail, "Mime", body)))

	inboxColl := syncAsUser(t, srv, bob, inboxKey, inbox.ID, options)
	if adds := inboxColl.Path("Commands").ChildrenNamed("Add"); len(adds) != 1 {
		t.Fatalf("inbox adds = %d, want 1 (key %q)", len(adds), inboxColl.ChildText("SyncKey"))
	}
	sentColl := syncAsUser(t, srv, bob, sentKey, sent.ID, options)
	if adds := sentColl.Path("Commands").ChildrenNamed("Add"); len(adds) != 1 {
		t.Fatalf("sent adds = %d, want 1 (key %q)", len(adds), sentColl.ChildText("SyncKey"))
	}
}
