package eas

import (
	"testing"

	"remoon.net/lifemail/internal/wbxml"
)

func TestProvisionDeliversRemoteWipe(t *testing.T) {
	srv, user, _ := testServer(t)
	if err := srv.Store.UpsertDevice(user.ID, "dev1", "Android", "14.1"); err != nil {
		t.Fatal(err)
	}
	if err := srv.Store.RequestWipe(user.ID, "dev1"); err != nil {
		t.Fatal(err)
	}

	resp := postDoc(t, srv, "Provision", wbxml.N(pageProvision, "Provision",
		wbxml.N(pageProvision, "Policies",
			wbxml.N(pageProvision, "Policy",
				wbxml.T(pageProvision, "PolicyType", "MS-EAS-Provisioning-WBXML"),
			),
		),
	))
	if resp.Child("RemoteWipe") == nil {
		t.Fatalf("response has no RemoteWipe: %+v", resp)
	}
	if got := resp.ChildText("Status"); got != "1" {
		t.Fatalf("status = %q, want 1", got)
	}
	if wipe, err := srv.Store.WipeRequested("dev1"); err != nil || wipe {
		t.Fatalf("wipe flag should be cleared, got %v (%v)", wipe, err)
	}

	// The next provisioning round trip is a normal policy exchange again.
	resp = postDoc(t, srv, "Provision", wbxml.N(pageProvision, "Provision",
		wbxml.N(pageProvision, "Policies",
			wbxml.N(pageProvision, "Policy",
				wbxml.T(pageProvision, "PolicyType", "MS-EAS-Provisioning-WBXML"),
			),
		),
	))
	if resp.Child("RemoteWipe") != nil {
		t.Fatal("wipe must be delivered only once")
	}
}
