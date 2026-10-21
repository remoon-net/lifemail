package ical

import (
	"strings"
	"testing"
	"time"

	"remoon.net/lifemail/internal/store"
)

// dt builds a UTC unix timestamp from a compact literal.
func dt(t *testing.T, value string) int64 {
	t.Helper()
	parsed, err := time.Parse("20060102T150405Z", value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return parsed.Unix()
}

func TestRRuleMappings(t *testing.T) {
	cases := []struct {
		name string
		rec  store.Recurrence
		want string
	}{
		{
			name: "not recurring",
			rec:  store.Recurrence{},
			want: "",
		},
		{
			name: "weekly on monday and tuesday",
			rec:  store.Recurrence{Type: 1, Interval: 1, DayOfWeek: 2 | 4},
			want: "FREQ=WEEKLY;BYDAY=MO,TU",
		},
		{
			name: "weekly every two weeks until a date",
			rec: store.Recurrence{Type: 1, Interval: 2, DayOfWeek: 1,
				Until: dt(t, "20261231T000000Z")},
			want: "FREQ=WEEKLY;BYDAY=SU;INTERVAL=2;UNTIL=20261231T000000Z",
		},
		{
			name: "monthly on the 15th",
			rec:  store.Recurrence{Type: 2, Interval: 1, DayOfMonth: 15},
			want: "FREQ=MONTHLY;BYMONTHDAY=15",
		},
		{
			name: "monthly second wednesday",
			rec:  store.Recurrence{Type: 3, Interval: 1, DayOfWeek: 8, WeekOfMonth: 2},
			want: "FREQ=MONTHLY;BYDAY=WE;BYSETPOS=2",
		},
		{
			name: "monthly last friday",
			rec:  store.Recurrence{Type: 3, Interval: 1, DayOfWeek: 32, WeekOfMonth: 5},
			want: "FREQ=MONTHLY;BYDAY=FR;BYSETPOS=-1",
		},
		{
			// EAS months are 0-based, iCalendar BYMONTH is 1-based. January is
			// the easiest place to get this wrong, so pin it.
			name: "yearly on january 1st",
			rec:  store.Recurrence{Type: 4, Interval: 1, DayOfMonth: 1, MonthOfYear: 0},
			want: "FREQ=YEARLY;BYMONTH=1;BYMONTHDAY=1",
		},
		{
			name: "yearly on the last saturday of december",
			rec: store.Recurrence{Type: 5, Interval: 1, DayOfWeek: 64, WeekOfMonth: 5,
				MonthOfYear: 11},
			want: "FREQ=YEARLY;BYMONTH=12;BYDAY=SA;BYSETPOS=-1",
		},
		{
			name: "counted occurrences win over until",
			rec: store.Recurrence{Type: 2, Interval: 1, DayOfMonth: 3, Occurrences: 10,
				Until: dt(t, "20261231T000000Z")},
			want: "FREQ=MONTHLY;BYMONTHDAY=3;COUNT=10",
		},
		{
			name: "unknown type is not advertised as repeating",
			rec:  store.Recurrence{Type: 9, Interval: 1},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RRule(tc.rec); got != tc.want {
				t.Fatalf("RRule = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderEventBasics(t *testing.T) {
	event := &store.Event{
		ID:        7,
		UID:       "abc@lifemail",
		Subject:   "Standup",
		Location:  "Room 1",
		Body:      "say hi",
		StartTime: dt(t, "20261021T090000Z"),
		EndTime:   dt(t, "20261021T093000Z"),
		DtStamp:   dt(t, "20261020T120000Z"),
	}
	feed := Feed{Name: "Work", Events: []*store.Event{event}}
	out := feed.Render(time.Unix(dt(t, "20261021T000000Z"), 0))

	for _, want := range []string{
		"BEGIN:VCALENDAR\r\n",
		"VERSION:2.0\r\n",
		"METHOD:PUBLISH\r\n",
		"X-WR-CALNAME:Work\r\n",
		"UID:abc@lifemail\r\n",
		"DTSTAMP:20261020T120000Z\r\n",
		"DTSTART:20261021T090000Z\r\n",
		"DTEND:20261021T093000Z\r\n",
		"SUMMARY:Standup\r\n",
		"LOCATION:Room 1\r\n",
		"DESCRIPTION:say hi\r\n",
		"END:VCALENDAR\r\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "RRULE:") {
		t.Fatal("a non-recurring event must not carry an RRULE")
	}
}

func TestAllDayUsesDateValues(t *testing.T) {
	event := &store.Event{
		UID:       "allday@lifemail",
		Subject:   "Holiday",
		AllDay:    true,
		StartTime: dt(t, "20261021T000000Z"),
		EndTime:   dt(t, "20261022T000000Z"),
		DtStamp:   dt(t, "20261020T120000Z"),
	}
	out := Feed{Events: []*store.Event{event}}.Render(time.Now())
	if !strings.Contains(out, "DTSTART;VALUE=DATE:20261021\r\n") {
		t.Fatalf("all-day start missing:\n%s", out)
	}
	if !strings.Contains(out, "DTEND;VALUE=DATE:20261022\r\n") {
		t.Fatalf("all-day end missing:\n%s", out)
	}
	if strings.Contains(out, "DTSTART:2026") {
		t.Fatalf("all-day event must not use a datetime start:\n%s", out)
	}
}

func TestExceptionsBecomeExdateAndOverrides(t *testing.T) {
	cancelled := dt(t, "20261028T090000Z")
	moved := dt(t, "20261104T090000Z")
	event := &store.Event{
		UID:       "series@lifemail",
		Subject:   "Weekly",
		StartTime: dt(t, "20261021T090000Z"),
		EndTime:   dt(t, "20261021T093000Z"),
		DtStamp:   dt(t, "20261020T120000Z"),
		Recurrence: store.Recurrence{
			Type: 1, Interval: 1, DayOfWeek: 8, Until: dt(t, "20261231T000000Z"),
		},
		Exceptions: []store.Exception{
			{Occurrence: cancelled, Deleted: true},
			{Occurrence: moved, Subject: "Weekly (moved)",
				StartTime: dt(t, "20261104T140000Z"), EndTime: dt(t, "20261104T143000Z")},
		},
	}
	out := Feed{Events: []*store.Event{event}}.Render(time.Now())

	if !strings.Contains(out, "EXDATE:20261028T090000Z\r\n") {
		t.Fatalf("cancelled occurrence is not excluded:\n%s", out)
	}
	if !strings.Contains(out, "RECURRENCE-ID:20261104T090000Z\r\n") {
		t.Fatalf("rescheduled occurrence has no RECURRENCE-ID:\n%s", out)
	}
	if !strings.Contains(out, "SUMMARY:Weekly (moved)\r\n") {
		t.Fatalf("override subject missing:\n%s", out)
	}
	if got := strings.Count(out, "BEGIN:VEVENT"); got != 2 {
		t.Fatalf("VEVENT count = %d, want 2 (series + one override)", got)
	}
}

func TestTextIsEscaped(t *testing.T) {
	event := &store.Event{
		UID:       "esc@lifemail",
		Subject:   "Lunch, then sync; back\\slash",
		Body:      "line one\nline two",
		StartTime: dt(t, "20261021T090000Z"),
		EndTime:   dt(t, "20261021T093000Z"),
		DtStamp:   dt(t, "20261020T120000Z"),
	}
	out := Feed{Events: []*store.Event{event}}.Render(time.Now())
	if !strings.Contains(out, `SUMMARY:Lunch\, then sync\; back\\slash`) {
		t.Fatalf("subject not escaped:\n%s", out)
	}
	if !strings.Contains(out, `DESCRIPTION:line one\nline two`) {
		t.Fatalf("newline not escaped:\n%s", out)
	}
	// No content line may contain a raw newline before the CRLF terminator.
	for _, line := range strings.Split(strings.TrimSuffix(out, "\r\n"), "\r\n") {
		if strings.HasPrefix(line, " ") {
			continue // folding continuation
		}
		if strings.ContainsAny(line, "\r\n") {
			t.Fatalf("raw newline inside a content line: %q", line)
		}
	}
}

func TestLongLinesAreFoldedAt75Octets(t *testing.T) {
	event := &store.Event{
		UID:       "long@lifemail",
		Subject:   strings.Repeat("é", 200), // 2 octets each in UTF-8
		StartTime: dt(t, "20261021T090000Z"),
		EndTime:   dt(t, "20261021T093000Z"),
		DtStamp:   dt(t, "20261020T120000Z"),
	}
	out := Feed{Events: []*store.Event{event}}.Render(time.Now())
	for _, line := range strings.Split(strings.TrimSuffix(out, "\r\n"), "\r\n") {
		if len(line) > 75 {
			t.Fatalf("line is %d octets, limit is 75: %q", len(line), line)
		}
		// Folding must not cut a multi-byte rune in half.
		if !utf8Valid(line) {
			t.Fatalf("fold split a UTF-8 sequence: %q", line)
		}
	}
}

func utf8Valid(s string) bool {
	for _, r := range s {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}

func TestRevisionAndStatusProperties(t *testing.T) {
	event := &store.Event{
		UID:       "props@lifemail",
		Subject:   "Review",
		StartTime: dt(t, "20261021T090000Z"),
		EndTime:   dt(t, "20261021T100000Z"),
		DtStamp:   dt(t, "20261020T120000Z"),

		Sequence:     4,
		LastModified: dt(t, "20261020T180000Z"),

		BusyStatus:     2,
		Sensitivity:    2,
		Reminder:       30,
		OrganizerName:  "Alice",
		OrganizerEmail: "alice@example.com",
		Attendees: []store.Attendee{
			{Name: "Bob", Email: "bob@example.com", Status: 3, Type: 1},
			{Name: "Carol", Email: "carol@example.com", Status: 4},
			{Email: "dave@example.com"},
		},
	}
	out := Feed{Events: []*store.Event{event}}.Render(time.Now())
	// The attendee lines are long enough to be folded, so compare against the
	// logical lines rather than the physical ones.
	unfolded := strings.ReplaceAll(out, "\r\n ", "")

	for _, want := range []string{
		"SEQUENCE:4\r\n",
		"LAST-MODIFIED:20261020T180000Z\r\n",
		"TRANSP:OPAQUE\r\n",
		"CLASS:PRIVATE\r\n",
		"STATUS:CONFIRMED\r\n",
		`ORGANIZER;CN="Alice":mailto:alice@example.com` + "\r\n",
		`ATTENDEE;PARTSTAT=ACCEPTED;CN="Bob";ROLE=REQ-PARTICIPANT:mailto:bob@example.com` + "\r\n",
		`ATTENDEE;PARTSTAT=DECLINED;CN="Carol":mailto:carol@example.com` + "\r\n",
		`ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:dave@example.com` + "\r\n",
		"BEGIN:VALARM\r\n",
		"TRIGGER:-PT30M\r\n",
		"END:VALARM\r\n",
	} {
		if !strings.Contains(unfolded, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestFreeEventIsTransparentAndHasNoAlarm(t *testing.T) {
	event := &store.Event{
		UID:        "free@lifemail",
		Subject:    "Optional",
		StartTime:  dt(t, "20261021T090000Z"),
		EndTime:    dt(t, "20261021T100000Z"),
		DtStamp:    dt(t, "20261020T120000Z"),
		BusyStatus: 0,
		Reminder:   0,
	}
	out := Feed{Events: []*store.Event{event}}.Render(time.Now())
	if !strings.Contains(out, "TRANSP:TRANSPARENT\r\n") {
		t.Fatalf("free event not transparent:\n%s", out)
	}
	if strings.Contains(out, "BEGIN:VALARM") {
		t.Fatalf("event without a reminder must not carry an alarm:\n%s", out)
	}
	if !strings.Contains(out, "SEQUENCE:0\r\n") {
		t.Fatalf("SEQUENCE is mandatory and must still be present:\n%s", out)
	}
}
