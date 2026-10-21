package store

import (
	"encoding/json"

	"github.com/jmoiron/sqlx"
)

// Contact is an address book entry.
type Contact struct {
	ID       int64
	UserID   string
	FolderID int64

	FirstName  string
	LastName   string
	MiddleName string
	FileAs     string
	Company    string
	Department string
	JobTitle   string

	Email1 string
	Email2 string
	Email3 string

	HomePhone     string
	Home2Phone    string
	MobilePhone   string
	BusinessPhone string
	BusinessFax   string

	HomeStreet     string
	HomeCity       string
	HomeState      string
	HomePostalCode string
	HomeCountry    string

	BusinessStreet     string
	BusinessCity       string
	BusinessState      string
	BusinessPostalCode string
	BusinessCountry    string

	Body    string
	Deleted bool
}

type contactRow struct {
	ID                 int64  `db:"id"`
	UserID             string `db:"user_id"`
	FolderID           int64  `db:"folder_id"`
	FirstName          string `db:"first_name"`
	LastName           string `db:"last_name"`
	MiddleName         string `db:"middle_name"`
	FileAs             string `db:"file_as"`
	Company            string `db:"company_name"`
	Department         string `db:"department"`
	JobTitle           string `db:"job_title"`
	Email1             string `db:"email1"`
	Email2             string `db:"email2"`
	Email3             string `db:"email3"`
	HomePhone          string `db:"home_phone"`
	Home2Phone         string `db:"home2_phone"`
	MobilePhone        string `db:"mobile_phone"`
	BusinessPhone      string `db:"business_phone"`
	BusinessFax        string `db:"business_fax"`
	HomeStreet         string `db:"home_street"`
	HomeCity           string `db:"home_city"`
	HomeState          string `db:"home_state"`
	HomePostalCode     string `db:"home_postal_code"`
	HomeCountry        string `db:"home_country"`
	BusinessStreet     string `db:"business_street"`
	BusinessCity       string `db:"business_city"`
	BusinessState      string `db:"business_state"`
	BusinessPostalCode string `db:"business_postal_code"`
	BusinessCountry    string `db:"business_country"`
	Body               string `db:"body"`
	Deleted            int    `db:"deleted"`
}

func (r contactRow) contact() *Contact {
	return &Contact{
		ID: r.ID, UserID: r.UserID, FolderID: r.FolderID,
		FirstName: r.FirstName, LastName: r.LastName, MiddleName: r.MiddleName,
		FileAs: r.FileAs, Company: r.Company, Department: r.Department, JobTitle: r.JobTitle,
		Email1: r.Email1, Email2: r.Email2, Email3: r.Email3,
		HomePhone: r.HomePhone, Home2Phone: r.Home2Phone, MobilePhone: r.MobilePhone,
		BusinessPhone: r.BusinessPhone, BusinessFax: r.BusinessFax,
		HomeStreet: r.HomeStreet, HomeCity: r.HomeCity, HomeState: r.HomeState,
		HomePostalCode: r.HomePostalCode, HomeCountry: r.HomeCountry,
		BusinessStreet: r.BusinessStreet, BusinessCity: r.BusinessCity,
		BusinessState: r.BusinessState, BusinessPostalCode: r.BusinessPostalCode,
		BusinessCountry: r.BusinessCountry, Body: r.Body, Deleted: r.Deleted != 0,
	}
}

const contactColumns = `id, user_id, folder_id, first_name, last_name, middle_name, file_as,
	company_name, department, job_title, email1, email2, email3, home_phone, home2_phone,
	mobile_phone, business_phone, business_fax, home_street, home_city, home_state,
	home_postal_code, home_country, business_street, business_city, business_state,
	business_postal_code, business_country, body, deleted`

const contactSelect = `SELECT ` + contactColumns + ` FROM contacts`

// InsertContact creates a contact and returns its server id.
func (s *Store) InsertContact(c *Contact) (int64, error) {
	err := s.get(&c.ID,
		`INSERT INTO contacts (user_id, folder_id, first_name, last_name, middle_name, file_as,
		 company_name, department, job_title, email1, email2, email3, home_phone, home2_phone,
		 mobile_phone, business_phone, business_fax, home_street, home_city, home_state,
		 home_postal_code, home_country, business_street, business_city, business_state,
		 business_postal_code, business_country, body, deleted)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 RETURNING id`,
		c.UserID, c.FolderID, c.FirstName, c.LastName, c.MiddleName, c.FileAs,
		c.Company, c.Department, c.JobTitle, c.Email1, c.Email2, c.Email3,
		c.HomePhone, c.Home2Phone, c.MobilePhone, c.BusinessPhone, c.BusinessFax,
		c.HomeStreet, c.HomeCity, c.HomeState, c.HomePostalCode, c.HomeCountry,
		c.BusinessStreet, c.BusinessCity, c.BusinessState, c.BusinessPostalCode,
		c.BusinessCountry, c.Body, boolInt(c.Deleted))
	return c.ID, err
}

// UpdateContact persists all mutable contact fields.
func (s *Store) UpdateContact(c *Contact) error {
	_, err := s.exec(
		`UPDATE contacts SET folder_id = ?, first_name = ?, last_name = ?, middle_name = ?,
		 file_as = ?, company_name = ?, department = ?, job_title = ?, email1 = ?, email2 = ?,
		 email3 = ?, home_phone = ?, home2_phone = ?, mobile_phone = ?, business_phone = ?,
		 business_fax = ?, home_street = ?, home_city = ?, home_state = ?, home_postal_code = ?,
		 home_country = ?, business_street = ?, business_city = ?, business_state = ?,
		 business_postal_code = ?, business_country = ?, body = ?, deleted = ? WHERE id = ?`,
		c.FolderID, c.FirstName, c.LastName, c.MiddleName, c.FileAs, c.Company,
		c.Department, c.JobTitle, c.Email1, c.Email2, c.Email3, c.HomePhone,
		c.Home2Phone, c.MobilePhone, c.BusinessPhone, c.BusinessFax, c.HomeStreet,
		c.HomeCity, c.HomeState, c.HomePostalCode, c.HomeCountry, c.BusinessStreet,
		c.BusinessCity, c.BusinessState, c.BusinessPostalCode, c.BusinessCountry,
		c.Body, boolInt(c.Deleted), c.ID)
	return err
}

// ContactByID fetches one contact.
func (s *Store) ContactByID(id int64) (*Contact, error) {
	var row contactRow
	if err := s.get(&row, contactSelect+` WHERE id = ?`, id); err != nil {
		return nil, err
	}
	return row.contact(), nil
}

// ContactsByFolder lists live contacts in a folder.
func (s *Store) ContactsByFolder(folderID int64) ([]*Contact, error) {
	var rows []contactRow
	if err := s.getAll(&rows,
		contactSelect+` WHERE folder_id = ? AND deleted = 0 ORDER BY file_as, id`, folderID); err != nil {
		return nil, err
	}
	out := make([]*Contact, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.contact())
	}
	return out, nil
}

// Attendee is a calendar event participant.
type Attendee struct {
	Name   string `json:"name,omitempty"`
	Email  string `json:"email,omitempty"`
	Status int    `json:"status,omitempty"`
	Type   int    `json:"type,omitempty"`
}

// Event is a calendar entry stored in UTC.
type Event struct {
	ID       int64
	UserID   string
	FolderID int64

	UID      string
	Subject  string
	Location string
	Body     string

	StartTime int64
	EndTime   int64
	DtStamp   int64

	AllDay        bool
	BusyStatus    int
	Sensitivity   int
	MeetingStatus int
	Reminder      int

	OrganizerName  string
	OrganizerEmail string
	Attendees      []Attendee

	Recurrence Recurrence

	// Sequence counts how often the item was revised, and LastModified is when
	// that happened. Subscribed calendars use them to decide whether a cached
	// copy is stale.
	Sequence     int
	LastModified int64

	// Exceptions holds the occurrences of a recurring event that were
	// cancelled or moved. They live in their own table; use EventExceptions or
	// ExceptionsByFolder to load them.
	Exceptions []Exception

	Deleted bool
}

type eventRow struct {
	ID                    int64  `db:"id"`
	UserID                string `db:"user_id"`
	FolderID              int64  `db:"folder_id"`
	UID                   string `db:"uid"`
	Subject               string `db:"subject"`
	Location              string `db:"location"`
	Body                  string `db:"body"`
	StartTime             int64  `db:"start_time"`
	EndTime               int64  `db:"end_time"`
	DtStamp               int64  `db:"dtstamp"`
	AllDay                int    `db:"all_day"`
	BusyStatus            int    `db:"busy_status"`
	Sensitivity           int    `db:"sensitivity"`
	MeetingStatus         int    `db:"meeting_status"`
	Reminder              int    `db:"reminder"`
	OrganizerName         string `db:"organizer_name"`
	OrganizerEmail        string `db:"organizer_email"`
	Attendees             string `db:"attendees"`
	Deleted               int    `db:"deleted"`
	RecurrenceType        int    `db:"recurrence_type"`
	RecurrenceInterval    int    `db:"recurrence_interval"`
	RecurrenceUntil       int64  `db:"recurrence_until"`
	RecurrenceOccurrences int    `db:"recurrence_occurrences"`
	RecurrenceDayOfWeek   int    `db:"recurrence_day_of_week"`
	RecurrenceDayOfMonth  int    `db:"recurrence_day_of_month"`
	RecurrenceWeekOfMonth int    `db:"recurrence_week_of_month"`
	RecurrenceMonthOfYear int    `db:"recurrence_month_of_year"`
	Sequence              int    `db:"sequence"`
	LastModified          int64  `db:"last_modified"`
}

func (r eventRow) event() *Event {
	e := &Event{
		ID: r.ID, UserID: r.UserID, FolderID: r.FolderID, UID: r.UID, Subject: r.Subject,
		Location: r.Location, Body: r.Body, StartTime: r.StartTime, EndTime: r.EndTime,
		DtStamp: r.DtStamp, AllDay: r.AllDay != 0, BusyStatus: r.BusyStatus,
		Sensitivity: r.Sensitivity, MeetingStatus: r.MeetingStatus, Reminder: r.Reminder,
		OrganizerName: r.OrganizerName, OrganizerEmail: r.OrganizerEmail,
		Deleted: r.Deleted != 0, Sequence: r.Sequence, LastModified: r.LastModified,
		Recurrence: Recurrence{
			Type: r.RecurrenceType, Interval: r.RecurrenceInterval, Until: r.RecurrenceUntil,
			Occurrences: r.RecurrenceOccurrences, DayOfWeek: r.RecurrenceDayOfWeek,
			DayOfMonth: r.RecurrenceDayOfMonth, WeekOfMonth: r.RecurrenceWeekOfMonth,
			MonthOfYear: r.RecurrenceMonthOfYear,
		},
	}
	if r.Attendees != "" {
		_ = json.Unmarshal([]byte(r.Attendees), &e.Attendees)
	}
	return e
}

const eventColumns = `id, user_id, folder_id, uid, subject, location, body, start_time, end_time,
	dtstamp, all_day, busy_status, sensitivity, meeting_status, reminder, organizer_name,
	organizer_email, attendees, deleted, recurrence_type, recurrence_interval, recurrence_until,
	recurrence_occurrences, recurrence_day_of_week, recurrence_day_of_month,
	recurrence_week_of_month, recurrence_month_of_year, sequence, last_modified`

const eventSelect = `SELECT ` + eventColumns + ` FROM events`

func eventRows(rows []eventRow) []*Event {
	out := make([]*Event, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.event())
	}
	return out
}

// Recurrence is a repeat rule in the shape MS-ASCAL uses. Type follows the
// protocol values (0 daily, 1 weekly, 2 monthly by date, 3 monthly by weekday,
// 4 yearly by date, 5 yearly by weekday). A zero Interval means "does not
// repeat": the zero value of this struct is a valid "no rule", which matters
// because 0 is a real Type value.
type Recurrence struct {
	Type        int
	Interval    int
	Until       int64
	Occurrences int
	DayOfWeek   int
	DayOfMonth  int
	WeekOfMonth int
	MonthOfYear int
}

// Active reports whether the event repeats.
func (r Recurrence) Active() bool { return r.Interval > 0 }

// InsertEvent creates a calendar event and returns its server id.
func (s *Store) InsertEvent(e *Event) (int64, error) {
	if e.BusyStatus == 0 {
		e.BusyStatus = 2
	}
	if e.DtStamp == 0 {
		e.DtStamp = nowUnix()
	}
	if e.LastModified == 0 {
		e.LastModified = e.DtStamp
	}
	att, err := json.Marshal(e.Attendees)
	if err != nil {
		return 0, err
	}
	err = s.get(&e.ID,
		`INSERT INTO events (user_id, folder_id, uid, subject, location, body, start_time,
		 end_time, dtstamp, all_day, busy_status, sensitivity, meeting_status, reminder,
		 organizer_name, organizer_email, attendees, deleted, recurrence_type,
		 recurrence_interval, recurrence_until, recurrence_occurrences,
		 recurrence_day_of_week, recurrence_day_of_month, recurrence_week_of_month,
		 recurrence_month_of_year, sequence, last_modified)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 RETURNING id`,
		e.UserID, e.FolderID, e.UID, e.Subject, e.Location, e.Body, e.StartTime,
		e.EndTime, e.DtStamp, boolInt(e.AllDay), e.BusyStatus, e.Sensitivity,
		e.MeetingStatus, e.Reminder, e.OrganizerName, e.OrganizerEmail, string(att),
		boolInt(e.Deleted), e.Recurrence.Type, e.Recurrence.Interval, e.Recurrence.Until,
		e.Recurrence.Occurrences, e.Recurrence.DayOfWeek, e.Recurrence.DayOfMonth,
		e.Recurrence.WeekOfMonth, e.Recurrence.MonthOfYear, e.Sequence, e.LastModified)
	return e.ID, err
}

// UpdateEvent persists all mutable event fields and bumps the revision.
func (s *Store) UpdateEvent(e *Event) error {
	att, err := json.Marshal(e.Attendees)
	if err != nil {
		return err
	}
	_, err = s.exec(
		`UPDATE events SET folder_id = ?, uid = ?, subject = ?, location = ?, body = ?,
		 start_time = ?, end_time = ?, dtstamp = ?, all_day = ?, busy_status = ?,
		 sensitivity = ?, meeting_status = ?, reminder = ?, organizer_name = ?,
		 organizer_email = ?, attendees = ?, deleted = ?, recurrence_type = ?,
		 recurrence_interval = ?, recurrence_until = ?, recurrence_occurrences = ?,
		 recurrence_day_of_week = ?, recurrence_day_of_month = ?, recurrence_week_of_month = ?,
		 recurrence_month_of_year = ?, sequence = sequence + 1, last_modified = ? WHERE id = ?`,
		e.FolderID, e.UID, e.Subject, e.Location, e.Body, e.StartTime, e.EndTime,
		e.DtStamp, boolInt(e.AllDay), e.BusyStatus, e.Sensitivity, e.MeetingStatus,
		e.Reminder, e.OrganizerName, e.OrganizerEmail, string(att), boolInt(e.Deleted),
		e.Recurrence.Type, e.Recurrence.Interval, e.Recurrence.Until,
		e.Recurrence.Occurrences, e.Recurrence.DayOfWeek, e.Recurrence.DayOfMonth,
		e.Recurrence.WeekOfMonth, e.Recurrence.MonthOfYear, nowUnix(), e.ID)
	return err
}

// EventByID fetches one event.
func (s *Store) EventByID(id int64) (*Event, error) {
	var row eventRow
	if err := s.get(&row, eventSelect+` WHERE id = ?`, id); err != nil {
		return nil, err
	}
	return row.event(), nil
}

// EventByUID finds a live event by its iCalendar UID for one account. Used to
// apply iMIP replies, which identify the meeting only by UID.
func (s *Store) EventByUID(userID, uid string) (*Event, error) {
	var row eventRow
	if err := s.get(&row,
		eventSelect+` WHERE user_id = ? AND uid = ? AND deleted = 0 ORDER BY id DESC LIMIT 1`,
		userID, uid); err != nil {
		return nil, err
	}
	return row.event(), nil
}

// EventsByFolder lists live events in a folder ordered by start time.
func (s *Store) EventsByFolder(folderID int64) ([]*Event, error) {
	var rows []eventRow
	if err := s.getAll(&rows,
		eventSelect+` WHERE folder_id = ? AND deleted = 0 ORDER BY start_time, id`, folderID); err != nil {
		return nil, err
	}
	return eventRows(rows), nil
}

// Exception is a single occurrence of a recurring event that differs from the
// series: either cancelled, or rescheduled for that date only.
type Exception struct {
	// Occurrence is the start of the occurrence being overridden.
	Occurrence int64 `json:"occurrence"`
	Deleted    bool  `json:"deleted,omitempty"`

	Subject     string `json:"subject,omitempty"`
	Location    string `json:"location,omitempty"`
	Body        string `json:"body,omitempty"`
	StartTime   int64  `json:"startTime,omitempty"`
	EndTime     int64  `json:"endTime,omitempty"`
	AllDay      bool   `json:"allDay,omitempty"`
	BusyStatus  int    `json:"busyStatus,omitempty"`
	Sensitivity int    `json:"sensitivity,omitempty"`
	Reminder    int    `json:"reminder,omitempty"`
	// MeetingStatus records a response to this single occurrence, for events
	// the account organises.
	MeetingStatus int `json:"meetingStatus,omitempty"`
}

type exceptionRow struct {
	EventID       int64  `db:"event_id"`
	Occurrence    int64  `db:"occurrence"`
	Deleted       int    `db:"deleted"`
	Subject       string `db:"subject"`
	Location      string `db:"location"`
	Body          string `db:"body"`
	StartTime     int64  `db:"start_time"`
	EndTime       int64  `db:"end_time"`
	AllDay        int    `db:"all_day"`
	BusyStatus    int    `db:"busy_status"`
	Sensitivity   int    `db:"sensitivity"`
	Reminder      int    `db:"reminder"`
	MeetingStatus int    `db:"meeting_status"`
}

func (r exceptionRow) exception() Exception {
	return Exception{
		Occurrence: r.Occurrence, Deleted: r.Deleted != 0, Subject: r.Subject,
		Location: r.Location, Body: r.Body, StartTime: r.StartTime, EndTime: r.EndTime,
		AllDay: r.AllDay != 0, BusyStatus: r.BusyStatus, Sensitivity: r.Sensitivity,
		Reminder: r.Reminder, MeetingStatus: r.MeetingStatus,
	}
}

const exceptionColumns = `event_id, occurrence, deleted, subject, location, body, start_time,
	end_time, all_day, busy_status, sensitivity, reminder, meeting_status`

// EventExceptions returns the occurrence overrides of a recurring event.
func (s *Store) EventExceptions(eventID int64) ([]Exception, error) {
	var rows []exceptionRow
	if err := s.getAll(&rows,
		`SELECT `+exceptionColumns+` FROM event_exceptions WHERE event_id = ? ORDER BY occurrence`,
		eventID); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	out := make([]Exception, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.exception())
	}
	return out, nil
}

// ExceptionsByFolder loads every occurrence override of a collection in one
// query, keyed by event id.
func (s *Store) ExceptionsByFolder(folderID int64) (map[int64][]Exception, error) {
	var rows []exceptionRow
	if err := s.getAll(&rows,
		`SELECT x.event_id, x.occurrence, x.deleted, x.subject, x.location, x.body,
		        x.start_time, x.end_time, x.all_day, x.busy_status, x.sensitivity,
		        x.reminder, x.meeting_status
		 FROM event_exceptions x
		 JOIN events e ON e.id = x.event_id
		 WHERE e.folder_id = ? AND e.deleted = 0
		 ORDER BY x.event_id, x.occurrence`, folderID); err != nil {
		return nil, err
	}
	out := make(map[int64][]Exception, len(rows))
	for _, row := range rows {
		out[row.EventID] = append(out[row.EventID], row.exception())
	}
	return out, nil
}

// ReplaceEventExceptions stores the complete set of overrides for an event.
func (s *Store) ReplaceEventExceptions(eventID int64, list []Exception) error {
	return s.inTx(func(tx *sqlx.Tx) error {
		if _, err := tx.Exec(s.rebind(`DELETE FROM event_exceptions WHERE event_id = ?`), eventID); err != nil {
			return err
		}
		for _, ex := range list {
			if _, err := tx.Exec(s.rebind(
				`INSERT INTO event_exceptions (`+exceptionColumns+`)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
				eventID, ex.Occurrence, boolInt(ex.Deleted), ex.Subject, ex.Location, ex.Body,
				ex.StartTime, ex.EndTime, boolInt(ex.AllDay), ex.BusyStatus, ex.Sensitivity,
				ex.Reminder, ex.MeetingStatus); err != nil {
				return err
			}
		}
		return touchEventTx(s, tx, eventID)
	})
}

// SetExceptionMeetingStatus records how the user answered one occurrence of a
// recurring meeting. The occurrence is identified by the start of the series
// instance, which is what clients send as InstanceId.
func (s *Store) SetExceptionMeetingStatus(eventID, occurrence int64, status int) error {
	return s.inTx(func(tx *sqlx.Tx) error {
		res, err := tx.Exec(s.rebind(
			`UPDATE event_exceptions SET meeting_status = ? WHERE event_id = ? AND occurrence = ?`),
			status, eventID, occurrence)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			if _, err := tx.Exec(s.rebind(
				`INSERT INTO event_exceptions (event_id, occurrence, meeting_status) VALUES (?, ?, ?)`),
				eventID, occurrence, status); err != nil {
				return err
			}
		}
		return touchEventTx(s, tx, eventID)
	})
}

// touchEventTx marks an event as revised without touching its other fields.
func touchEventTx(s *Store, tx *sqlx.Tx, eventID int64) error {
	_, err := tx.Exec(s.rebind(
		`UPDATE events SET sequence = sequence + 1, last_modified = ? WHERE id = ?`),
		nowUnix(), eventID)
	return err
}
