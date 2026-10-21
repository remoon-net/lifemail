package eas

import "testing"

// Clients pick the parser for a collection from the Class element in the Sync
// response. Without it, calendar and contact items are parsed as mail and
// silently dropped.
func TestSyncResponseCarriesCollectionClass(t *testing.T) {
	srv, user, _ := testServer(t)
	for _, tc := range []struct {
		folderType int
		want       string
	}{
		{2, "Email"},
		{8, "Calendar"},
		{9, "Contacts"},
	} {
		folder, err := srv.Store.FolderByType(user.ID, tc.folderType)
		if err != nil {
			t.Fatal(err)
		}
		coll := syncAsUser(t, srv, user, "0", folder.ID, nil)
		if got := coll.ChildText("Class"); got != tc.want {
			t.Fatalf("folder type %d: class = %q, want %q", tc.folderType, got, tc.want)
		}
	}
}
