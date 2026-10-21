package eas

import (
	"testing"
	"time"

	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/wbxml"
)

// A weekly rule has to survive parse -> render with the fields the client uses,
// and must not grow stray monthly fields on the way out.
func TestRecurrenceWeeklyRoundTrip(t *testing.T) {
	ad := wbxml.N(pageAirSync, "ApplicationData",
		wbxml.T(pageCalendar, "Subject", "standup"),
		wbxml.T(pageCalendar, "StartTime", "20261021T020000Z"),
		wbxml.T(pageCalendar, "EndTime", "20261021T023000Z"),
		wbxml.N(pageCalendar, "Recurrence",
			wbxml.T(pageCalendar, "RecurrenceType", "1"),
			wbxml.T(pageCalendar, "RecurrenceInterval", "2"),
			wbxml.T(pageCalendar, "RecurrenceDayOfWeek", "4"),
			wbxml.T(pageCalendar, "RecurrenceUntil", "20261231T000000Z"),
		))
	e := parseEventAppData(ad)
	if !e.Recurrence.Active() {
		t.Fatal("weekly rule was not parsed")
	}
	if e.Recurrence.Type != 1 || e.Recurrence.Interval != 2 || e.Recurrence.DayOfWeek != 4 {
		t.Fatalf("parsed rule = %+v", e.Recurrence)
	}
	if want := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC).Unix(); e.Recurrence.Until != want {
		t.Fatalf("until = %d, want %d", e.Recurrence.Until, want)
	}

	node := recurrenceNode(e.Recurrence)
	if got := node.ChildText("RecurrenceInterval"); got != "2" {
		t.Fatalf("interval = %q", got)
	}
	// RecurrenceUntil is MS-ASDTIME: clients choke on the ISO form here.
	if got := node.ChildText("RecurrenceUntil"); got != "20261231T000000Z" {
		t.Fatalf("until rendered as %q, want compact MS-ASDTIME", got)
	}
	if node.Child("RecurrenceDayOfMonth") != nil {
		t.Fatal("weekly rule rendered a day-of-month")
	}
}

// Each rule type carries a different subset of fields; rendering the union would
// leave clients guessing.
func TestRecurrenceFieldSelection(t *testing.T) {
	cases := []struct {
		name   string
		rule   store.Recurrence
		expect map[string]string
		absent []string
	}{
		{
			name:   "monthly by date",
			rule:   store.Recurrence{Type: 2, Interval: 1, DayOfMonth: 15, Occurrences: 6},
			expect: map[string]string{"RecurrenceDayOfMonth": "15", "RecurrenceOccurrences": "6"},
			absent: []string{"RecurrenceDayOfWeek", "RecurrenceWeekOfMonth"},
		},
		{
			name: "monthly nth weekday",
			rule: store.Recurrence{Type: 3, Interval: 1, DayOfWeek: 4, WeekOfMonth: 2},
			expect: map[string]string{
				"RecurrenceDayOfWeek":   "4",
				"RecurrenceWeekOfMonth": "2",
			},
			absent: []string{"RecurrenceDayOfMonth", "RecurrenceMonthOfYear"},
		},
		{
			name: "yearly by date",
			rule: store.Recurrence{Type: 4, Interval: 1, DayOfMonth: 1, MonthOfYear: 9},
			expect: map[string]string{
				"RecurrenceDayOfMonth":  "1",
				"RecurrenceMonthOfYear": "9",
			},
			absent: []string{"RecurrenceDayOfWeek"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := recurrenceNode(tc.rule)
			for name, want := range tc.expect {
				if got := node.ChildText(name); got != want {
					t.Fatalf("%s = %q, want %q", name, got, want)
				}
			}
			for _, name := range tc.absent {
				if node.Child(name) != nil {
					t.Fatalf("%s must not be rendered for this rule", name)
				}
			}
		})
	}
}

// Until and Occurrences are mutually exclusive; the client picks the earlier
// limit when both are present, so only one may be sent.
func TestRecurrenceUntilWinsOverOccurrences(t *testing.T) {
	node := recurrenceNode(store.Recurrence{Type: 0, Interval: 1, Until: 1798761600, Occurrences: 10})
	if node.Child("RecurrenceOccurrences") != nil {
		t.Fatal("occurrences must not be sent together with until")
	}
	if got := node.ChildText("RecurrenceUntil"); got == "" {
		t.Fatal("until is missing")
	}
}

// An event without a rule must not grow a Recurrence element.
func TestEventAppDataOmitsRecurrenceWhenNotRepeating(t *testing.T) {
	e := &store.Event{
		UID:       "no-rule",
		Subject:   "one off",
		StartTime: 1792549800,
		EndTime:   1792553400,
		DtStamp:   1792549800,
	}
	ad := eventAppData(e, defaultTimeZoneBlob())
	if ad.Child("Recurrence") != nil {
		t.Fatal("non-repeating event rendered a Recurrence element")
	}
}

// The rule must survive a real database round trip, defaults included.
func TestRecurrencePersistsThroughStore(t *testing.T) {
	srv, user, folder := testServer(t)
	e := &store.Event{
		UserID:    user.ID,
		FolderID:  folder.ID,
		UID:       "recurring-1",
		Subject:   "standup",
		StartTime: 1792549800,
		EndTime:   1792553400,
		Recurrence: store.Recurrence{
			Type:        1,
			Interval:    2,
			DayOfWeek:   4,
			Occurrences: 10,
		},
	}
	id, err := srv.Store.InsertEvent(e)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := srv.Store.EventByID(id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Recurrence != e.Recurrence {
		t.Fatalf("recurrence = %+v, want %+v", got.Recurrence, e.Recurrence)
	}

	// Updating back to "does not repeat" must clear every field.
	got.Recurrence = store.Recurrence{}
	if err := srv.Store.UpdateEvent(got); err != nil {
		t.Fatalf("update: %v", err)
	}
	again, err := srv.Store.EventByID(id)
	if err != nil {
		t.Fatalf("reload after update: %v", err)
	}
	if again.Recurrence.Active() {
		t.Fatalf("rule was not cleared: %+v", again.Recurrence)
	}
}
