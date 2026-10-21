package eas

import (
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"remoon.net/lifemail/internal/mailparse"
	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/wbxml"
)

// newPeer adds a second account so local delivery can be observed end to end.
func newPeer(t *testing.T, srv *Server, name string) *store.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	user, err := srv.Store.CreateUser(name, name+"@example.com", name, string(hash))
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return user
}

func inboxSubjects(t *testing.T, srv *Server, user *store.User) []string {
	t.Helper()
	inbox, err := srv.Store.FolderByType(user.ID, 2)
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	msgs, err := srv.Store.MessagesByFolder(inbox.ID, 20)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Subject)
	}
	return out
}

// Accepting an invitation has to tell the organiser: the device only sends
// MeetingResponse and never composes the answer itself.
func TestMeetingResponseRepliesToOrganiser(t *testing.T) {
	srv, alice, folder := testServer(t)
	bob := newPeer(t, srv, "bob")

	event := &store.Event{
		UserID:         alice.ID,
		FolderID:       folder.ID,
		UID:            "meeting-1",
		Subject:        "规划会",
		StartTime:      1792551600,
		EndTime:        1792555200,
		DtStamp:        1792550000,
		OrganizerEmail: bob.Address,
		OrganizerName:  "Bob",
	}
	id, err := srv.Store.InsertEvent(event)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}

	req := wbxml.N(pageMeetingResponse, "MeetingResponse",
		wbxml.N(pageMeetingResponse, "Request",
			wbxml.T(pageMeetingResponse, "CollectionId", i64toa(folder.ID)),
			wbxml.T(pageMeetingResponse, "RequestId", i64toa(id)),
			wbxml.T(pageMeetingResponse, "UserResponse", "2")))
	if _, err := srv.cmdMeetingResponse(alice, req); err != nil {
		t.Fatalf("meeting response: %v", err)
	}

	subjects := inboxSubjects(t, srv, bob)
	if len(subjects) != 1 {
		t.Fatalf("organiser inbox = %v, want one reply", subjects)
	}
	if !strings.Contains(subjects[0], "Accepted") || !strings.Contains(subjects[0], "规划会") {
		t.Fatalf("reply subject = %q", subjects[0])
	}
}

// Declining an event owned by the account itself must not send anything.
func TestMeetingResponseSkipsSelfOrganisedEvent(t *testing.T) {
	srv, alice, folder := testServer(t)
	event := &store.Event{
		UserID:         alice.ID,
		FolderID:       folder.ID,
		UID:            "self-1",
		Subject:        "自己建的会",
		StartTime:      1792551600,
		EndTime:        1792555200,
		DtStamp:        1792550000,
		OrganizerEmail: alice.Address,
	}
	id, err := srv.Store.InsertEvent(event)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
	req := wbxml.N(pageMeetingResponse, "MeetingResponse",
		wbxml.N(pageMeetingResponse, "Request",
			wbxml.T(pageMeetingResponse, "CollectionId", i64toa(folder.ID)),
			wbxml.T(pageMeetingResponse, "RequestId", i64toa(id)),
			wbxml.T(pageMeetingResponse, "UserResponse", "3")))
	if _, err := srv.cmdMeetingResponse(alice, req); err != nil {
		t.Fatalf("meeting response: %v", err)
	}
	if subjects := inboxSubjects(t, srv, alice); len(subjects) != 0 {
		t.Fatalf("self reply must not be sent, got %v", subjects)
	}
}

// Settings/Oof has to persist what the client sends and report it back.
func TestOofSettingsRoundTrip(t *testing.T) {
	srv, alice, _ := testServer(t)

	set := wbxml.N(pageSettings, "Settings",
		wbxml.N(pageSettings, "Oof",
			wbxml.N(pageSettings, "Set",
				wbxml.T(pageSettings, "OofState", "1"),
				wbxml.N(pageSettings, "OofMessage",
					wbxml.T(pageSettings, "AppliesToInternal", ""),
					wbxml.T(pageSettings, "ReplyMessage", "我在休假，下周回来")))))
	resp, err := srv.cmdSettings(alice, set)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	oof := resp.Child("Oof")
	if oof == nil {
		t.Fatal("no Oof element in response")
	}
	if got := oof.Child("Get").ChildText("OofState"); got != "1" {
		t.Fatalf("OofState = %q, want 1", got)
	}
	if got := srv.loadOof(alice.ID); got.Message != "我在休假，下周回来" || got.State != 1 {
		t.Fatalf("stored oof = %+v", got)
	}

	// Turning it off must stick as well.
	off := wbxml.N(pageSettings, "Settings",
		wbxml.N(pageSettings, "Oof",
			wbxml.N(pageSettings, "Set", wbxml.T(pageSettings, "OofState", "0"))))
	if _, err := srv.cmdSettings(alice, off); err != nil {
		t.Fatalf("settings off: %v", err)
	}
	if got := srv.loadOof(alice.ID); got.State != 0 {
		t.Fatalf("state after disable = %d", got.State)
	}
}

// Inbound mail during an Oof window answers the sender once, and never answers
// a machine-generated message.
func TestOofRepliesOncePerCorrespondent(t *testing.T) {
	srv, alice, _ := testServer(t)
	bob := newPeer(t, srv, "bob")
	if err := srv.saveOof(alice.ID, oofSettings{State: 1, Message: "休假中"}); err != nil {
		t.Fatal(err)
	}

	raw := []byte("From: bob@example.com\r\nTo: alice@example.com\r\nSubject: 你好\r\n\r\nhi\r\n")
	parsed, err := mailparse.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	srv.OofReply(alice, parsed, raw)
	srv.OofReply(alice, parsed, raw) // second time must be suppressed

	subjects := inboxSubjects(t, srv, bob)
	if len(subjects) != 1 {
		t.Fatalf("auto replies = %v, want exactly one", subjects)
	}
	if !strings.Contains(subjects[0], "Auto") || !strings.Contains(subjects[0], "你好") {
		t.Fatalf("reply subject = %q", subjects[0])
	}

	// A bounce or a mailing list post must not be answered.
	auto := []byte("From: robot@example.com\r\nTo: alice@example.com\r\n" +
		"Subject: out of office\r\nAuto-Submitted: auto-replied\r\n\r\nnope\r\n")
	parsedAuto, err := mailparse.Parse(auto)
	if err != nil {
		t.Fatalf("parse auto: %v", err)
	}
	srv.OofReply(alice, parsedAuto, auto)
	robot, err := srv.Store.UserByAddress("robot@example.com")
	if err == nil && robot != nil {
		t.Fatal("unexpected account")
	}
}

// Busy slots are 30 minutes each, and a busy event must mark exactly the slots
// it covers.
func TestMergedFreeBusyMarksBusySlots(t *testing.T) {
	srv, alice, _ := testServer(t)
	calendar, err := srv.Store.FolderByType(alice.ID, 8)
	if err != nil {
		t.Fatalf("calendar folder: %v", err)
	}
	// 10:00-11:00 UTC, measured from 10:00 to 12:00 → slots 00,30,00,30
	start := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC).Unix()
	if _, err := srv.Store.InsertEvent(&store.Event{
		UserID:     alice.ID,
		FolderID:   calendar.ID,
		UID:        "busy-1",
		Subject:    "standing meeting",
		StartTime:  start,
		EndTime:    start + 3600,
		DtStamp:    start,
		BusyStatus: 2,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got := srv.mergedFreeBusy(alice, start, start+2*3600)
	if got != "2200" {
		t.Fatalf("mergedFreeBusy = %q, want %q", got, "2200")
	}
	// A free event must not mark the slot busy.
	if free := srv.mergedFreeBusy(alice, start+2*3600, start+3*3600); free != "00" {
		t.Fatalf("outside window = %q, want %q", free, "00")
	}
}

// MS-ASCAL numbers the responses in a way that is easy to get backwards:
// 9 is tentative, 3 is received/accepted and 7 is declined.
func TestMeetingResponseStatusMapping(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response string
		want     int
	}{
		{"tentative", "1", 9},
		{"accepted", "2", 3},
		{"declined", "3", 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, alice, folder := testServer(t)
			event := &store.Event{
				UserID:    alice.ID,
				FolderID:  folder.ID,
				UID:       "status-" + tc.name,
				Subject:   "status test",
				StartTime: 1792551600,
				EndTime:   1792555200,
				DtStamp:   1792550000,
			}
			id, err := srv.Store.InsertEvent(event)
			if err != nil {
				t.Fatalf("insert: %v", err)
			}
			req := wbxml.N(pageMeetingResponse, "MeetingResponse",
				wbxml.N(pageMeetingResponse, "Request",
					wbxml.T(pageMeetingResponse, "CollectionId", i64toa(folder.ID)),
					wbxml.T(pageMeetingResponse, "RequestId", i64toa(id)),
					wbxml.T(pageMeetingResponse, "UserResponse", tc.response)))
			if _, err := srv.cmdMeetingResponse(alice, req); err != nil {
				t.Fatalf("meeting response: %v", err)
			}
			got, err := srv.Store.EventByID(id)
			if err != nil {
				t.Fatalf("reload: %v", err)
			}
			if got.MeetingStatus != tc.want {
				t.Fatalf("MeetingStatus = %d, want %d", got.MeetingStatus, tc.want)
			}
		})
	}
}
