package eas

import (
	"testing"

	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/wbxml"
)

func insertMessage(t *testing.T, srv *Server, user *store.User, folderID int64) int64 {
	t.Helper()
	id, err := srv.Store.InsertMessage(&store.Message{
		UserID:   user.ID,
		FolderID: folderID,
		Subject:  "spam",
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func moveDocument(msgID, srcFolder, dstFolder int64) *wbxml.Node {
	move := wbxml.N(pageMove, "Move",
		wbxml.T(pageMove, "SrcMsgId", i64toa(msgID)),
		wbxml.T(pageMove, "SrcFldId", i64toa(srcFolder)))
	if dstFolder != 0 {
		move.Children = append(move.Children, wbxml.T(pageMove, "DstFldId", i64toa(dstFolder)))
	}
	return wbxml.N(pageMove, "MoveItems", move)
}

func TestJunkFolderIsPreset(t *testing.T) {
	srv, user, _ := testServer(t)
	junk, err := srv.Store.FolderByType(user.ID, 12)
	if err != nil {
		t.Fatalf("Spam must be a preset folder: %v", err)
	}
	if junk.Name != "Spam" {
		t.Fatalf("junk folder name = %q", junk.Name)
	}

	folders, err := srv.Store.FoldersByUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 8 {
		t.Fatalf("preset folders = %d, want 8", len(folders))
	}

	doc := postDoc(t, srv, "FolderSync", wbxml.N(pageFolderHierarchy, "FolderSync",
		wbxml.T(pageFolderHierarchy, "SyncKey", "0")))
	advertised := false
	for _, add := range doc.Path("Changes").ChildrenNamed("Add") {
		if add.ChildText("Type") == "12" && add.ChildText("DisplayName") == "Spam" {
			advertised = true
		}
	}
	if !advertised {
		t.Fatal("FolderSync does not advertise the preset Junk folder")
	}
}

func TestMoveToJunkFolder(t *testing.T) {
	srv, user, inbox := testServer(t)
	junk, err := srv.Store.FolderByType(user.ID, 12)
	if err != nil {
		t.Fatal(err)
	}
	msgID := insertMessage(t, srv, user, inbox.ID)

	doc := postDoc(t, srv, "MoveItems", moveDocument(msgID, inbox.ID, junk.ID))
	if got := doc.Child("Response").ChildText("Status"); got != "3" {
		t.Fatalf("status = %q, want 3: %+v", got, doc)
	}
	msg, err := srv.Store.MessageByID(msgID)
	if err != nil {
		t.Fatal(err)
	}
	if msg.FolderID != junk.ID {
		t.Fatalf("message folder = %d, want %d", msg.FolderID, junk.ID)
	}
	if changes, _ := srv.Store.ChangesSince(junk.ID, 0, 10); len(changes) != 1 {
		t.Fatalf("junk changes = %d, want 1", len(changes))
	}
}

func TestMoveWithoutDestinationUsesJunk(t *testing.T) {
	srv, user, inbox := testServer(t)
	junk, err := srv.Store.FolderByType(user.ID, 12)
	if err != nil {
		t.Fatal(err)
	}
	msgID := insertMessage(t, srv, user, inbox.ID)

	// MIUI Mail sends no DstFldId for its built-in "move to spam" action.
	doc := postDoc(t, srv, "MoveItems", moveDocument(msgID, inbox.ID, 0))
	if got := doc.Child("Response").ChildText("Status"); got != "3" {
		t.Fatalf("status = %q, want 3: %+v", got, doc)
	}
	msg, err := srv.Store.MessageByID(msgID)
	if err != nil {
		t.Fatal(err)
	}
	if msg.FolderID != junk.ID {
		t.Fatalf("message folder = %d, want junk %d", msg.FolderID, junk.ID)
	}
}

func TestMoveWithoutDestinationKeepsMailWhenSourceIsStale(t *testing.T) {
	srv, user, inbox := testServer(t)
	foldersBefore, err := srv.Store.FoldersByUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}

	// The client refers to a message the server no longer has.
	doc := postDoc(t, srv, "MoveItems", moveDocument(999999, inbox.ID, 0))
	if got := doc.Child("Response").ChildText("Status"); got != "1" {
		t.Fatalf("status = %q, want 1", got)
	}
	foldersAfter, err := srv.Store.FoldersByUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(foldersAfter) != len(foldersBefore) {
		t.Fatalf("a failed move must not create folders (%d -> %d)",
			len(foldersBefore), len(foldersAfter))
	}
}

func TestMoveToUnknownFolderFails(t *testing.T) {
	srv, user, inbox := testServer(t)
	msgID := insertMessage(t, srv, user, inbox.ID)
	doc := postDoc(t, srv, "MoveItems", moveDocument(msgID, inbox.ID, 999999))
	if got := doc.Child("Response").ChildText("Status"); got != "2" {
		t.Fatalf("status = %q, want 2", got)
	}
	msg, err := srv.Store.MessageByID(msgID)
	if err != nil {
		t.Fatal(err)
	}
	if msg.FolderID != inbox.ID {
		t.Fatal("message must stay in the inbox")
	}
}
