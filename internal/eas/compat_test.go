package eas

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"remoon.net/lifemail/internal/deliver"
	"remoon.net/lifemail/internal/smtp"
	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/wbxml"
)

func sendMailRequest(t *testing.T, srv *Server, protocol, body string) *httptest.ResponseRecorder {
	t.Helper()
	doc := wbxml.N(pageComposeMail, "SendMail",
		wbxml.T(pageComposeMail, "Mime", base64.StdEncoding.EncodeToString([]byte(body))))
	raw, err := wbxml.Encode(doc, codec)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost,
		"/Microsoft-Server-ActiveSync?Cmd=SendMail&DeviceId=dev1", bytes.NewReader(raw))
	req.SetBasicAuth("alice@example.com", "secret")
	req.Header.Set("MS-ASProtocolVersion", protocol)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestSendMailEas14ReturnsEmptyBody(t *testing.T) {
	srv, alice, _ := testServer(t)
	bob, err := srv.Store.CreateUser("bob", "bob@example.com", "Bob", "unused-hash")
	if err != nil {
		t.Fatal(err)
	}

	rec := sendMailRequest(t, srv, "14.1", localTestMIME)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("EAS 14 success must have an empty body, got %x", rec.Body.Bytes())
	}

	inbox, err := srv.Store.FolderByType(bob.ID, deliver.FolderInbox)
	if err != nil {
		t.Fatal(err)
	}
	if msgs, _ := srv.Store.MessagesByFolder(inbox.ID, 0); len(msgs) != 1 {
		t.Fatalf("bob inbox = %d messages", len(msgs))
	}
	// EAS 14 clients omit SaveInSentItems and rely on the server copy.
	sent, err := srv.Store.FolderByType(alice.ID, deliver.FolderSent)
	if err != nil {
		t.Fatal(err)
	}
	if msgs, _ := srv.Store.MessagesByFolder(sent.ID, 0); len(msgs) != 1 {
		t.Fatalf("alice sent = %d messages", len(msgs))
	}
}

func TestSendMailFailsWhenSMTPMissing(t *testing.T) {
	srv, _, _ := testServer(t) // bob is not created: this goes upstream
	rec := sendMailRequest(t, srv, "14.1", localTestMIME)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("missing SMTP must fail, not queue")
	}
	doc, err := wbxml.Decode(rec.Body.Bytes(), codec)
	if err != nil {
		t.Fatal(err)
	}
	if doc.ChildText("Status") != "6" {
		t.Fatalf("status = %q, want 6", doc.ChildText("Status"))
	}
	if items, _ := srv.Store.DueOutbound(0, 10); len(items) != 0 {
		t.Fatalf("nothing may be queued when SMTP is unconfigured: %+v", items)
	}
}

func TestSendMailQueuesWhenSMTPFails(t *testing.T) {
	srv, _, _ := testServer(t)
	// An enabled sender pointed at a closed port fails fast, like a relay
	// outage.
	srv.Sender = &smtp.Sender{
		Provider: func() smtp.Config {
			return smtp.Config{Host: "127.0.0.1", Port: 1, Password: "token"}
		},
		Timeout: 300 * time.Millisecond,
	}

	rec := sendMailRequest(t, srv, "14.1", localTestMIME)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("response = %d, %d bytes", rec.Code, rec.Body.Len())
	}

	items, err := srv.Store.DueOutbound(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("queued = %d messages", len(items))
	}
	if len(items[0].Recipients) != 1 || items[0].Recipients[0] != "bob@example.com" {
		t.Fatalf("recipients = %v", items[0].Recipients)
	}
}

func TestSendMailLegacyReturnsStatus(t *testing.T) {
	srv, _, _ := testServer(t)
	if _, err := srv.Store.CreateUser("bob", "bob@example.com", "Bob", "unused-hash"); err != nil {
		t.Fatal(err)
	}
	rec := sendMailRequest(t, srv, "12.1", localTestMIME)
	if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
		t.Fatalf("legacy response = %d, %d bytes", rec.Code, rec.Body.Len())
	}
	doc, err := wbxml.Decode(rec.Body.Bytes(), codec)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Name != "SendMail" || doc.ChildText("Status") != "1" {
		detail := ""
		for _, c := range doc.Children {
			detail += c.Name + "=" + c.Text + " "
		}
		t.Fatalf("response root=%s children: %s", doc.Name, detail)
	}
}

func TestMoveItemsResponseIsFlat(t *testing.T) {
	srv, user, inbox := testServer(t)
	drafts, err := srv.Store.FolderByType(user.ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	msgID, err := srv.Store.InsertMessage(&store.Message{
		UserID:   user.ID,
		FolderID: inbox.ID,
		Subject:  "move me",
	})
	if err != nil {
		t.Fatal(err)
	}

	body, err := wbxml.Encode(wbxml.N(pageMove, "MoveItems",
		wbxml.N(pageMove, "Move",
			wbxml.T(pageMove, "SrcMsgId", i64toa(msgID)),
			wbxml.T(pageMove, "SrcFldId", i64toa(inbox.ID)),
			wbxml.T(pageMove, "DstFldId", i64toa(drafts.ID)))), codec)
	if err != nil {
		t.Fatal(err)
	}
	doc := decode(t, post(t, srv, "MoveItems", "dev1", body))
	response := doc.Child("Response")
	if response == nil {
		t.Fatalf("no Response child: %+v", doc)
	}
	if response.Child("Move") != nil {
		t.Fatal("Response must not wrap fields in a Move element")
	}
	if got := response.ChildText("SrcMsgId"); got != i64toa(msgID) {
		t.Fatalf("SrcMsgId = %q", got)
	}
	if got := response.ChildText("Status"); got != "3" {
		t.Fatalf("Status = %q", got)
	}
	if response.ChildText("DstMsgId") == "" {
		t.Fatal("DstMsgId missing on success")
	}
}
