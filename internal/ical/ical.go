// Package ical renders calendar events as an iCalendar (RFC 5545) feed that
// any client can subscribe to by URL.
package ical

import (
	"fmt"
	"strings"
	"time"

	"remoon.net/lifemail/internal/store"
)

// CalendarName is the display name a client shows for the feed.
type Feed struct {
	Name   string
	Events []*store.Event
}

// Render produces the complete VCALENDAR document.
//
// Times are always emitted in UTC, which avoids shipping a VTIMEZONE block and
// the Windows-zone mapping headaches that come with it. Clients convert to the
// viewer's own zone.
func (f Feed) Render(now time.Time) string {
	var b strings.Builder
	write := func(lines ...string) {
		for _, line := range lines {
			b.WriteString(fold(line))
		}
	}

	write("BEGIN:VCALENDAR")
	write("VERSION:2.0")
	write("PRODID:-//lifemail//calendar//EN")
	write("CALSCALE:GREGORIAN")
	write("METHOD:PUBLISH")
	if f.Name != "" {
		write("X-WR-CALNAME:" + escape(f.Name))
	}
	// Subscribers re-read the feed once an hour by default.
	write("X-PUBLISHED-TTL:PT1H")
	write("REFRESH-INTERVAL;VALUE=DURATION:PT1H")

	for _, event := range f.Events {
		write(renderEvent(event, now)...)
	}
	write("END:VCALENDAR")
	return b.String()
}

// renderEvent writes the series itself plus one VEVENT per exception that
// changes anything (a cancelled occurrence becomes an EXDATE instead).
func renderEvent(e *store.Event, now time.Time) []string {
	stamp := utc(e.DtStamp)
	if e.DtStamp == 0 {
		stamp = utc(now.Unix())
	}
	uid := e.UID
	if uid == "" {
		uid = fmt.Sprintf("event-%d@lifemail", e.ID)
	}

	var cancelled []string
	var overrides []*store.Exception
	for i := range e.Exceptions {
		ex := &e.Exceptions[i]
		if ex.Deleted {
			cancelled = append(cancelled, utc(ex.Occurrence))
			continue
		}
		overrides = append(overrides, ex)
	}

	lines := []string{"BEGIN:VEVENT", "UID:" + uid, "DTSTAMP:" + stamp}
	lines = append(lines, revision(e)...)
	if e.AllDay {
		start := time.Unix(e.StartTime, 0).UTC()
		end := time.Unix(e.EndTime, 0).UTC()
		lines = append(lines,
			"DTSTART;VALUE=DATE:"+start.Format("20060102"),
			"DTEND;VALUE=DATE:"+end.Format("20060102"))
	} else {
		lines = append(lines,
			"DTSTART:"+utc(e.StartTime),
			"DTEND:"+utc(e.EndTime))
	}
	lines = append(lines, "SUMMARY:"+escape(e.Subject))
	if e.Location != "" {
		lines = append(lines, "LOCATION:"+escape(e.Location))
	}
	if e.Body != "" {
		lines = append(lines, "DESCRIPTION:"+escape(e.Body))
	}
	lines = append(lines, classification(e)...)
	lines = append(lines, parties(e)...)
	if rule := RRule(e.Recurrence); rule != "" {
		lines = append(lines, "RRULE:"+rule)
	}
	if len(cancelled) > 0 {
		lines = append(lines, "EXDATE:"+strings.Join(cancelled, ","))
	}
	lines = append(lines, alarm(e)...)
	lines = append(lines, "END:VEVENT")

	for _, ex := range overrides {
		lines = append(lines, "BEGIN:VEVENT", "UID:"+uid,
			"RECURRENCE-ID:"+utc(ex.Occurrence),
			"DTSTAMP:"+stamp)
		lines = append(lines, revision(e)...)
		start, end := ex.StartTime, ex.EndTime
		if start == 0 {
			start, end = e.StartTime, e.EndTime
		}
		if e.AllDay {
			lines = append(lines,
				"DTSTART;VALUE=DATE:"+time.Unix(start, 0).UTC().Format("20060102"),
				"DTEND;VALUE=DATE:"+time.Unix(end, 0).UTC().Format("20060102"))
		} else {
			lines = append(lines, "DTSTART:"+utc(start), "DTEND:"+utc(end))
		}
		subject := ex.Subject
		if subject == "" {
			subject = e.Subject
		}
		lines = append(lines, "SUMMARY:"+escape(subject))
		location := ex.Location
		if location == "" {
			location = e.Location
		}
		if location != "" {
			lines = append(lines, "LOCATION:"+escape(location))
		}
		lines = append(lines, classification(e)...)
		lines = append(lines, "END:VEVENT")
	}
	return lines
}

// revision emits the bookkeeping lines a subscriber needs in order to notice
// that its cached copy is out of date. Without them many clients keep showing
// the old time after an event was moved.
func revision(e *store.Event) []string {
	lines := []string{"SEQUENCE:" + itoa(e.Sequence)}
	stamp := e.LastModified
	if stamp == 0 {
		stamp = e.DtStamp
	}
	if stamp > 0 {
		lines = append(lines, "LAST-MODIFIED:"+utc(stamp))
	}
	return lines
}

// classification maps the EAS busy and sensitivity flags onto the properties a
// calendar uses to decide how an event shows up and who may read it.
func classification(e *store.Event) []string {
	var lines []string
	// EAS busy status: 0 free, 1 tentative, 2 busy, 3 out of office. Only a
	// free event leaves the time slot visually open.
	if e.BusyStatus == 0 {
		lines = append(lines, "TRANSP:TRANSPARENT")
	} else {
		lines = append(lines, "TRANSP:OPAQUE")
	}
	switch e.Sensitivity {
	case 1: // personal
		lines = append(lines, "CLASS:PRIVATE")
	case 2:
		lines = append(lines, "CLASS:PRIVATE")
	case 3:
		lines = append(lines, "CLASS:CONFIDENTIAL")
	default:
		lines = append(lines, "CLASS:PUBLIC")
	}
	lines = append(lines, "STATUS:CONFIRMED")
	return lines
}

// parties emits the organiser and attendees, including how each one answered.
func parties(e *store.Event) []string {
	var lines []string
	if e.OrganizerEmail != "" {
		line := "ORGANIZER"
		if e.OrganizerName != "" {
			line += ";CN=" + quote(e.OrganizerName)
		}
		lines = append(lines, line+":mailto:"+e.OrganizerEmail)
	}
	for _, a := range e.Attendees {
		if a.Email == "" {
			continue
		}
		line := "ATTENDEE;PARTSTAT=" + partStat(a.Status)
		if a.Name != "" {
			line += ";CN=" + quote(a.Name)
		}
		// EAS attendee type 1 is the required attendee; everything else is
		// optional, which is the iCalendar default anyway.
		if a.Type == 1 {
			line += ";ROLE=REQ-PARTICIPANT"
		}
		lines = append(lines, line+":mailto:"+a.Email)
	}
	return lines
}

// alarm turns the EAS reminder (minutes before the start) into a display
// alarm. A zero reminder means the event has none.
func alarm(e *store.Event) []string {
	if e.Reminder <= 0 {
		return nil
	}
	action := "ACTION:DISPLAY"
	return []string{
		"BEGIN:VALARM",
		action,
		"DESCRIPTION:" + escape(e.Subject),
		"TRIGGER:-PT" + itoa(e.Reminder) + "M",
		"END:VALARM",
	}
}

// partStat maps EAS AttendeeStatus onto the RFC 5546 participation status.
func partStat(status int) string {
	switch status {
	case 2:
		return "TENTATIVE"
	case 3:
		return "ACCEPTED"
	case 4:
		return "DECLINED"
	}
	return "NEEDS-ACTION"
}

// quote wraps a parameter value that may contain separators.
func quote(value string) string {
	value = strings.ReplaceAll(value, `"`, "")
	return `"` + value + `"`
}

// weekdays maps the EAS DayOfWeek bit mask to RFC 5545 day names, in the order
// a calendar would list them.
var weekdays = []struct {
	bit  int
	name string
}{
	{1, "SU"}, {2, "MO"}, {4, "TU"}, {8, "WE"}, {16, "TH"}, {32, "FR"}, {64, "SA"},
}

// RRule renders a recurrence rule, or "" when the event does not repeat.
func RRule(r store.Recurrence) string {
	if !r.Active() {
		return ""
	}
	var parts []string
	switch r.Type {
	case 1:
		parts = append(parts, "FREQ=WEEKLY")
		if days := byDay(r.DayOfWeek); len(days) > 0 {
			parts = append(parts, "BYDAY="+strings.Join(days, ","))
		}
	case 2:
		parts = append(parts, "FREQ=MONTHLY", "BYMONTHDAY="+itoa(r.DayOfMonth))
	case 3:
		parts = append(parts, "FREQ=MONTHLY")
		if days := byDay(r.DayOfWeek); len(days) > 0 {
			parts = append(parts, "BYDAY="+strings.Join(days, ","))
		}
		if pos := setPos(r.WeekOfMonth); pos != "" {
			parts = append(parts, "BYSETPOS="+pos)
		}
	case 4:
		parts = append(parts, "FREQ=YEARLY",
			"BYMONTH="+itoa(r.MonthOfYear+1), // EAS months are 0-based
			"BYMONTHDAY="+itoa(r.DayOfMonth))
	case 5:
		parts = append(parts, "FREQ=YEARLY", "BYMONTH="+itoa(r.MonthOfYear+1))
		if days := byDay(r.DayOfWeek); len(days) > 0 {
			parts = append(parts, "BYDAY="+strings.Join(days, ","))
		}
		if pos := setPos(r.WeekOfMonth); pos != "" {
			parts = append(parts, "BYSETPOS="+pos)
		}
	default:
		return ""
	}
	if r.Interval > 1 {
		parts = append(parts, "INTERVAL="+itoa(r.Interval))
	}
	// UNTIL and COUNT are mutually exclusive in RFC 5545. Clients only ever
	// set one, but if a stale value of both survived, a count is the safer
	// choice: it cannot silently truncate the series at an old date.
	if r.Occurrences > 0 {
		parts = append(parts, "COUNT="+itoa(r.Occurrences))
	} else if r.Until > 0 {
		parts = append(parts, "UNTIL="+utc(r.Until))
	}
	return strings.Join(parts, ";")
}

// byDay expands the bit mask into day names.
func byDay(mask int) []string {
	var out []string
	for _, day := range weekdays {
		if mask&day.bit != 0 {
			out = append(out, day.name)
		}
	}
	return out
}

// setPos maps EAS WeekOfMonth to BYSETPOS. EAS uses 5 for "the last one",
// which in RFC 5545 is -1.
func setPos(week int) string {
	switch {
	case week >= 1 && week <= 4:
		return itoa(week)
	case week == 5:
		return "-1"
	}
	return ""
}

// utc formats a unix time the way iCalendar wants it.
func utc(unix int64) string {
	return time.Unix(unix, 0).UTC().Format("20060102T150405Z")
}

// escape handles the characters RFC 5545 requires to be escaped in text.
func escape(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, ";", `\;`)
	value = strings.ReplaceAll(value, ",", `\,`)
	value = strings.ReplaceAll(value, "\r\n", `\n`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	value = strings.ReplaceAll(value, "\r", `\n`)
	return value
}

// fold wraps a content line at 75 octets without splitting a UTF-8 sequence,
// and terminates it with CRLF.
func fold(line string) string {
	const limit = 74 // leave room for the space continuation marker
	var b strings.Builder
	width := 0
	for _, r := range line {
		size := len(string(r))
		if width+size > limit {
			b.WriteString("\r\n ")
			width = 0
		}
		b.WriteRune(r)
		width += size
	}
	b.WriteString("\r\n")
	return b.String()
}

func itoa(v int) string { return fmt.Sprintf("%d", v) }
