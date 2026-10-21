package eas

import (
	"testing"
	"time"

	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/wbxml"
)

// Answering a single occurrence of a recurring meeting must not mark the whole
// series, and must leave the other occurrences untouched.
func TestMeetingResponseInstanceIdMarksOneOccurrence(t *testing.T) {
	srv, user, calendar := testServer(t)
	start := time.Date(2026, 10, 27, 2, 0, 0, 0, time.UTC).Unix()
	event := &store.Event{
		UserID:        user.ID,
		FolderID:      calendar.ID,
		UID:           "series-1",
		Subject:       "weekly",
		StartTime:     start,
		EndTime:       start + 3600,
		DtStamp:       start,
		MeetingStatus: 3,
		Recurrence:    store.Recurrence{Type: 1, Interval: 1, DayOfWeek: 4, Occurrences: 4},
	}
	id, err := srv.Store.InsertEvent(event)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	occurrence := time.Unix(start+7*86400, 0).UTC()
	req := wbxml.N(pageMeetingResponse, "MeetingResponse",
		wbxml.N(pageMeetingResponse, "Request",
			wbxml.T(pageMeetingResponse, "UserResponse", "3"), // decline
			wbxml.T(pageMeetingResponse, "CollectionId", i64toa(calendar.ID)),
			wbxml.T(pageMeetingResponse, "RequestId", i64toa(id)),
			wbxml.T(pageMeetingResponse, "InstanceId", formatEasCalendarTime(occurrence.Unix()))))

	resp, err := srv.cmdMeetingResponse(user, req)
	if err != nil {
		t.Fatalf("meeting response: %v", err)
	}
	if got := resp.Path("Result").ChildText("Status"); got != "1" {
		t.Fatalf("status = %q, want 1", got)
	}

	// The series itself is untouched.
	stored, err := srv.Store.EventByID(id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.MeetingStatus != 3 {
		t.Fatalf("series MeetingStatus = %d, want it unchanged (3)", stored.MeetingStatus)
	}

	exceptions, err := srv.Store.EventExceptions(id)
	if err != nil {
		t.Fatalf("load exceptions: %v", err)
	}
	if len(exceptions) != 1 {
		t.Fatalf("got %d exceptions, want 1", len(exceptions))
	}
	if exceptions[0].Occurrence != occurrence.Unix() {
		t.Fatalf("occurrence = %d, want %d", exceptions[0].Occurrence, occurrence.Unix())
	}
	if exceptions[0].MeetingStatus != 7 {
		t.Fatalf("occurrence MeetingStatus = %d, want 7 (declined)", exceptions[0].MeetingStatus)
	}
}

// Without an InstanceId the whole series is answered, as before.
func TestMeetingResponseWithoutInstanceIdMarksSeries(t *testing.T) {
	srv, user, calendar := testServer(t)
	start := time.Date(2026, 10, 27, 2, 0, 0, 0, time.UTC).Unix()
	id, err := srv.Store.InsertEvent(&store.Event{
		UserID: user.ID, FolderID: calendar.ID, UID: "series-2", Subject: "weekly",
		StartTime: start, EndTime: start + 3600, DtStamp: start, MeetingStatus: 3,
		Recurrence: store.Recurrence{Type: 1, Interval: 1, DayOfWeek: 4, Occurrences: 4},
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	req := wbxml.N(pageMeetingResponse, "MeetingResponse",
		wbxml.N(pageMeetingResponse, "Request",
			wbxml.T(pageMeetingResponse, "UserResponse", "2"), // accept
			wbxml.T(pageMeetingResponse, "CollectionId", i64toa(calendar.ID)),
			wbxml.T(pageMeetingResponse, "RequestId", i64toa(id))))
	if _, err := srv.cmdMeetingResponse(user, req); err != nil {
		t.Fatalf("meeting response: %v", err)
	}
	stored, err := srv.Store.EventByID(id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.MeetingStatus != 3 {
		t.Fatalf("series MeetingStatus = %d, want 3", stored.MeetingStatus)
	}
	exceptions, _ := srv.Store.EventExceptions(id)
	if len(exceptions) != 0 {
		t.Fatalf("series answer created %d exceptions", len(exceptions))
	}
}

// An unparsable InstanceId is a protocol error rather than a silent no-op.
func TestMeetingResponseBadInstanceId(t *testing.T) {
	srv, user, calendar := testServer(t)
	start := time.Date(2026, 10, 27, 2, 0, 0, 0, time.UTC).Unix()
	id, err := srv.Store.InsertEvent(&store.Event{
		UserID: user.ID, FolderID: calendar.ID, UID: "series-3", Subject: "weekly",
		StartTime: start, EndTime: start + 3600, DtStamp: start, MeetingStatus: 3,
		Recurrence: store.Recurrence{Type: 1, Interval: 1, DayOfWeek: 4, Occurrences: 4},
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	req := wbxml.N(pageMeetingResponse, "MeetingResponse",
		wbxml.N(pageMeetingResponse, "Request",
			wbxml.T(pageMeetingResponse, "UserResponse", "2"),
			wbxml.T(pageMeetingResponse, "CollectionId", i64toa(calendar.ID)),
			wbxml.T(pageMeetingResponse, "RequestId", i64toa(id)),
			wbxml.T(pageMeetingResponse, "InstanceId", "not-a-time")))
	resp, err := srv.cmdMeetingResponse(user, req)
	if err != nil {
		t.Fatalf("meeting response: %v", err)
	}
	if got := resp.Path("Result").ChildText("Status"); got != "2" {
		t.Fatalf("status = %q, want 2", got)
	}
}
