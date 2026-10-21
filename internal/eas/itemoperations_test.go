package eas

import (
	"testing"

	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/wbxml"
)

// Emptying a folder must actually delete its messages and report success.
func TestItemOperationsEmptyFolderContents(t *testing.T) {
	srv, user, inbox := testServer(t)
	var ids []int64
	for i := 0; i < 3; i++ {
		id, err := srv.Store.InsertMessage(&store.Message{
			UserID:            user.ID,
			FolderID:          inbox.ID,
			Subject:           "spam",
			ToAddr:            user.Address,
			DateReceived:      int64(1792558000 + i),
			Size:              10,
			Importance:        1,
			MessageClass:      "IPM.Note",
			InternetMessageID: "<empty-" + i64toa(int64(i)) + "@example.com>",
		})
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		ids = append(ids, id)
	}

	req := wbxml.N(pageItemOperations, "ItemOperations",
		wbxml.N(pageItemOperations, "EmptyFolderContents",
			wbxml.T(pageAirSync, "CollectionId", i64toa(inbox.ID))))
	resp, err := srv.cmdItemOperations(user, req)
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if got := resp.ChildText("Status"); got != "1" {
		t.Fatalf("top status = %q, want 1", got)
	}
	res := resp.Path("Response", "EmptyFolderContents")
	if res == nil {
		t.Fatalf("no EmptyFolderContents in response")
	}
	if got := res.ChildText("Status"); got != "1" {
		t.Fatalf("status = %q, want 1", got)
	}
	left, err := srv.Store.MessagesByFolder(inbox.ID, 0)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(left) != 0 {
		t.Fatalf("%d messages survived the purge", len(left))
	}
	for _, id := range ids {
		m, err := srv.Store.MessageByID(id)
		if err != nil {
			continue // deleted rows may disappear
		}
		if !m.Deleted {
			t.Fatalf("message %d is still live", id)
		}
	}
}

// A request we cannot honour must not report success.
func TestItemOperationsUnknownRequestIsAnError(t *testing.T) {
	srv, user, _ := testServer(t)
	resp, err := srv.cmdItemOperations(user, wbxml.N(pageItemOperations, "ItemOperations"))
	if err != nil {
		t.Fatalf("itemoperations: %v", err)
	}
	if got := resp.ChildText("Status"); got != "2" {
		t.Fatalf("status = %q, want 2", got)
	}
}

// Another account's folder must not be emptied.
func TestItemOperationsEmptyFolderRejectsOtherAccount(t *testing.T) {
	srv, user, inbox := testServer(t)
	other, err := srv.Store.CreateUser("bob", "bob@example.com", "Bob", "x")
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}
	_, err = srv.Store.InsertMessage(&store.Message{
		UserID:            user.ID,
		FolderID:          inbox.ID,
		Subject:           "keep me",
		ToAddr:            user.Address,
		DateReceived:      1792558000,
		Size:              10,
		Importance:        1,
		MessageClass:      "IPM.Note",
		InternetMessageID: "<keep@example.com>",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	req := wbxml.N(pageItemOperations, "ItemOperations",
		wbxml.N(pageItemOperations, "EmptyFolderContents",
			wbxml.T(pageAirSync, "CollectionId", i64toa(inbox.ID))))
	resp, err := srv.cmdItemOperations(other, req)
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if got := resp.Path("Response", "EmptyFolderContents").ChildText("Status"); got != "3" {
		t.Fatalf("status = %q, want 3", got)
	}
	left, _ := srv.Store.MessagesByFolder(inbox.ID, 0)
	if len(left) != 1 {
		t.Fatalf("another account's purge removed %d messages", 1-len(left))
	}
}
