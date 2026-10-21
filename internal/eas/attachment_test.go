package eas

import (
	"bytes"
	"encoding/base64"
	"testing"

	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/wbxml"
)

// An attachment fetch must answer with itemoperations Range/Total/Data. MIUI
// Mail shows 0 B and saves an empty file when Total is missing.
func TestItemOperationsAttachmentRange(t *testing.T) {
	srv, user, folder := testServer(t)
	msgID, err := srv.Store.InsertMessage(&store.Message{
		UserID:            user.ID,
		FolderID:          folder.ID,
		Subject:           "with attachment",
		FromAddr:          "joke@example.com",
		ToAddr:            user.Address,
		DateReceived:      1792558000,
		Size:              1024,
		Importance:        1,
		MessageClass:      "IPM.Note",
		InternetMessageID: "<att-1@example.com>",
		BodyText:          "see attachment",
	})
	if err != nil {
		t.Fatalf("insert message: %v", err)
	}
	payload := bytes.Repeat([]byte("abcdefgh"), 64) // 512 bytes
	attID, err := srv.Store.InsertAttachment(&store.Attachment{
		MessageID:   msgID,
		FileName:    "gradient.png",
		ContentType: "image/png",
		Size:        int64(len(payload)),
		Data:        payload,
	})
	if err != nil {
		t.Fatalf("insert attachment: %v", err)
	}
	ref := i64toa(msgID) + ":" + i64toa(attID)

	request := func(rng string) *wbxml.Node {
		opts := wbxml.N(pageItemOperations, "Options")
		if rng != "" {
			opts.Children = append(opts.Children, wbxml.T(pageItemOperations, "Range", rng))
		}
		return wbxml.N(pageItemOperations, "ItemOperations",
			wbxml.N(pageItemOperations, "Fetch",
				wbxml.T(pageAirSyncBase, "FileReference", ref),
				opts))
	}

	// Whole file.
	resp, err := srv.cmdItemOperations(user, request(""))
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	props := resp.Path("Response", "Fetch", "Properties")
	if props == nil {
		t.Fatalf("no Properties in response")
	}
	if got := props.ChildText("Total"); got != "512" {
		t.Fatalf("Total = %q, want 512", got)
	}
	if got := props.ChildText("Range"); got != "0-511" {
		t.Fatalf("Range = %q, want 0-511", got)
	}
	got, err := base64.StdEncoding.DecodeString(props.ChildText("Data"))
	if err != nil {
		t.Fatalf("Data is not base64: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: %d bytes", len(got))
	}

	// A ranged request returns only that chunk but still reports the total.
	resp, err = srv.cmdItemOperations(user, request("0-99"))
	if err != nil {
		t.Fatalf("ranged fetch: %v", err)
	}
	props = resp.Path("Response", "Fetch", "Properties")
	if got := props.ChildText("Total"); got != "512" {
		t.Fatalf("ranged Total = %q, want 512", got)
	}
	if got := props.ChildText("Range"); got != "0-99" {
		t.Fatalf("ranged Range = %q, want 0-99", got)
	}
	chunk, err := base64.StdEncoding.DecodeString(props.ChildText("Data"))
	if err != nil {
		t.Fatalf("ranged Data is not base64: %v", err)
	}
	if !bytes.Equal(chunk, payload[:100]) {
		t.Fatalf("ranged chunk mismatch: %d bytes", len(chunk))
	}
}

// A range past the end must be clamped instead of panicking.
func TestFetchRangeClamps(t *testing.T) {
	fetch := func(rng string) *wbxml.Node {
		opts := wbxml.N(pageItemOperations, "Options")
		if rng != "" {
			opts.Children = append(opts.Children, wbxml.T(pageItemOperations, "Range", rng))
		}
		return wbxml.N(pageItemOperations, "Fetch", opts)
	}
	cases := []struct {
		rng        string
		size       int
		wantStart  int
		wantEndIdx int
	}{
		{"", 10, 0, 9},
		{"0-4", 10, 0, 4},
		{"5-", 10, 5, 9},
		{"8-99", 10, 8, 9},
		{"99-200", 10, 9, 9},
		{"0-4", 0, 0, 0},
	}
	for _, tc := range cases {
		start, end := fetchRange(fetch(tc.rng), tc.size)
		if start != tc.wantStart || end != tc.wantEndIdx {
			t.Fatalf("range %q size %d = %d-%d, want %d-%d",
				tc.rng, tc.size, start, end, tc.wantStart, tc.wantEndIdx)
		}
	}
}
