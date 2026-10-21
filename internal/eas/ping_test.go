package eas

import (
	"testing"
	"time"

	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/wbxml"
)

func pingDocument(heartbeat string, folderIDs ...int64) *wbxml.Node {
	folders := wbxml.N(pagePing, "Folders")
	for _, id := range folderIDs {
		folders.Children = append(folders.Children,
			wbxml.N(pagePing, "Folder",
				wbxml.T(pagePing, "ServerId", i64toa(id)),
				wbxml.T(pagePing, "Class", "Email")))
	}
	return wbxml.N(pagePing, "Ping",
		wbxml.T(pagePing, "HeartbeatInterval", heartbeat),
		folders)
}

func TestPingReportsNoChangesForUnsyncedFolders(t *testing.T) {
	srv, user, inbox := testServer(t)
	drafts, err := srv.Store.FolderByType(user.ID, 3)
	if err != nil {
		t.Fatal(err)
	}

	// The device never synced either folder, so there is nothing to announce.
	// Answering "changes" here is what made MIUI Mail ping in a tight loop.
	doc := postDoc(t, srv, "Ping", pingDocument("1", inbox.ID, drafts.ID))
	if got := doc.ChildText("Status"); got != "1" {
		t.Fatalf("status = %q, want 1 (no changes)", got)
	}
}

func TestPingReportsChangesOnceSynced(t *testing.T) {
	srv, user, inbox := testServer(t)
	if err := srv.Store.SaveSyncState(&store.SyncState{
		UserID:   user.ID,
		DeviceID: "dev1",
		FolderID: inbox.ID,
		SyncKey:  "k1",
		Seq:      0,
	}); err != nil {
		t.Fatal(err)
	}
	msgID, err := srv.Store.InsertMessage(&store.Message{
		UserID:   user.ID,
		FolderID: inbox.ID,
		Subject:  "new mail",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.AddChange(inbox.ID, "add", "email", msgID); err != nil {
		t.Fatal(err)
	}

	doc := postDoc(t, srv, "Ping", pingDocument("1", inbox.ID))
	if got := doc.ChildText("Status"); got != "2" {
		t.Fatalf("status = %q, want 2", got)
	}
	// The response carries only the status; a folder list here confuses MIUI.
	if child := doc.Child("Folders"); child != nil {
		t.Fatalf("Ping response must not echo folders: %+v", child)
	}
}

func TestPingThrottlesRepeatedNotifications(t *testing.T) {
	old := pingNotifyInterval
	pingNotifyInterval = time.Hour
	defer func() { pingNotifyInterval = old }()

	srv, user, inbox := testServer(t)
	if err := srv.Store.SaveSyncState(&store.SyncState{
		UserID:   user.ID,
		DeviceID: "dev1",
		FolderID: inbox.ID,
		SyncKey:  "k1",
		Seq:      0,
	}); err != nil {
		t.Fatal(err)
	}
	msgID, err := srv.Store.InsertMessage(&store.Message{
		UserID:   user.ID,
		FolderID: inbox.ID,
		Subject:  "new mail",
	})
	if err != nil {
		t.Fatal(err)
	}
	change, err := srv.Store.AddChange(inbox.ID, "add", "email", msgID)
	if err != nil {
		t.Fatal(err)
	}

	// The first Ping reports the change.
	if got := postDoc(t, srv, "Ping", pingDocument("1", inbox.ID)).ChildText("Status"); got != "2" {
		t.Fatalf("first ping status = %q, want 2", got)
	}
	// A client that keeps pinging without syncing must not turn this into a
	// busy loop: the same change is muted for the interval.
	if got := postDoc(t, srv, "Ping", pingDocument("1", inbox.ID)).ChildText("Status"); got != "1" {
		t.Fatalf("second ping status = %q, want 1 (muted)", got)
	}

	// Once the device consumes the change and a new one arrives, Ping speaks
	// up again.
	if err := srv.Store.SaveSyncState(&store.SyncState{
		UserID:   user.ID,
		DeviceID: "dev1",
		FolderID: inbox.ID,
		SyncKey:  "k2",
		Seq:      change,
	}); err != nil {
		t.Fatal(err)
	}
	nextID, err := srv.Store.InsertMessage(&store.Message{
		UserID:   user.ID,
		FolderID: inbox.ID,
		Subject:  "another",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.AddChange(inbox.ID, "add", "email", nextID); err != nil {
		t.Fatal(err)
	}
	if got := postDoc(t, srv, "Ping", pingDocument("1", inbox.ID)).ChildText("Status"); got != "2" {
		t.Fatalf("status after new change = %q, want 2", got)
	}
}

func TestPingQuietAfterChangesConsumed(t *testing.T) {
	srv, user, inbox := testServer(t)
	msgID, err := srv.Store.InsertMessage(&store.Message{
		UserID:   user.ID,
		FolderID: inbox.ID,
		Subject:  "new mail",
	})
	if err != nil {
		t.Fatal(err)
	}
	change, err := srv.Store.AddChange(inbox.ID, "add", "email", msgID)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Store.SaveSyncState(&store.SyncState{
		UserID:   user.ID,
		DeviceID: "dev1",
		FolderID: inbox.ID,
		SyncKey:  "k2",
		Seq:      change,
	}); err != nil {
		t.Fatal(err)
	}

	doc := postDoc(t, srv, "Ping", pingDocument("1", inbox.ID))
	if got := doc.ChildText("Status"); got != "1" {
		t.Fatalf("status = %q, want 1 after the change was consumed", got)
	}
}
