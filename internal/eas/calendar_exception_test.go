package eas

import (
	"testing"
	"time"

	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/wbxml"
)

// A client cancels one occurrence of a weekly meeting. The override has to
// survive a store round trip and come back out in the ApplicationData.
func TestRecurringEventExceptionRoundTrip(t *testing.T) {
	srv, user, calendar := testServer(t)
	start := time.Date(2026, 10, 20, 3, 0, 0, 0, time.UTC).Unix()
	event := &store.Event{
		UserID:     user.ID,
		FolderID:   calendar.ID,
		UID:        "weekly-1",
		Subject:    "standup",
		StartTime:  start,
		EndTime:    start + 1800,
		DtStamp:    start,
		BusyStatus: 2,
		Reminder:   15,
		Recurrence: store.Recurrence{Type: 1, Interval: 1, DayOfWeek: 4, Occurrences: 6},
		Exceptions: []store.Exception{{
			Occurrence: start + 7*86400,
			Deleted:    true,
		}, {
			Occurrence: start + 14*86400,
			StartTime:  start + 14*86400 + 3600,
			EndTime:    start + 14*86400 + 5400,
			Subject:    "standup (moved)",
			Location:   "room 2",
		}},
	}
	id, err := srv.Store.InsertEvent(event)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := srv.Store.ReplaceEventExceptions(id, event.Exceptions); err != nil {
		t.Fatalf("store exceptions: %v", err)
	}

	stored, err := srv.Store.EventByID(id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	exceptions, err := srv.Store.EventExceptions(id)
	if err != nil {
		t.Fatalf("load exceptions: %v", err)
	}
	if len(exceptions) != 2 {
		t.Fatalf("got %d exceptions, want 2", len(exceptions))
	}
	stored.Exceptions = exceptions

	app := eventAppData(stored, defaultTimeZoneBlob())
	if app.Child("Recurrence") == nil {
		t.Fatalf("no Recurrence element")
	}
	// MS-ASCAL: Exceptions is a child of ApplicationData, not of Recurrence.
	list := app.Child("Exceptions")
	if list == nil {
		t.Fatalf("Exceptions must be a direct child of ApplicationData")
	}
	if len(list.Children) != 2 {
		t.Fatalf("got %d exception elements, want 2", len(list.Children))
	}
	if got := list.Children[0].ChildText("Deleted"); got != "1" {
		t.Fatalf("first exception Deleted = %q, want 1", got)
	}
	if got := list.Children[1].ChildText("Subject"); got != "standup (moved)" {
		t.Fatalf("second exception Subject = %q", got)
	}
	if got := list.Children[1].ChildText("Location"); got != "room 2" {
		t.Fatalf("second exception Location = %q", got)
	}
	if got := list.Children[1].ChildText("StartTime"); got != formatEasCalendarTime(start+14*86400+3600) {
		t.Fatalf("second exception StartTime = %q", got)
	}
}

// Parsing the client's ApplicationData must pick the overrides out of it.
func TestParseExceptionsFromClientTree(t *testing.T) {
	occurrence := time.Date(2026, 10, 27, 3, 0, 0, 0, time.UTC)
	ad := wbxml.N(pageAirSync, "ApplicationData",
		wbxml.T(pageCalendar, "Subject", "standup"),
		wbxml.N(pageCalendar, "Recurrence",
			wbxml.T(pageCalendar, "RecurrenceType", "1"),
			wbxml.T(pageCalendar, "RecurrenceInterval", "1"),
			wbxml.T(pageCalendar, "RecurrenceDayOfWeek", "4")),
		wbxml.N(pageCalendar, "Exceptions",
			wbxml.N(pageCalendar, "Exception",
				wbxml.T(pageCalendar, "ExceptionStartTime", formatEasCalendarTime(occurrence.Unix())),
				wbxml.T(pageCalendar, "Deleted", "1")),
			wbxml.N(pageCalendar, "Exception",
				wbxml.T(pageCalendar, "ExceptionStartTime", formatEasCalendarTime(occurrence.AddDate(0, 0, 7).Unix())),
				wbxml.T(pageCalendar, "StartTime", formatEasCalendarTime(occurrence.AddDate(0, 0, 7).Add(time.Hour).Unix())),
				wbxml.T(pageCalendar, "Subject", "moved"))))

	got := parseExceptions(ad)
	if len(got) != 2 {
		t.Fatalf("got %d exceptions, want 2", len(got))
	}
	if got[0].Occurrence != occurrence.Unix() || !got[0].Deleted {
		t.Fatalf("first exception = %+v", got[0])
	}
	if got[1].Subject != "moved" || got[1].StartTime == 0 {
		t.Fatalf("second exception = %+v", got[1])
	}

	// A client that nests Exceptions inside Recurrence is still understood.
	nested := wbxml.N(pageAirSync, "ApplicationData",
		wbxml.N(pageCalendar, "Recurrence",
			wbxml.N(pageCalendar, "Exceptions",
				wbxml.N(pageCalendar, "Exception",
					wbxml.T(pageCalendar, "ExceptionStartTime", formatEasCalendarTime(occurrence.Unix()))))))
	if got := parseExceptions(nested); len(got) != 1 {
		t.Fatalf("nested exceptions not parsed: %d", len(got))
	}
}

// Replacing with an empty set clears the overrides.
func TestReplaceEventExceptionsClears(t *testing.T) {
	srv, user, calendar := testServer(t)
	id, err := srv.Store.InsertEvent(&store.Event{
		UserID: user.ID, FolderID: calendar.ID, UID: "x", Subject: "x",
		StartTime: 1792558000, EndTime: 1792559000, DtStamp: 1792558000,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := srv.Store.ReplaceEventExceptions(id, []store.Exception{
		{Occurrence: 1792558000, Deleted: true},
	}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := srv.Store.ReplaceEventExceptions(id, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, err := srv.Store.EventExceptions(id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("%d exceptions survived the clear", len(got))
	}
}
