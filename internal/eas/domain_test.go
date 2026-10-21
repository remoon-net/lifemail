package eas

import "testing"

func TestClassifyRecipientsByDomain(t *testing.T) {
	srv, _, _ := testServer(t)
	if _, err := srv.Store.CreateUser("bob", "bob@example.com", "Bob", "unused-hash"); err != nil {
		t.Fatal(err)
	}
	srv.IsLocalDomain = func(domain string) bool { return domain == "example.com" }

	local, external, unknown := srv.classifyRecipients([]string{
		"bob@example.com",
		"nobody@example.com",
		"someone@other.test",
	})
	if len(local) != 1 || local[0].Address != "bob@example.com" {
		t.Fatalf("local = %+v", local)
	}
	if len(unknown) != 1 || unknown[0] != "nobody@example.com" {
		t.Fatalf("unknown = %v", unknown)
	}
	if len(external) != 1 || external[0] != "someone@other.test" {
		t.Fatalf("external = %v", external)
	}
}

func TestUnknownLocalRecipientFailsSend(t *testing.T) {
	srv, _, _ := testServer(t)
	srv.IsLocalDomain = func(domain string) bool { return domain == "example.com" }

	// localTestMIME goes to bob@example.com, which has no account here.
	rec := sendMailRequest(t, srv, "14.1", localTestMIME)
	if rec.Body.Len() == 0 {
		t.Fatal("expected a failure status document")
	}
	doc := decode(t, rec)
	if doc.ChildText("Status") != "6" {
		t.Fatalf("status = %q, want 6", doc.ChildText("Status"))
	}
}
