package eas

import (
	"context"
	"encoding/base64"
	"testing"

	"remoon.net/lifemail/internal/deliver"
	"remoon.net/lifemail/internal/wbxml"
)

const localTestMIME = "From: Alice <alice@example.com>\r\n" +
	"To: Bob <bob@example.com>\r\n" +
	"Subject: internal hello\r\n" +
	"Message-Id: <local-1@example.com>\r\n" +
	"Date: Mon, 20 Oct 2026 10:00:00 +0000\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"hello internally\r\n"

func TestComposeMailDeliversLocallyWithoutSMTP(t *testing.T) {
	srv, alice, _ := testServer(t) // Sender is disabled in this fixture
	bob, err := srv.Store.CreateUser("bob", "bob@example.com", "Bob", "unused-hash")
	if err != nil {
		t.Fatal(err)
	}

	body := base64.StdEncoding.EncodeToString([]byte(localTestMIME))
	req := wbxml.N(pageComposeMail, "ComposeMail",
		wbxml.N(pageComposeMail, "SendMail",
			wbxml.T(pageComposeMail, "Mime", body)),
		wbxml.N(pageComposeMail, "SaveInSentItems"))

	resp, err := srv.cmdComposeMail(alice, req)
	if err != nil {
		t.Fatal(err)
	}
	send := resp.Child("SendMail")
	if send == nil || send.ChildText("Status") != "1" {
		t.Fatalf("SendMail status = %+v, want 1", resp)
	}

	inbox, err := srv.Store.FolderByType(bob.ID, deliver.FolderInbox)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := srv.Store.MessagesByFolder(inbox.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("bob inbox messages = %d, want 1", len(msgs))
	}
	if msgs[0].Subject != "internal hello" || msgs[0].IsRead {
		t.Fatalf("unexpected message: %+v", msgs[0])
	}

	sent, err := srv.Store.FolderByType(alice.ID, deliver.FolderSent)
	if err != nil {
		t.Fatal(err)
	}
	sentMsgs, err := srv.Store.MessagesByFolder(sent.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sentMsgs) != 1 {
		t.Fatalf("alice sent messages = %d, want 1", len(sentMsgs))
	}

	// A retry with the same Message-ID must not create a duplicate copy.
	resp, err = srv.cmdComposeMail(alice, req)
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Child("SendMail").ChildText("Status"); got != "1" {
		t.Fatalf("retry status = %s, want 1", got)
	}
	msgs, _ = srv.Store.MessagesByFolder(inbox.ID, 0)
	if len(msgs) != 1 {
		t.Fatalf("duplicate delivered, inbox has %d messages", len(msgs))
	}
}

func TestSendMailCommandDeliversLocally(t *testing.T) {
	srv, alice, _ := testServer(t)
	bob, err := srv.Store.CreateUser("bob", "bob@example.com", "Bob", "unused-hash")
	if err != nil {
		t.Fatal(err)
	}

	body := base64.StdEncoding.EncodeToString([]byte(localTestMIME))
	// Real clients send Cmd=SendMail with the command element as the root.
	doc := wbxml.N(pageComposeMail, "SendMail",
		wbxml.N(pageComposeMail, "SaveInSentItems"),
		wbxml.T(pageComposeMail, "Mime", body))

	resp, err := srv.dispatch(context.Background(), alice, "dev1", "SendMail", doc)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if resp.Name != "SendMail" || resp.ChildText("Status") != "1" {
		t.Fatalf("response = %+v, want SendMail/Status 1", resp)
	}

	inbox, err := srv.Store.FolderByType(bob.ID, deliver.FolderInbox)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := srv.Store.MessagesByFolder(inbox.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("bob inbox messages = %d, want 1", len(msgs))
	}
}

func TestSendMailAcceptsRawMIME(t *testing.T) {
	srv, alice, _ := testServer(t)
	bob, err := srv.Store.CreateUser("bob", "bob@example.com", "Bob", "unused-hash")
	if err != nil {
		t.Fatal(err)
	}

	// Some Android clients put raw RFC822 in Mime instead of base64.
	doc := wbxml.N(pageComposeMail, "SendMail",
		wbxml.N(pageComposeMail, "SaveInSentItems"),
		wbxml.T(pageComposeMail, "Mime", localTestMIME))

	resp, err := srv.dispatch(context.Background(), alice, "dev1", "SendMail", doc)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if resp.ChildText("Status") != "1" {
		t.Fatalf("status = %q, want 1", resp.ChildText("Status"))
	}

	inbox, err := srv.Store.FolderByType(bob.ID, deliver.FolderInbox)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := srv.Store.MessagesByFolder(inbox.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Subject != "internal hello" {
		t.Fatalf("bob inbox = %+v", msgs)
	}
}

func TestDecodeMimeForms(t *testing.T) {
	wrapped := base64.StdEncoding.EncodeToString([]byte(localTestMIME))
	wrapped = wrapped[:10] + "\r\n " + wrapped[10:]
	cases := []struct {
		name string
		node *wbxml.Node
		want string
	}{
		{"base64", wbxml.T(pageComposeMail, "Mime", wrapped), localTestMIME},
		{"raw", wbxml.T(pageComposeMail, "Mime", localTestMIME), localTestMIME},
		{"opaque", &wbxml.Node{Page: pageComposeMail, Name: "Mime", Text: localTestMIME, Binary: true}, localTestMIME},
	}
	for _, tc := range cases {
		raw, err := decodeMime(tc.node)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if string(raw) != tc.want {
			t.Fatalf("%s: decoded %q", tc.name, raw)
		}
	}

	if _, err := decodeMime(wbxml.T(pageComposeMail, "Mime", "")); err == nil {
		t.Fatal("empty mime should fail")
	}
	if _, err := decodeMime(nil); err == nil {
		t.Fatal("missing mime should fail")
	}
}
