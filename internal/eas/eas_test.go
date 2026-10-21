package eas

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"remoon.net/lifemail/internal/blobstore"
	"remoon.net/lifemail/internal/smtp"
	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/wbxml"
)

func testServer(t *testing.T) (*Server, *store.User, *store.Folder) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	blobs, err := blobstore.Open(filepath.Join(t.TempDir(), "blobs"))
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
	inbox, err := db.FolderByType(user.ID, 2)
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	srv := &Server{
		Store:  db,
		Sender: &smtp.Sender{},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return srv, user, inbox
}

func post(t *testing.T, srv *Server, cmd, device string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost,
		"/Microsoft-Server-ActiveSync?Cmd="+cmd+"&DeviceId="+device, bytes.NewReader(body))
	req.SetBasicAuth("alice@example.com", "secret")
	req.Header.Set("MS-ASProtocolVersion", "14.1")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d: %s", cmd, rec.Code, rec.Body.String())
	}
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) *wbxml.Node {
	t.Helper()
	doc, err := wbxml.Decode(rec.Body.Bytes(), codec)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return doc
}

func TestFolderSync(t *testing.T) {
	srv, _, _ := testServer(t)
	body, err := wbxml.Encode(wbxml.N(pageFolderHierarchy, "FolderSync",
		wbxml.T(pageFolderHierarchy, "SyncKey", "0")), codec)
	if err != nil {
		t.Fatal(err)
	}
	doc := decode(t, post(t, srv, "FolderSync", "dev1", body))
	if doc.Name != "FolderSync" {
		t.Fatalf("root = %q", doc.Name)
	}
	if got := doc.ChildText("Status"); got != "1" {
		t.Fatalf("status = %q", got)
	}
	adds := doc.Path("Changes").ChildrenNamed("Add")
	if len(adds) < 6 {
		t.Fatalf("expected default folders, got %d", len(adds))
	}
	if doc.ChildText("SyncKey") == "0" {
		t.Fatal("sync key must not be 0")
	}
}

func TestSyncDeliversNewMail(t *testing.T) {
	srv, user, inbox := testServer(t)

	id, err := srv.Store.InsertMessage(&store.Message{
		UserID:       user.ID,
		FolderID:     inbox.ID,
		Subject:      "hello",
		FromAddr:     "bob@example.com",
		FromName:     "Bob",
		ToAddr:       "alice@example.com",
		DateReceived: 1700000000,
		BodyText:     "hi there",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.AddChange(inbox.ID, "add", "email", id); err != nil {
		t.Fatal(err)
	}

	syncReq := wbxml.N(pageAirSync, "Sync",
		wbxml.N(pageAirSync, "Collections",
			wbxml.N(pageAirSync, "Collection",
				wbxml.T(pageAirSync, "SyncKey", "0"),
				wbxml.T(pageAirSync, "CollectionId", i64toa(inbox.ID)),
				wbxml.N(pageAirSync, "Options",
					wbxml.N(pageAirSyncBase, "BodyPreference",
						wbxml.T(pageAirSyncBase, "Type", "2"))))))
	body, err := wbxml.Encode(syncReq, codec)
	if err != nil {
		t.Fatal(err)
	}

	doc := decode(t, post(t, srv, "Sync", "dev1", body))
	coll := doc.Path("Collections", "Collection")
	if coll == nil {
		t.Fatal("no collection in response")
	}
	if got := coll.ChildText("Status"); got != "1" {
		t.Fatalf("collection status = %q", got)
	}
	adds := coll.Path("Commands").ChildrenNamed("Add")
	if len(adds) != 1 {
		t.Fatalf("expected one Add, got %d", len(adds))
	}
	if got := adds[0].Path("ApplicationData").ChildText("Subject"); got != "hello" {
		t.Fatalf("subject = %q", got)
	}
	key := coll.ChildText("SyncKey")
	if key == "" || key == "0" {
		t.Fatalf("bad sync key %q", key)
	}

	// A second sync with the returned key must not repeat the message.
	syncReq.Children[0].Children[0].Children[0].Text = key
	body, err = wbxml.Encode(syncReq, codec)
	if err != nil {
		t.Fatal(err)
	}
	doc = decode(t, post(t, srv, "Sync", "dev1", body))
	coll = doc.Path("Collections", "Collection")
	if got := coll.ChildText("Status"); got != "1" {
		t.Fatalf("second sync status = %q", got)
	}
	if cmds := coll.Child("Commands"); cmds != nil {
		if adds := cmds.ChildrenNamed("Add"); len(adds) != 0 {
			t.Fatalf("message delivered twice: %d adds", len(adds))
		}
	}
}
