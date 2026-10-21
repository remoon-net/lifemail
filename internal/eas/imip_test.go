package eas

import (
	"encoding/base64"
	"strings"
	"testing"

	"remoon.net/lifemail/internal/mailparse"
	"remoon.net/lifemail/internal/store"
)

// buildIMipReply mirrors what MIUI Mail sends: multipart/alternative with a
// single base64 text/calendar part whose ATTENDEE line is folded.
func buildIMipReply(uid, partstat, attendee string) []byte {
	ics := strings.Join([]string{
		"BEGIN:VCALENDAR",
		"METHOD:REPLY",
		"VERSION:2.0",
		"BEGIN:VEVENT",
		"UID:" + uid,
		"DTSTART:20261022T030000Z",
		"SUMMARY:standup",
		"ATTENDEE;ROLE=REQ-PARTICIPANT;PARTSTAT=" + partstat + ";CN=\"someone\":MAILTO:" + attendee[:4],
		" " + attendee[4:],
		"END:VEVENT",
		"END:VCALENDAR",
		"",
	}, "\r\n")

	// Any 7bit-safe transport will do: base64 is what clients use.
	var b strings.Builder
	b.WriteString("From: someone@example.com\r\n")
	b.WriteString("To: alice@example.com\r\n")
	b.WriteString("Subject: Re: standup\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/alternative; boundary=\"b1\"\r\n")
	b.WriteString("\r\n")
	b.WriteString("--b1\r\n")
	b.WriteString("Content-Type: text/calendar; method=REPLY; name=\"invite.ics\"\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	b.WriteString("\r\n")
	b.WriteString(base64Lines(ics))
	b.WriteString("\r\n--b1--\r\n")
	return []byte(b.String())
}

func base64Lines(s string) string {
	const wrapped = 76
	enc := base64.StdEncoding.EncodeToString([]byte(s))
	var out strings.Builder
	for i := 0; i < len(enc); i += wrapped {
		end := i + wrapped
		if end > len(enc) {
			end = len(enc)
		}
		out.WriteString(enc[i:end])
		out.WriteString("\r\n")
	}
	return out.String()
}

// A reply with METHOD:REPLY has to move the organiser's copy of the meeting to
// the right state, even though the client reported MeetingStatus 3.
func TestIMipReplyUpdatesMeetingStatus(t *testing.T) {
	srv, alice, folder := testServer(t)
	event := &store.Event{
		UserID:         alice.ID,
		FolderID:       folder.ID,
		UID:            "meeting-42",
		Subject:        "standup",
		StartTime:      1792551600,
		EndTime:        1792555200,
		DtStamp:        1792550000,
		MeetingStatus:  3,
		OrganizerEmail: alice.Address,
		Attendees: []store.Attendee{
			{Name: "bob", Email: "bob@example.com", Status: 0, Type: 1},
		},
	}
	if _, err := srv.Store.InsertEvent(event); err != nil {
		t.Fatalf("insert: %v", err)
	}

	raw := buildIMipReply("meeting-42", "DECLINED", "bob@example.com")
	parsed, err := mailparse.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	srv.HandleIMipReply(alice, parsed, raw)

	got, err := srv.Store.EventByUID(alice.ID, "meeting-42")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.MeetingStatus != 7 {
		t.Fatalf("MeetingStatus = %d, want 7 (declined)", got.MeetingStatus)
	}
	if len(got.Attendees) != 1 || got.Attendees[0].Status != 4 {
		t.Fatalf("attendee status = %+v, want decline (4)", got.Attendees)
	}
}

// An invitation that was just received already reports status 3, so accepting
// cannot be detected by comparing the meeting status; the attendee entry has to
// be updated on its own.
func TestIMipAcceptTouchesAttendeeDespiteSameStatus(t *testing.T) {
	srv, alice, folder := testServer(t)
	event := &store.Event{
		UserID:         alice.ID,
		FolderID:       folder.ID,
		UID:            "meeting-43",
		Subject:        "review",
		StartTime:      1792551600,
		EndTime:        1792555200,
		DtStamp:        1792550000,
		MeetingStatus:  3,
		OrganizerEmail: alice.Address,
		Attendees: []store.Attendee{
			{Name: "bob", Email: "bob@example.com", Status: 0, Type: 1},
		},
	}
	if _, err := srv.Store.InsertEvent(event); err != nil {
		t.Fatalf("insert: %v", err)
	}

	raw := buildIMipReply("meeting-43", "ACCEPTED", "bob@example.com")
	parsed, err := mailparse.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	srv.HandleIMipReply(alice, parsed, raw)

	got, err := srv.Store.EventByUID(alice.ID, "meeting-43")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.MeetingStatus != 3 {
		t.Fatalf("MeetingStatus = %d, want 3 (accepted)", got.MeetingStatus)
	}
	if len(got.Attendees) != 1 || got.Attendees[0].Status != 3 {
		t.Fatalf("attendee status = %+v, want accepted (3)", got.Attendees)
	}
}

// A reply for a meeting this account does not organise must be ignored.
func TestIMipReplyIgnoresUnknownMeeting(t *testing.T) {
	srv, alice, _ := testServer(t)
	raw := buildIMipReply("does-not-exist", "ACCEPTED", "bob@example.com")
	parsed, err := mailparse.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	srv.HandleIMipReply(alice, parsed, raw) // must not panic or create anything
}

// The folded ATTENDEE line is the interesting part: without unfolding the
// address is truncated and the attendee would not match.
func TestParseCalendarReplyUnfoldsAttendee(t *testing.T) {
	ics := "BEGIN:VCALENDAR\r\nMETHOD:REPLY\r\nBEGIN:VEVENT\r\nUID:u1\r\n" +
		"ATTENDEE;ROLE=REQ-PARTICIPANT;PARTSTAT=ACCEPTED;CN=\"test\":MAILTO:test@ma\r\n" +
		"\til.lo.remoon.cn\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	uid, partstat, attendee := parseCalendarReply(ics)
	if uid != "u1" || partstat != "ACCEPTED" || attendee != "test@mail.lo.remoon.cn" {
		t.Fatalf("parsed = %q %q %q", uid, partstat, attendee)
	}
}
