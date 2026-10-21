package eas

import (
	"encoding/base64"
	"testing"
	"time"

	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/wbxml"
)

func TestParseEasTimeFormats(t *testing.T) {
	want := time.Date(2026, 10, 21, 1, 48, 2, 0, time.UTC).Unix()
	for _, value := range []string{
		"2026-10-21T01:48:02.000Z",
		"2026-10-21T01:48:02Z",
		"2026-10-21T01:48:02.0000000Z",
		"2026-10-21T01:48:02.000000Z",
		"20261021T014802Z",
		"2026-10-21T01:48:02.000",
		"2026-10-21 01:48:02",
		"2026-10-21T01:48:02.000+00:00",
		"2026-10-21T09:48:02.000+08:00",
		" 2026-10-21T01:48:02.000Z ",
		"2026-10-21T01:48:02.000z",
	} {
		got := parseEasTime(value)
		if got.IsZero() {
			t.Fatalf("parseEasTime(%q) failed", value)
		}
		if got.Unix() != want {
			t.Fatalf("parseEasTime(%q) = %v, want %v", value, got.UTC(), time.Unix(want, 0).UTC())
		}
	}
	if !parseEasTime("not a time").IsZero() {
		t.Fatal("garbage must not parse")
	}
}

// A client that sends an empty or unknown StartTime must not end up with an
// event in year 1: the calendar would hide it entirely.
func TestParseEventAppDataFallsBackForBadTimes(t *testing.T) {
	for _, start := range []string{"", " ", "0001-01-01T00:00:00.000Z", "garbage"} {
		ad := wbxml.N(pageAirSync, "ApplicationData",
			wbxml.T(pageCalendar, "Subject", "no time"),
			wbxml.T(pageCalendar, "StartTime", start),
			wbxml.T(pageCalendar, "EndTime", start))
		e := parseEventAppData(ad)
		if e.StartTime <= 0 {
			t.Fatalf("start time for %q = %d", start, e.StartTime)
		}
		if e.EndTime <= e.StartTime {
			t.Fatalf("end time for %q = %d (start %d)", start, e.EndTime, e.StartTime)
		}
		if delta := e.EndTime - e.StartTime; delta > 24*3600 {
			t.Fatalf("default duration for %q = %ds", start, delta)
		}
	}
}

func TestParseEventAppDataKeepsValidTimes(t *testing.T) {
	ad := wbxml.N(pageAirSync, "ApplicationData",
		wbxml.T(pageCalendar, "Subject", "lunch"),
		wbxml.T(pageCalendar, "StartTime", "2026-10-21T04:00:00.000Z"),
		wbxml.T(pageCalendar, "EndTime", "2026-10-21T05:00:00.000Z"))
	e := parseEventAppData(ad)
	want := time.Date(2026, 10, 21, 4, 0, 0, 0, time.UTC).Unix()
	if e.StartTime != want {
		t.Fatalf("start = %d, want %d", e.StartTime, want)
	}
	if e.EndTime != want+3600 {
		t.Fatalf("end = %d, want %d", e.EndTime, want+3600)
	}
}

// A Delete command must get a Delete response; without it MIUI Mail's calendar
// hangs until the operation times out.
func TestSyncDeleteAnswersClient(t *testing.T) {
	srv, user, _ := testServer(t)
	calendar, err := srv.Store.FolderByType(user.ID, 8)
	if err != nil {
		t.Fatal(err)
	}
	id, err := srv.Store.InsertEvent(&store.Event{
		UserID:    user.ID,
		FolderID:  calendar.ID,
		UID:       "delete-me",
		Subject:   "lunch",
		StartTime: time.Now().Unix(),
		EndTime:   time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.AddChange(calendar.ID, "add", "event", id); err != nil {
		t.Fatal(err)
	}
	key := syncAsUser(t, srv, user, "0", calendar.ID, nil).ChildText("SyncKey")

	req := syncRequest(key, calendar.ID)
	req.Child("Collections").Child("Collection").Children = append(
		req.Child("Collections").Child("Collection").Children,
		wbxml.N(pageAirSync, "Commands",
			wbxml.N(pageAirSync, "Delete", wbxml.T(pageAirSync, "ServerId", i64toa(id)))))
	doc := postAsUser(t, srv, user, "Sync", req)
	coll := doc.Path("Collections", "Collection")
	deletes := coll.Path("Responses").ChildrenNamed("Delete")
	if len(deletes) != 1 {
		t.Fatalf("delete responses = %d, want 1: %+v", len(deletes), coll)
	}
	if got := deletes[0].ChildText("Status"); got != "1" {
		t.Fatalf("delete status = %q, want 1", got)
	}
	if got := deletes[0].ChildText("ServerId"); got != i64toa(id) {
		t.Fatalf("delete server id = %q, want %d", got, id)
	}
}

// The time zone advertised to clients must be one they can map to a known
// Windows zone; a bare "UTC" blob makes some clients drop the whole item.
func TestDefaultTimeZoneBlobIsNamed(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(defaultTimeZoneBlob())
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 172 {
		t.Fatalf("blob = %d bytes, want 172", len(raw))
	}
	name := make([]byte, 0, 64)
	for i := 4; i < 4+64; i += 2 {
		if raw[i] == 0 {
			break
		}
		name = append(name, raw[i])
	}
	if string(name) != "China Standard Time" {
		t.Fatalf("standard name = %q", name)
	}
}

func TestCalendarRemembersClientTimeZone(t *testing.T) {
	srv, user, _ := testServer(t)
	calendar, err := srv.Store.FolderByType(user.ID, 8)
	if err != nil {
		t.Fatal(err)
	}
	blob := timeZoneBlob("Tokyo Standard Time", -540)
	add := wbxml.N(pageAirSync, "Add",
		wbxml.T(pageAirSync, "ClientId", "tz-1"),
		wbxml.N(pageAirSync, "ApplicationData",
			wbxml.T(pageCalendar, "TimeZone", blob),
			wbxml.T(pageCalendar, "StartTime", "2026-10-21T04:00:00.000Z"),
			wbxml.T(pageCalendar, "EndTime", "2026-10-21T05:00:00.000Z"),
			wbxml.T(pageCalendar, "Subject", "tz")))
	srv.applyClientCommands(user, calendar, wbxml.N(pageAirSync, "Commands", add), false)
	if got := srv.calendarTimeZone(user.ID); got != blob {
		t.Fatalf("calendar time zone was not remembered")
	}
}
