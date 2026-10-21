package eas

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"net/mail"
	"strings"
	"time"

	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/wbxml"
)

// newEventUID mints a stable calendar UID for events created by clients that
// do not send one.
func newEventUID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return i64toa(time.Now().UnixNano()) + "@lifemail"
	}
	return hex.EncodeToString(b[:]) + "@lifemail"
}

// bodyPref mirrors AirSyncBase BodyPreference.
type bodyPref struct {
	Type       int // 1 plain, 2 HTML, 4 MIME
	Truncation int
	AllOrNone  bool

	// BodyPartPreference asks for one message part only. When PartType is set a
	// BodyPart element is returned instead of Body.
	PartType       int // 1 plain, 2 HTML, 4 MIME
	PartTruncation int
	PartAllOrNone  bool
}

func bodyPrefFrom(options *wbxml.Node) bodyPref {
	pref := bodyPref{Type: 2}
	if options == nil {
		return pref
	}
	if bp := options.Child("BodyPreference"); bp != nil {
		pref.Type = atoiDefault(bp.ChildText("Type"), 2)
		pref.Truncation = atoiDefault(bp.ChildText("Truncation"), 0)
		pref.AllOrNone = bp.ChildText("AllOrNone") == "1"
	}
	if bp := options.Child("BodyPartPreference"); bp != nil {
		pref.PartType = atoiDefault(bp.ChildText("Type"), 0)
		pref.PartTruncation = atoiDefault(bp.ChildText("Truncation"), 0)
		pref.PartAllOrNone = bp.ChildText("AllOrNone") == "1"
	}
	return pref
}

// bodyData returns the payload for a body type and whether it was cut short.
//
// A MIME payload is truncated on the raw bytes before base64 encoding: cutting
// the encoded text would hand the client a payload it cannot decode.
func bodyData(m *store.Message, bodyType, truncation int) (data string, truncated bool) {
	switch bodyType {
	case 4:
		if len(m.Raw) == 0 {
			return "", false
		}
		raw := m.Raw
		if truncation > 0 && len(raw) > truncation {
			raw = raw[:truncation]
			truncated = true
		}
		return base64.StdEncoding.EncodeToString(raw), truncated
	case 2:
		data = m.BodyHTML
	default:
		data = m.BodyText
	}
	if truncation > 0 && len(data) > truncation {
		data = truncateUTF8(data, truncation)
		truncated = true
	}
	return data, truncated
}

// bodyNode renders the classic Body element.
func bodyNode(m *store.Message, pref bodyPref) *wbxml.Node {
	bodyType := 1
	switch {
	case pref.Type == 4 && len(m.Raw) > 0:
		bodyType = 4
	case pref.Type == 2 && m.BodyHTML != "":
		bodyType = 2
	case m.BodyText == "" && m.BodyHTML != "":
		bodyType = 2
	}
	data, truncated := bodyData(m, bodyType, pref.Truncation)

	body := wbxml.N(pageAirSyncBase, "Body")
	body.Children = append(body.Children,
		wbxml.T(pageAirSyncBase, "Type", itoa(bodyType)),
		wbxml.T(pageAirSyncBase, "EstimatedDataSize", itoa(len(data))))
	if truncated {
		body.Children = append(body.Children, wbxml.T(pageAirSyncBase, "Truncated", "1"))
	}
	if data != "" {
		body.Children = append(body.Children, wbxml.T(pageAirSyncBase, "Data", data))
	}
	return body
}

// bodyPartNode renders the BodyPart element, which is required whenever the
// request carried a BodyPartPreference.
func bodyPartNode(m *store.Message, pref bodyPref) *wbxml.Node {
	bodyType := pref.PartType
	if bodyType != 2 && bodyType != 4 {
		bodyType = 1
	}
	data, truncated := bodyData(m, bodyType, pref.PartTruncation)

	node := wbxml.N(pageAirSyncBase, "BodyPart",
		wbxml.T(pageAirSyncBase, "Status", "1"),
		wbxml.T(pageAirSyncBase, "Type", itoa(bodyType)),
		wbxml.T(pageAirSyncBase, "EstimatedDataSize", itoa(len(data))))
	if truncated {
		node.Children = append(node.Children, wbxml.T(pageAirSyncBase, "Truncated", "1"))
	}
	if data != "" {
		node.Children = append(node.Children, wbxml.T(pageAirSyncBase, "Data", data))
	}
	return node
}

func formatAddress(name, addr string) string {
	if name == "" {
		return addr
	}
	return (&mail.Address{Name: name, Address: addr}).String()
}

func emailAppData(m *store.Message, attachments []*store.Attachment, pref bodyPref) *wbxml.Node {
	ad := wbxml.N(pageAirSync, "ApplicationData")

	if pref.PartType > 0 {
		ad.Children = append(ad.Children, bodyPartNode(m, pref))
	} else {
		ad.Children = append(ad.Children, bodyNode(m, pref))
	}

	ad.Children = append(ad.Children,
		wbxml.T(pageEmail, "Subject", m.Subject),
		wbxml.T(pageEmail, "From", formatAddress(m.FromName, m.FromAddr)),
		wbxml.T(pageEmail, "To", m.ToAddr),
		wbxml.T(pageEmail, "Cc", m.CcAddr),
		wbxml.T(pageEmail, "DisplayTo", m.ToAddr),
		wbxml.T(pageEmail, "DateReceived", formatEasTime(time.Unix(m.DateReceived, 0))),
		wbxml.T(pageEmail, "Importance", itoa(m.Importance)),
		wbxml.T(pageEmail, "MessageClass", m.MessageClass),
		wbxml.T(pageEmail, "Read", boolString(m.IsRead)),
	)

	// Email2: Sender only when it differs from From (a delegate sent this on
	// someone's behalf), ReceivedAsBcc when this account got it through Bcc.
	if m.SenderAddr != "" && !strings.EqualFold(m.SenderAddr, m.FromAddr) {
		ad.Children = append(ad.Children,
			wbxml.T(pageEmail2, "Sender", formatAddress(m.SenderName, m.SenderAddr)))
	}
	if m.ReceivedAsBcc {
		ad.Children = append(ad.Children, wbxml.T(pageEmail2, "ReceivedAsBcc", "1"))
	}
	// ConversationId lets the client group a thread itself; the server does not
	// maintain a conversation index or ConversationMode collections.
	if m.ConversationID != "" {
		ad.Children = append(ad.Children, wbxml.T(pageEmail2, "ConversationId", m.ConversationID))
	}

	// FlagStatus lives on the Email page; inside <Flag> the element is the
	// same token the spec calls Status.
	flagStatus := "0"
	if m.IsFlagged {
		flagStatus = "2"
	}
	flag := wbxml.N(pageEmail, "Flag", wbxml.T(pageEmail, "FlagStatus", flagStatus))
	if m.IsFlagged {
		flag.Children = append(flag.Children, wbxml.T(pageEmail, "FlagType", "FollowUp"))
	}
	ad.Children = append(ad.Children, flag)

	if len(attachments) > 0 {
		list := wbxml.N(pageAirSyncBase, "Attachments")
		for _, a := range attachments {
			att := wbxml.N(pageAirSyncBase, "Attachment")
			att.Children = append(att.Children,
				wbxml.T(pageAirSyncBase, "DisplayName", a.FileName),
				wbxml.T(pageAirSyncBase, "FileReference", attachmentRef(a.MessageID, a.ID)),
				wbxml.T(pageAirSyncBase, "Method", "1"),
				wbxml.T(pageAirSyncBase, "EstimatedDataSize", i64toa(a.Size)),
				wbxml.T(pageAirSyncBase, "ContentType", a.ContentType),
			)
			// Images embedded in the HTML body are referenced as cid:<ContentId>.
			// Without the ContentId the client cannot match them up and renders
			// the picture as a blank box.
			if a.ContentID != "" {
				att.Children = append(att.Children,
					wbxml.T(pageAirSyncBase, "ContentId", a.ContentID))
			}
			if a.IsInline || a.ContentID != "" {
				att.Children = append(att.Children, wbxml.T(pageAirSyncBase, "IsInline", "1"))
			}
			list.Children = append(list.Children, att)
		}
		ad.Children = append(ad.Children, list)
	}
	return ad
}

func attachmentRef(messageID, attachmentID int64) string {
	return i64toa(messageID) + ":" + i64toa(attachmentID)
}

func contactAppData(c *store.Contact) *wbxml.Node {
	ad := wbxml.N(pageAirSync, "ApplicationData")
	fields := [][2]string{
		{"FirstName", c.FirstName},
		{"LastName", c.LastName},
		{"MiddleName", c.MiddleName},
		{"FileAs", c.FileAs},
		{"CompanyName", c.Company},
		{"Department", c.Department},
		{"JobTitle", c.JobTitle},
		{"Email1Address", c.Email1},
		{"Email2Address", c.Email2},
		{"Email3Address", c.Email3},
		{"HomePhoneNumber", c.HomePhone},
		{"Home2PhoneNumber", c.Home2Phone},
		{"MobilePhoneNumber", c.MobilePhone},
		{"BusinessPhoneNumber", c.BusinessPhone},
		{"BusinessFaxNumber", c.BusinessFax},
		{"HomeAddressStreet", c.HomeStreet},
		{"HomeAddressCity", c.HomeCity},
		{"HomeAddressState", c.HomeState},
		{"HomeAddressPostalCode", c.HomePostalCode},
		{"HomeAddressCountry", c.HomeCountry},
		{"BusinessAddressStreet", c.BusinessStreet},
		{"BusinessAddressCity", c.BusinessCity},
		{"BusinessAddressState", c.BusinessState},
		{"BusinessAddressPostalCode", c.BusinessPostalCode},
		{"BusinessAddressCountry", c.BusinessCountry},
	}
	for _, f := range fields {
		if f[1] != "" {
			ad.Children = append(ad.Children, wbxml.T(pageContacts, f[0], f[1]))
		}
	}
	if c.Body != "" {
		body := wbxml.N(pageAirSyncBase, "Body")
		body.Children = []*wbxml.Node{
			wbxml.T(pageAirSyncBase, "Type", "1"),
			wbxml.T(pageAirSyncBase, "EstimatedDataSize", itoa(len(c.Body))),
			wbxml.T(pageAirSyncBase, "Data", c.Body),
		}
		ad.Children = append(ad.Children, body)
	}
	return ad
}

// formatEasCalendarTime renders a calendar timestamp in the MS-ASDTIME form
// (YYYYMMDDThhmmssZ). Clients send that form themselves and MIUI Mail throws a
// NumberFormatException on the ISO form that formatEasTime produces for mail.
func formatEasCalendarTime(unix int64) string {
	return time.Unix(unix, 0).UTC().Format("20060102T150405Z")
}

// parseRecurrence reads an MS-ASCAL <Recurrence> element. Absent children fall
// back to the protocol defaults so a partial rule still round-trips; a missing
// RecurrenceType means the client did not send a rule at all.
func parseRecurrence(node *wbxml.Node) store.Recurrence {
	r := store.Recurrence{
		Type:     atoiDefault(node.ChildText("RecurrenceType"), 0),
		Interval: atoiDefault(node.ChildText("RecurrenceInterval"), 1),
	}
	if r.Interval < 1 {
		r.Interval = 1
	}
	if v := node.ChildText("RecurrenceUntil"); v != "" {
		if t := parseEasTime(v); !t.IsZero() {
			r.Until = t.Unix()
		}
	}
	r.Occurrences = atoiDefault(node.ChildText("RecurrenceOccurrences"), 0)
	r.DayOfWeek = atoiDefault(node.ChildText("RecurrenceDayOfWeek"), 0)
	r.DayOfMonth = atoiDefault(node.ChildText("RecurrenceDayOfMonth"), 0)
	r.WeekOfMonth = atoiDefault(node.ChildText("RecurrenceWeekOfMonth"), 0)
	r.MonthOfYear = atoiDefault(node.ChildText("RecurrenceMonthOfYear"), 0)
	return r
}

// recurrenceNode renders only the fields the rule type actually uses. Sending
// stray DayOfMonth on a weekly rule confuses some clients.
func recurrenceNode(r store.Recurrence) *wbxml.Node {
	node := wbxml.N(pageCalendar, "Recurrence",
		wbxml.T(pageCalendar, "RecurrenceType", itoa(r.Type)),
		wbxml.T(pageCalendar, "RecurrenceInterval", itoa(r.Interval)))
	add := func(name, value string) {
		node.Children = append(node.Children, wbxml.T(pageCalendar, name, value))
	}
	switch r.Type {
	case 1: // weekly
		if r.DayOfWeek != 0 {
			add("RecurrenceDayOfWeek", itoa(r.DayOfWeek))
		}
	case 2: // monthly, fixed day of month
		add("RecurrenceDayOfMonth", itoa(r.DayOfMonth))
	case 3: // monthly, nth weekday
		add("RecurrenceDayOfWeek", itoa(r.DayOfWeek))
		add("RecurrenceWeekOfMonth", itoa(r.WeekOfMonth))
	case 4: // yearly, fixed date
		add("RecurrenceDayOfMonth", itoa(r.DayOfMonth))
		add("RecurrenceMonthOfYear", itoa(r.MonthOfYear))
	case 5: // yearly, nth weekday of a month
		add("RecurrenceDayOfWeek", itoa(r.DayOfWeek))
		add("RecurrenceWeekOfMonth", itoa(r.WeekOfMonth))
		add("RecurrenceMonthOfYear", itoa(r.MonthOfYear))
	}
	// Until and Occurrences are mutually exclusive: a client that sees both may
	// apply the earlier limit.
	if r.Until > 0 {
		add("RecurrenceUntil", formatEasCalendarTime(r.Until))
	} else if r.Occurrences > 0 {
		add("RecurrenceOccurrences", itoa(r.Occurrences))
	}
	return node
}

// parseExceptions reads the per-occurrence overrides of a recurring event.
//
// MS-ASCAL puts Exceptions directly under ApplicationData, not under
// Recurrence; a client that nests it inside Recurrence is tolerated too.
func parseExceptions(ad *wbxml.Node) []store.Exception {
	if ad == nil {
		return nil
	}
	list := ad.Child("Exceptions")
	if list == nil {
		if recurrence := ad.Child("Recurrence"); recurrence != nil {
			list = recurrence.Child("Exceptions")
		}
	}
	if list == nil {
		return nil
	}
	var out []store.Exception
	for _, node := range list.ChildrenNamed("Exception") {
		ex := store.Exception{
			Deleted:       node.ChildText("Deleted") == "1",
			Subject:       node.ChildText("Subject"),
			Location:      node.ChildText("Location"),
			AllDay:        node.ChildText("AllDayEvent") == "1",
			BusyStatus:    atoiDefault(node.ChildText("BusyStatus"), 0),
			Sensitivity:   atoiDefault(node.ChildText("Sensitivity"), 0),
			Reminder:      atoiDefault(node.ChildText("Reminder"), 0),
			MeetingStatus: atoiDefault(node.ChildText("MeetingStatus"), 0),
		}
		if t := parseEasTime(node.ChildText("ExceptionStartTime")); !t.IsZero() {
			ex.Occurrence = t.Unix()
		}
		if t := parseEasTime(node.ChildText("StartTime")); !t.IsZero() {
			ex.StartTime = t.Unix()
		}
		if t := parseEasTime(node.ChildText("EndTime")); !t.IsZero() {
			ex.EndTime = t.Unix()
		}
		if body := node.Child("Body"); body != nil {
			if data := body.ChildText("Data"); data != "" {
				ex.Body = data
			} else {
				ex.Body = body.Text
			}
		}
		if ex.Occurrence == 0 {
			continue
		}
		out = append(out, ex)
	}
	return out
}

// exceptionsNode renders the occurrence overrides of a recurring event. Only
// fields that actually differ are sent; a client then keeps the series values
// for everything else.
func exceptionsNode(list []store.Exception) *wbxml.Node {
	node := wbxml.N(pageCalendar, "Exceptions")
	for _, ex := range list {
		item := wbxml.N(pageCalendar, "Exception",
			wbxml.T(pageCalendar, "ExceptionStartTime", formatEasCalendarTime(ex.Occurrence)))
		if ex.Deleted {
			item.Children = append(item.Children,
				wbxml.T(pageCalendar, "Deleted", "1"))
			node.Children = append(node.Children, item)
			continue
		}
		if ex.StartTime > 0 {
			item.Children = append(item.Children,
				wbxml.T(pageCalendar, "StartTime", formatEasCalendarTime(ex.StartTime)))
		}
		if ex.EndTime > 0 {
			item.Children = append(item.Children,
				wbxml.T(pageCalendar, "EndTime", formatEasCalendarTime(ex.EndTime)))
		}
		if ex.Subject != "" {
			item.Children = append(item.Children, wbxml.T(pageCalendar, "Subject", ex.Subject))
		}
		if ex.Location != "" {
			item.Children = append(item.Children, wbxml.T(pageCalendar, "Location", ex.Location))
		}
		if ex.AllDay {
			item.Children = append(item.Children, wbxml.T(pageCalendar, "AllDayEvent", "1"))
		}
		if ex.Reminder > 0 {
			item.Children = append(item.Children, wbxml.T(pageCalendar, "Reminder", itoa(ex.Reminder)))
		}
		if ex.BusyStatus > 0 {
			item.Children = append(item.Children, wbxml.T(pageCalendar, "BusyStatus", itoa(ex.BusyStatus)))
		}
		if ex.MeetingStatus > 0 {
			item.Children = append(item.Children, wbxml.T(pageCalendar, "MeetingStatus", itoa(ex.MeetingStatus)))
		}
		node.Children = append(node.Children, item)
	}
	return node
}

func eventAppData(e *store.Event, timeZone string) *wbxml.Node {
	ad := wbxml.N(pageAirSync, "ApplicationData")
	ad.Children = append(ad.Children,
		wbxml.T(pageCalendar, "TimeZone", timeZone),
		wbxml.T(pageCalendar, "AllDayEvent", boolString(e.AllDay)),
		wbxml.T(pageCalendar, "StartTime", formatEasCalendarTime(e.StartTime)),
		wbxml.T(pageCalendar, "EndTime", formatEasCalendarTime(e.EndTime)),
		wbxml.T(pageCalendar, "Subject", e.Subject),
		wbxml.T(pageCalendar, "Location", e.Location),
		wbxml.T(pageCalendar, "UID", e.UID),
		wbxml.T(pageCalendar, "DtStamp", formatEasCalendarTime(e.DtStamp)),
		wbxml.T(pageCalendar, "BusyStatus", itoa(e.BusyStatus)),
		wbxml.T(pageCalendar, "Sensitivity", itoa(e.Sensitivity)),
		wbxml.T(pageCalendar, "MeetingStatus", itoa(e.MeetingStatus)),
		wbxml.T(pageCalendar, "Reminder", itoa(e.Reminder)),
	)
	if e.Recurrence.Active() {
		ad.Children = append(ad.Children, recurrenceNode(e.Recurrence))
	}
	if len(e.Exceptions) > 0 {
		ad.Children = append(ad.Children, exceptionsNode(e.Exceptions))
	}
	if e.OrganizerName != "" || e.OrganizerEmail != "" {
		ad.Children = append(ad.Children,
			wbxml.T(pageCalendar, "OrganizerName", e.OrganizerName),
			wbxml.T(pageCalendar, "OrganizerEmail", e.OrganizerEmail))
	}
	if len(e.Attendees) > 0 {
		list := wbxml.N(pageCalendar, "Attendees")
		for _, a := range e.Attendees {
			att := wbxml.N(pageCalendar, "Attendee")
			if a.Email != "" {
				att.Children = append(att.Children, wbxml.T(pageCalendar, "Email", a.Email))
			}
			if a.Name != "" {
				att.Children = append(att.Children, wbxml.T(pageCalendar, "Name", a.Name))
			}
			att.Children = append(att.Children,
				wbxml.T(pageCalendar, "AttendeeStatus", itoa(a.Status)),
				wbxml.T(pageCalendar, "AttendeeType", itoa(a.Type)))
			list.Children = append(list.Children, att)
		}
		ad.Children = append(ad.Children, list)
	}
	if e.Body != "" {
		body := wbxml.N(pageAirSyncBase, "Body")
		body.Children = []*wbxml.Node{
			wbxml.T(pageAirSyncBase, "Type", "1"),
			wbxml.T(pageAirSyncBase, "EstimatedDataSize", itoa(len(e.Body))),
			wbxml.T(pageAirSyncBase, "Data", e.Body),
		}
		ad.Children = append(ad.Children, body)
	}
	return ad
}

// parseEmailAppData applies an Email ApplicationData element to m.
func parseEmailAppData(ad *wbxml.Node, m *store.Message) {
	if ad == nil {
		return
	}
	if v := ad.ChildText("Subject"); v != "" {
		m.Subject = v
	}
	if v := ad.ChildText("From"); v != "" {
		if addr, err := mail.ParseAddress(v); err == nil {
			m.FromAddr = addr.Address
			m.FromName = addr.Name
		} else {
			m.FromAddr = v
		}
	}
	if v := ad.ChildText("To"); v != "" {
		m.ToAddr = v
	}
	if v := ad.ChildText("Cc"); v != "" {
		m.CcAddr = v
	}
	if v := ad.ChildText("DateReceived"); v != "" {
		if t := parseEasTime(v); !t.IsZero() {
			m.DateReceived = t.Unix()
		}
	}
	if v := ad.ChildText("Importance"); v != "" {
		m.Importance = atoiDefault(v, 1)
	}
	if v := ad.ChildText("Read"); v != "" {
		m.IsRead = v == "1"
	}
	if v := ad.ChildText("MessageClass"); v != "" {
		m.MessageClass = v
	}
	// Flag may arrive as <Flag><Status/></Flag> or, on older clients, as a
	// bare <FlagStatus/> child.
	if flag := ad.Child("Flag"); flag != nil {
		status := flag.ChildText("FlagStatus")
		if status == "" {
			status = flag.ChildText("Status")
		}
		m.IsFlagged = status != "" && status != "0"
	} else if v := ad.ChildText("FlagStatus"); v != "" {
		m.IsFlagged = v != "0"
	}
	if body := ad.Child("Body"); body != nil {
		typ := atoiDefault(body.ChildText("Type"), 1)
		data := body.ChildText("Data")
		switch typ {
		case 2:
			m.BodyHTML = data
		case 4:
			if raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(data)); err == nil {
				m.Raw = raw
			}
		default:
			m.BodyText = data
		}
	}
}

func parseContactAppData(ad *wbxml.Node) *store.Contact {
	c := &store.Contact{}
	if ad == nil {
		return c
	}
	c.FirstName = ad.ChildText("FirstName")
	c.LastName = ad.ChildText("LastName")
	c.MiddleName = ad.ChildText("MiddleName")
	c.FileAs = ad.ChildText("FileAs")
	c.Company = ad.ChildText("CompanyName")
	c.Department = ad.ChildText("Department")
	c.JobTitle = ad.ChildText("JobTitle")
	c.Email1 = ad.ChildText("Email1Address")
	c.Email2 = ad.ChildText("Email2Address")
	c.Email3 = ad.ChildText("Email3Address")
	c.HomePhone = ad.ChildText("HomePhoneNumber")
	c.Home2Phone = ad.ChildText("Home2PhoneNumber")
	c.MobilePhone = ad.ChildText("MobilePhoneNumber")
	c.BusinessPhone = ad.ChildText("BusinessPhoneNumber")
	c.BusinessFax = ad.ChildText("BusinessFaxNumber")
	c.HomeStreet = ad.ChildText("HomeAddressStreet")
	c.HomeCity = ad.ChildText("HomeAddressCity")
	c.HomeState = ad.ChildText("HomeAddressState")
	c.HomePostalCode = ad.ChildText("HomeAddressPostalCode")
	c.HomeCountry = ad.ChildText("HomeAddressCountry")
	c.BusinessStreet = ad.ChildText("BusinessAddressStreet")
	c.BusinessCity = ad.ChildText("BusinessAddressCity")
	c.BusinessState = ad.ChildText("BusinessAddressState")
	c.BusinessPostalCode = ad.ChildText("BusinessAddressPostalCode")
	c.BusinessCountry = ad.ChildText("BusinessAddressCountry")
	if body := ad.Child("Body"); body != nil {
		if data := body.ChildText("Data"); data != "" {
			c.Body = data
		} else {
			c.Body = body.Text
		}
	}
	if c.FileAs == "" {
		c.FileAs = strings.TrimSpace(c.FirstName + " " + c.LastName)
	}
	return c
}

func parseEventAppData(ad *wbxml.Node) *store.Event {
	e := &store.Event{Reminder: 15, BusyStatus: 2}
	if ad == nil {
		return e
	}
	if v := ad.ChildText("StartTime"); v != "" {
		if t := parseEasTime(v); !t.IsZero() {
			e.StartTime = t.Unix()
		}
	}
	if v := ad.ChildText("EndTime"); v != "" {
		if t := parseEasTime(v); !t.IsZero() {
			e.EndTime = t.Unix()
		}
	}
	// An unparsable timestamp must not leave the event in year 1: clients would
	// drop it from the calendar without any error.
	if e.StartTime <= 0 {
		e.StartTime = time.Now().Unix()
	}
	if e.EndTime <= 0 {
		e.EndTime = e.StartTime + 3600
	}
	e.Subject = ad.ChildText("Subject")
	e.Location = ad.ChildText("Location")
	e.UID = ad.ChildText("UID")
	if node := ad.Child("Recurrence"); node != nil {
		e.Recurrence = parseRecurrence(node)
	}
	if v := ad.ChildText("AllDayEvent"); v != "" {
		e.AllDay = v == "1"
	}
	if v := ad.ChildText("BusyStatus"); v != "" {
		e.BusyStatus = atoiDefault(v, 2)
	}
	if v := ad.ChildText("Sensitivity"); v != "" {
		e.Sensitivity = atoiDefault(v, 0)
	}
	if v := ad.ChildText("MeetingStatus"); v != "" {
		e.MeetingStatus = atoiDefault(v, 0)
	}
	if v := ad.ChildText("Reminder"); v != "" {
		e.Reminder = atoiDefault(v, 15)
	}
	e.OrganizerName = ad.ChildText("OrganizerName")
	e.OrganizerEmail = ad.ChildText("OrganizerEmail")
	if body := ad.Child("Body"); body != nil {
		if data := body.ChildText("Data"); data != "" {
			e.Body = data
		} else {
			e.Body = body.Text
		}
	}
	if list := ad.Child("Attendees"); list != nil {
		for _, att := range list.ChildrenNamed("Attendee") {
			e.Attendees = append(e.Attendees, store.Attendee{
				Name:   att.ChildText("Name"),
				Email:  att.ChildText("Email"),
				Status: atoiDefault(att.ChildText("AttendeeStatus"), 0),
				Type:   atoiDefault(att.ChildText("AttendeeType"), 0),
			})
		}
	}
	if e.DtStamp == 0 {
		e.DtStamp = time.Now().Unix()
	}
	e.Exceptions = parseExceptions(ad)
	return e
}

// sendCalendarTimeZone controls whether the MS-ASDTIMEZONE blob is sent with
// calendar items. Clients that omit it accept an event without a zone, but the
// element is part of the protocol and Outlook relies on it.
const sendCalendarTimeZone = true

// calendarExtendedFields covers the calendar properties beyond the four that
// every client needs (times, subject and UID).
const calendarExtendedFields = true

// utcTimeZoneBlob returns a MS-ASDTIMEZONE structure for a time zone. Times in
// the Calendar namespace are always UTC; the blob only tells the client how to
// display them, and some clients reject time zones they cannot map to a known
// Windows zone name, so a named zone is used instead of a bare "UTC".
func timeZoneBlob(name string, biasMinutes int32) string {
	buf := make([]byte, 172)
	putBias(buf[0:4], biasMinutes)
	writeUTF16Name(buf[4:68], name)
	putBias(buf[84:88], biasMinutes)
	writeUTF16Name(buf[88:152], name)
	putBias(buf[168:172], biasMinutes)
	return base64.StdEncoding.EncodeToString(buf)
}

// defaultTimeZoneBlob is used until a client uploads its own time zone, which
// then takes precedence for that account.
func defaultTimeZoneBlob() string {
	return timeZoneBlob("China Standard Time", -480)
}

func putBias(dst []byte, minutes int32) {
	value := uint32(minutes)
	dst[0] = byte(value)
	dst[1] = byte(value >> 8)
	dst[2] = byte(value >> 16)
	dst[3] = byte(value >> 24)
}

// utcTimeZoneBlob keeps the previous behaviour available for tests and for
// accounts that explicitly want UTC.
func utcTimeZoneBlob() string {
	return timeZoneBlob("UTC", 0)
}

func writeUTF16Name(dst []byte, name string) {
	for i := 0; i < len(name) && 2*i+1 < len(dst); i++ {
		dst[2*i] = name[i]
		dst[2*i+1] = 0
	}
}

const calendarTimeZoneSetting = "eas.calendar.timezone."

// calendarTimeZone returns the time zone blob advertised to a client for an
// account. The blob the client itself uploaded wins, because some clients only
// accept time zones they can map to a known Windows zone name.
func (s *Server) calendarTimeZone(userID string) string {
	if value, err := s.Store.Setting(calendarTimeZoneSetting + userID); err == nil {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return defaultTimeZoneBlob()
}

// rememberCalendarTimeZone stores the blob a client sends with a calendar
// item, so later downloads use the same zone.
func (s *Server) rememberCalendarTimeZone(userID, blob string) {
	blob = strings.TrimSpace(blob)
	if blob == "" || userID == "" {
		return
	}
	if err := s.Store.SetSetting(calendarTimeZoneSetting+userID, blob); err != nil {
		s.Log.Warn("cannot store calendar time zone", "err", err)
	}
}
