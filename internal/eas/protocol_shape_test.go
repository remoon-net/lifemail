package eas

import (
	"bytes"
	"net/http/httptest"
	"testing"

	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/wbxml"
)

// The GetItemEstimate response schema allows one Collection per Response, so a
// request for several folders has to produce several Response elements.
func TestGetItemEstimateOneResponsePerCollection(t *testing.T) {
	srv, user, inbox := testServer(t)
	calendar, err := srv.Store.FolderByType(user.ID, 8)
	if err != nil {
		t.Fatalf("calendar folder: %v", err)
	}

	req := wbxml.N(pageGetItemEstimate, "GetItemEstimate",
		wbxml.N(pageGetItemEstimate, "Collections",
			wbxml.N(pageGetItemEstimate, "Collection",
				wbxml.T(pageGetItemEstimate, "Class", "Email"),
				wbxml.T(pageGetItemEstimate, "CollectionId", i64toa(inbox.ID))),
			wbxml.N(pageGetItemEstimate, "Collection",
				wbxml.T(pageGetItemEstimate, "Class", "Calendar"),
				wbxml.T(pageGetItemEstimate, "CollectionId", i64toa(calendar.ID)))))

	resp, err := srv.cmdGetItemEstimate(user, "device1", req)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if len(resp.Children) != 2 {
		t.Fatalf("got %d Response elements, want 2", len(resp.Children))
	}
	for i, child := range resp.Children {
		if child.Name != "Response" {
			t.Fatalf("child %d is %q, want Response", i, child.Name)
		}
		collections := child.ChildrenNamed("Collection")
		if len(collections) != 1 {
			t.Fatalf("response %d has %d Collections, want 1", i, len(collections))
		}
		if child.ChildText("Status") != "1" {
			t.Fatalf("response %d Status = %q", i, child.ChildText("Status"))
		}
	}
}

// GetAttachment answers with raw bytes, not a WBXML document.
func TestGetAttachmentReturnsBytes(t *testing.T) {
	srv, user, inbox := testServer(t)
	msgID, err := srv.Store.InsertMessage(&store.Message{
		UserID:            user.ID,
		FolderID:          inbox.ID,
		Subject:           "att",
		ToAddr:            user.Address,
		DateReceived:      1792558000,
		Size:              32,
		Importance:        1,
		MessageClass:      "IPM.Note",
		InternetMessageID: "<ga-1@example.com>",
	})
	if err != nil {
		t.Fatalf("insert message: %v", err)
	}
	payload := []byte("attachment bytes")
	attID, err := srv.Store.InsertAttachment(&store.Attachment{
		MessageID:   msgID,
		FileName:    "a.bin",
		ContentType: "application/octet-stream",
		Size:        int64(len(payload)),
		Data:        payload,
	})
	if err != nil {
		t.Fatalf("insert attachment: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.serveGetAttachment(rec, user, i64toa(msgID)+":"+i64toa(attID))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), payload) {
		t.Fatalf("body = %q", rec.Body.String())
	}

	// A reference to someone else's attachment must not leak.
	other, err := srv.Store.CreateUser("bob", "bob@example.com", "Bob", "x")
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}
	rec = httptest.NewRecorder()
	srv.serveGetAttachment(rec, other, i64toa(msgID)+":"+i64toa(attID))
	if rec.Code != 404 {
		t.Fatalf("cross-account status = %d, want 404", rec.Code)
	}
}

// OofMessage needs a real boolean audience flag.
func TestOofResponseMarksInternalAudience(t *testing.T) {
	srv, user, _ := testServer(t)
	set := wbxml.N(pageSettings, "Oof",
		wbxml.N(pageSettings, "Set",
			wbxml.T(pageSettings, "OofState", "1"),
			wbxml.N(pageSettings, "OofMessage",
				wbxml.T(pageSettings, "AppliesToInternal", ""),
				wbxml.T(pageSettings, "ReplyMessage", "away"))))
	node := srv.oofResponse(user, set)
	msg := node.Path("Get", "OofMessage")
	if msg == nil {
		t.Fatalf("no OofMessage in %v", node.Children)
	}
	if got := msg.ChildText("AppliesToInternal"); got != "1" {
		t.Fatalf("AppliesToInternal = %q, want 1", got)
	}
}
