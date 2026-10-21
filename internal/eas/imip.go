package eas

import (
	"strings"

	"remoon.net/lifemail/internal/mailparse"
	"remoon.net/lifemail/internal/store"
)

// HandleIMipReply applies an RFC 5546 reply to an event this account organises.
//
// MIUI Mail answers invitations by sending such a message and reports
// MeetingStatus 3 for every outcome, so the calendar payload is the only place
// the real answer appears. Without this a declined meeting stays "received" and
// other clients cannot tell accept from decline.
func (s *Server) HandleIMipReply(user *store.User, parsed *mailparse.Message, raw []byte) {
	ics, ok := calendarReply(parsed)
	if !ok {
		return
	}
	uid, partstat, attendee := parseCalendarReply(ics)
	if uid == "" || partstat == "" {
		return
	}
	if attendee == "" {
		attendee = parsed.FromAddr
	}
	status := meetingStatusForPartStat(partstat)
	if status == 0 {
		return
	}

	event, err := s.Store.EventByUID(user.ID, uid)
	if err != nil || event == nil {
		// Not the organiser of this meeting, or it lives on another account.
		return
	}
	// An invite that was just received already carries status 3, so comparing
	// the meeting status alone is not enough: the attendee entry still has to
	// become accepted.
	attStatus := attendeeStatusForPartStat(partstat)
	changed := false
	if event.MeetingStatus != status {
		event.MeetingStatus = status
		changed = true
	}
	for i := range event.Attendees {
		if strings.EqualFold(event.Attendees[i].Email, attendee) && event.Attendees[i].Status != attStatus {
			event.Attendees[i].Status = attStatus
			changed = true
		}
	}
	if !changed {
		return
	}
	if err := s.Store.UpdateEvent(event); err != nil {
		s.Log.Warn("cannot apply imip reply", "err", err, "uid", uid)
		return
	}
	if _, err := s.Store.AddChange(event.FolderID, "change", "event", event.ID); err != nil {
		s.Log.Warn("cannot record imip change", "err", err, "uid", uid)
	}
	s.Log.Info("applied imip reply", "uid", uid, "partstat", partstat, "attendee", attendee)
}

// hasCalendarPart reports whether the message carries an iCalendar payload of
// any kind, used to keep the auto-responder out of calendar traffic.
func hasCalendarPart(parsed *mailparse.Message) bool {
	if parsed == nil {
		return false
	}
	for _, part := range parsed.Parts {
		if strings.Contains(strings.ToLower(part.ContentType), "text/calendar") {
			return true
		}
	}
	return false
}

// calendarReply returns the body of the first text/calendar part carrying a
// METHOD:REPLY, which is what clients attach when answering an invitation.
func calendarReply(parsed *mailparse.Message) (string, bool) {
	if parsed == nil {
		return "", false
	}
	for _, part := range parsed.Parts {
		if !strings.Contains(strings.ToLower(part.ContentType), "text/calendar") {
			continue
		}
		body := string(part.Data)
		if !strings.Contains(strings.ToUpper(body), "METHOD:REPLY") {
			continue
		}
		return body, true
	}
	return "", false
}

// unfold joins RFC 5545 folded lines: a continuation starts with a space or tab.
// Without this the attendee address is cut in half.
func unfold(ics string) []string {
	ics = strings.ReplaceAll(ics, "\r\n", "\n")
	var out []string
	for _, line := range strings.Split(ics, "\n") {
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') && len(out) > 0 {
			out[len(out)-1] += strings.TrimLeft(line, " \t")
			continue
		}
		out = append(out, line)
	}
	return out
}

// parseCalendarReply pulls the meeting UID, the attendee's PARTSTAT and their
// address out of a reply payload.
func parseCalendarReply(ics string) (uid, partstat, attendee string) {
	for _, line := range unfold(ics) {
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "UID:"):
			uid = strings.TrimSpace(line[len("UID:"):])
		case strings.HasPrefix(upper, "ATTENDEE"):
			if idx := strings.Index(upper, "PARTSTAT="); idx >= 0 {
				rest := line[idx+len("PARTSTAT="):]
				partstat = strings.ToUpper(strings.TrimSpace(strings.SplitN(rest, ";", 2)[0]))
			}
			if idx := strings.Index(upper, "MAILTO:"); idx >= 0 {
				addr := strings.TrimSpace(line[idx+len("MAILTO:"):])
				// Some clients append parameters after the address.
				if semi := strings.Index(addr, ";"); semi >= 0 {
					addr = addr[:semi]
				}
				attendee = addr
			}
		}
	}
	return uid, partstat, attendee
}

// meetingStatusForPartStat maps RFC 5546 PARTSTAT onto MS-ASCAL MeetingStatus.
func meetingStatusForPartStat(partstat string) int {
	switch partstat {
	case "ACCEPTED":
		return 3
	case "DECLINED":
		return 7
	case "TENTATIVE":
		return 9
	}
	return 0
}

// attendeeStatusForPartStat maps PARTSTAT onto EAS AttendeeStatus.
func attendeeStatusForPartStat(partstat string) int {
	switch partstat {
	case "ACCEPTED":
		return 3
	case "DECLINED":
		return 4
	case "TENTATIVE":
		return 2
	}
	return 0
}
