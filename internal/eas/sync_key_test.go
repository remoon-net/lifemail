package eas

import (
	"testing"

	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/wbxml"
)

func syncRequest(key string, collectionID int64) *wbxml.Node {
	return wbxml.N(pageAirSync, "Sync",
		wbxml.N(pageAirSync, "Collections",
			wbxml.N(pageAirSync, "Collection",
				wbxml.T(pageAirSync, "SyncKey", key),
				wbxml.T(pageAirSync, "CollectionId", i64toa(collectionID)),
				wbxml.T(pageAirSync, "DeletesAsMoves", "1"),
			)))
}

func postSync(t *testing.T, srv *Server, key string, folderID int64) *wbxml.Node {
	t.Helper()
	doc := postDoc(t, srv, "Sync", syncRequest(key, folderID))
	coll := doc.Path("Collections", "Collection")
	if coll == nil {
		t.Fatalf("no collection in response: %+v", doc)
	}
	if got := coll.ChildText("Status"); got != "1" {
		t.Fatalf("sync status = %q", got)
	}
	return coll
}

func TestSyncKeyStableWithoutChanges(t *testing.T) {
	srv, _, inbox := testServer(t)

	// First sync initialises the collection.
	first := postSync(t, srv, "0", inbox.ID)
	key := first.ChildText("SyncKey")
	if key == "" || key == "0" {
		t.Fatalf("initial sync key = %q", key)
	}

	// Repeating the sync must not advance the key, otherwise clients keep
	// re-syncing the folder forever.
	for i := 0; i < 3; i++ {
		coll := postSync(t, srv, key, inbox.ID)
		if got := coll.ChildText("SyncKey"); got != key {
			t.Fatalf("round %d: sync key moved %q -> %q without changes", i+1, key, got)
		}
		if cmds := coll.Child("Commands"); cmds != nil {
			t.Fatalf("round %d: unexpected commands", i+1)
		}
	}
}

func TestSyncKeyAdvancesWithChanges(t *testing.T) {
	srv, user, inbox := testServer(t)
	key := postSync(t, srv, "0", inbox.ID).ChildText("SyncKey")

	msgID, err := srv.Store.InsertMessage(&store.Message{
		UserID:   user.ID,
		FolderID: inbox.ID,
		Subject:  "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.AddChange(inbox.ID, "add", "email", msgID); err != nil {
		t.Fatal(err)
	}

	coll := postSync(t, srv, key, inbox.ID)
	if got := coll.ChildText("SyncKey"); got == key {
		t.Fatalf("sync key did not advance after a change (%q)", got)
	}
	adds := coll.Path("Commands").ChildrenNamed("Add")
	if len(adds) != 1 {
		t.Fatalf("adds = %d, want 1", len(adds))
	}

	// And it settles again once the change is consumed.
	next := coll.ChildText("SyncKey")
	settled := postSync(t, srv, next, inbox.ID)
	if got := settled.ChildText("SyncKey"); got != next {
		t.Fatalf("sync key kept moving: %q -> %q", next, got)
	}
}
