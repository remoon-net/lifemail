package eas

import (
	"testing"

	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/wbxml"
)

func postDoc(t *testing.T, srv *Server, cmd string, doc *wbxml.Node) *wbxml.Node {
	t.Helper()
	raw, err := wbxml.Encode(doc, codec)
	if err != nil {
		t.Fatal(err)
	}
	rec := post(t, srv, cmd, "dev1", raw)
	resp, err := wbxml.Decode(rec.Body.Bytes(), codec)
	if err != nil {
		t.Fatalf("%s decode: %v (%x)", cmd, err, rec.Body.Bytes())
	}
	return resp
}

func TestMeetingResponseUpdatesEvent(t *testing.T) {
	srv, user, _ := testServer(t)
	calendar, err := srv.Store.FolderByType(user.ID, 8)
	if err != nil {
		t.Fatal(err)
	}
	eventID, err := srv.Store.InsertEvent(&store.Event{
		UserID:        user.ID,
		FolderID:      calendar.ID,
		UID:           "uid-1",
		Subject:       "sync up",
		StartTime:     1700000000,
		EndTime:       1700003600,
		MeetingStatus: 5, // meeting received
	})
	if err != nil {
		t.Fatal(err)
	}

	resp := postDoc(t, srv, "MeetingResponse", wbxml.N(pageMeetingResponse, "MeetingResponse",
		wbxml.N(pageMeetingResponse, "Request",
			wbxml.T(pageMeetingResponse, "UserResponse", "2"),
			wbxml.T(pageMeetingResponse, "FolderId", i64toa(calendar.ID)),
			wbxml.T(pageMeetingResponse, "RequestId", i64toa(eventID)),
		),
	))

	results := resp.ChildrenNamed("Result")
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if got := results[0].ChildText("Status"); got != "1" {
		t.Fatalf("status = %q, want 1", got)
	}
	if got := results[0].ChildText("RequestId"); got != i64toa(eventID) {
		t.Fatalf("request id = %q", got)
	}

	event, err := srv.Store.EventByID(eventID)
	if err != nil {
		t.Fatal(err)
	}
	if event.MeetingStatus != 3 {
		t.Fatalf("meeting status = %d, want 3 (accepted)", event.MeetingStatus)
	}
	changes, err := srv.Store.ChangesSince(calendar.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %d, want 1", len(changes))
	}
}

func TestMeetingResponseUnknownRequest(t *testing.T) {
	srv, user, _ := testServer(t)
	calendar, err := srv.Store.FolderByType(user.ID, 8)
	if err != nil {
		t.Fatal(err)
	}
	resp := postDoc(t, srv, "MeetingResponse", wbxml.N(pageMeetingResponse, "MeetingResponse",
		wbxml.N(pageMeetingResponse, "Request",
			wbxml.T(pageMeetingResponse, "UserResponse", "2"),
			wbxml.T(pageMeetingResponse, "FolderId", i64toa(calendar.ID)),
			wbxml.T(pageMeetingResponse, "RequestId", "9999"),
		),
	))
	results := resp.ChildrenNamed("Result")
	if len(results) != 1 || results[0].ChildText("Status") != "2" {
		t.Fatalf("unexpected result: %+v", results)
	}
}

func TestResolveRecipients(t *testing.T) {
	srv, _, _ := testServer(t)
	if _, err := srv.Store.CreateUser("bob", "bob@example.com", "Bob", "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.CreateAlias("sales@example.com", userID(t, srv, "bob@example.com")); err != nil {
		t.Fatal(err)
	}

	resp := postDoc(t, srv, "ResolveRecipients", wbxml.N(pageResolveRecipients, "ResolveRecipients",
		wbxml.T(pageResolveRecipients, "To", "bob@example.com"),
		wbxml.T(pageResolveRecipients, "To", "sales@example.com"),
		wbxml.T(pageResolveRecipients, "To", "nobody@elsewhere.com"),
	))
	if got := resp.ChildText("Status"); got != "1" {
		t.Fatalf("status = %q, want 1", got)
	}

	byAddr := make(map[string]*wbxml.Node)
	for _, r := range resp.ChildrenNamed("Response") {
		byAddr[r.ChildText("To")] = r
	}
	if len(byAddr) != 3 {
		t.Fatalf("responses = %d, want 3", len(byAddr))
	}
	for _, addr := range []string{"bob@example.com", "sales@example.com"} {
		r := byAddr[addr]
		if r == nil {
			t.Fatalf("missing response for %q", addr)
		}
		if got := r.ChildText("RecipientCount"); got != "1" {
			t.Fatalf("%s recipient count = %q, want 1", addr, got)
		}
		if got := r.Child("Recipient").ChildText("EmailAddress"); got != "bob@example.com" {
			t.Fatalf("%s resolved to %q", addr, got)
		}
	}
	if got := byAddr["nobody@elsewhere.com"].ChildText("RecipientCount"); got != "0" {
		t.Fatalf("unknown recipient count = %q, want 0", got)
	}
}

func userID(t *testing.T, srv *Server, address string) string {
	t.Helper()
	u, err := srv.Store.UserByAddress(address)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func TestSearchMailboxFindsMessages(t *testing.T) {
	srv, user, inbox := testServer(t)
	matchID, err := srv.Store.InsertMessage(&store.Message{
		UserID:       user.ID,
		FolderID:     inbox.ID,
		Subject:      "Invoice for March",
		FromAddr:     "billing@example.com",
		ToAddr:       "alice@example.com",
		DateReceived: 1700000100,
		BodyText:     "please pay",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.InsertMessage(&store.Message{
		UserID:       user.ID,
		FolderID:     inbox.ID,
		Subject:      "lunch",
		FromAddr:     "bob@example.com",
		ToAddr:       "alice@example.com",
		DateReceived: 1700000200,
		BodyText:     "soup",
	}); err != nil {
		t.Fatal(err)
	}

	resp := postDoc(t, srv, "Search", wbxml.N(pageSearch, "Search",
		wbxml.N(pageSearch, "Store",
			wbxml.T(pageSearch, "Name", "Mailbox"),
			wbxml.N(pageSearch, "Query",
				wbxml.T(pageSearch, "FreeText", "invoice")),
			wbxml.N(pageSearch, "Options",
				wbxml.T(pageSearch, "Range", "0-9")),
		),
	))

	storeResp := resp.Path("Response", "Store")
	if storeResp == nil {
		t.Fatalf("no store response: %+v", resp)
	}
	if got := storeResp.ChildText("Status"); got != "1" {
		t.Fatalf("store status = %q", got)
	}
	if got := storeResp.ChildText("Total"); got != "1" {
		t.Fatalf("total = %q, want 1", got)
	}
	results := storeResp.ChildrenNamed("Result")
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	props := results[0].Child("Properties")
	if props == nil {
		t.Fatal("missing properties")
	}
	if got := props.ChildText("ServerId"); got != i64toa(matchID) {
		t.Fatalf("server id = %q, want %d", got, matchID)
	}
	if got := props.ChildText("Subject"); got != "Invoice for March" {
		t.Fatalf("subject = %q", got)
	}
}

func TestSearchGALFindsUsers(t *testing.T) {
	srv, _, _ := testServer(t)
	resp := postDoc(t, srv, "Search", wbxml.N(pageSearch, "Search",
		wbxml.N(pageSearch, "Store",
			wbxml.T(pageSearch, "Name", "GAL"),
			wbxml.T(pageSearch, "Query", "alice"),
		),
	))
	results := resp.Path("Response", "Store").ChildrenNamed("Result")
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if got := results[0].Child("Properties").ChildText("EmailAddress"); got != "alice@example.com" {
		t.Fatalf("gal email = %q", got)
	}
}
