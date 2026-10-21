package eas

import (
	"encoding/hex"
	"testing"

	"remoon.net/lifemail/internal/wbxml"
)

// The first request Android clients send after authentication. Captured from a
// real client: WBXML 1.3, page switch to FolderHierarchy, FolderSync/SyncKey
// "0" encoded as a NUL-terminated STR_I.
const androidFolderSyncBody = "03016a00000756520330000101"

func TestDecodeClientFolderSyncRequest(t *testing.T) {
	body, err := hex.DecodeString(androidFolderSyncBody)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := wbxml.Decode(body, codec)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.Name != "FolderSync" {
		t.Fatalf("root = %q, want FolderSync", doc.Name)
	}
	if got := doc.ChildText("SyncKey"); got != "0" {
		t.Fatalf("SyncKey = %q, want 0", got)
	}
}

func TestEncodeFolderSyncMatchesClientBytes(t *testing.T) {
	body, err := wbxml.Encode(wbxml.N(pageFolderHierarchy, "FolderSync",
		wbxml.T(pageFolderHierarchy, "SyncKey", "0")), codec)
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(body); got != androidFolderSyncBody {
		t.Fatalf("encoded = %s\nwant       %s", got, androidFolderSyncBody)
	}
}
