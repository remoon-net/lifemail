package store

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// Folder is a mail/calendar/contacts collection.
type Folder struct {
	ID       int64
	UserID   string
	ParentID int64
	Name     string
	Type     int
	Class    string
	Seq      int64
	Deleted  bool
	// UIDValidity is the IMAP UIDVALIDITY of the collection. It is assigned
	// once when the folder is created and never changes.
	UIDValidity int64
	// UUID identifies a calendar in its public subscription URL
	// (/calendar/<uuid>.ics). Assigned at creation so renaming a calendar never
	// breaks subscribers.
	UUID string
	// Public reports whether the owner opted this calendar into being readable
	// by anyone who knows the UUID.
	Public bool
}

type folderRow struct {
	ID             int64  `db:"id"`
	UserID         string `db:"user_id"`
	ParentID       int64  `db:"parent_id"`
	Name           string `db:"name"`
	Type           int    `db:"type"`
	Class          string `db:"class_name"`
	Seq            int64  `db:"seq"`
	Deleted        int    `db:"deleted"`
	UIDValidity    int64  `db:"uid_validity"`
	UUID           string `db:"calendar_uuid"`
	CalendarPublic int    `db:"calendar_public"`
}

func (r folderRow) folder() *Folder {
	return &Folder{
		ID: r.ID, UserID: r.UserID, ParentID: r.ParentID, Name: r.Name, Type: r.Type,
		Class: r.Class, Seq: r.Seq, Deleted: r.Deleted != 0, UIDValidity: r.UIDValidity,
		UUID: r.UUID, Public: r.CalendarPublic != 0,
	}
}

const folderColumns = `id, user_id, parent_id, name, type, class_name, seq, deleted,
	uid_validity, calendar_uuid, calendar_public`

const folderSelect = `SELECT ` + folderColumns + ` FROM folders`

// CreateFolder inserts a folder and bumps the user's folder sync counter.
func (s *Store) CreateFolder(userID string, parentID int64, name string, typ int) (*Folder, error) {
	var folder *Folder
	err := s.inTx(func(tx *sqlx.Tx) error {
		f, err := insertFolderTx(s, tx, userID, parentID, name, typ)
		folder = f
		return err
	})
	if err != nil {
		return nil, err
	}
	return folder, nil
}

// insertFolderTx inserts a folder inside an existing transaction.
func insertFolderTx(s *Store, tx *sqlx.Tx, userID string, parentID int64, name string, typ int) (*Folder, error) {
	var seq int64
	if err := tx.Get(&seq, s.rebind(
		`UPDATE users SET folder_seq = folder_seq + 1, updated_at = ? WHERE id = ? RETURNING folder_seq`),
		nowUnix(), userID); err != nil {
		return nil, err
	}
	f := &Folder{
		UserID:      userID,
		ParentID:    parentID,
		Name:        name,
		Type:        typ,
		Class:       ClassForType(typ),
		Seq:         seq,
		UIDValidity: nowUnix(),
	}
	if typ == FolderTypeCalendar {
		f.UUID = NewUUID()
	}
	if err := tx.Get(&f.ID, s.rebind(
		`INSERT INTO folders (user_id, parent_id, name, type, class_name, seq, uid_validity,
		 deleted, calendar_uuid, calendar_public)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, 0) RETURNING id`),
		f.UserID, f.ParentID, f.Name, f.Type, f.Class, f.Seq, f.UIDValidity, f.UUID); err != nil {
		return nil, wrapDuplicate(err)
	}
	return f, nil
}

// FolderByID returns a folder by primary key.
func (s *Store) FolderByID(id int64) (*Folder, error) {
	var row folderRow
	if err := s.get(&row, folderSelect+` WHERE id = ?`, id); err != nil {
		return nil, err
	}
	return row.folder(), nil
}

// FolderByType returns the first live folder of the given EAS folder type.
func (s *Store) FolderByType(userID string, typ int) (*Folder, error) {
	var row folderRow
	if err := s.get(&row,
		folderSelect+` WHERE user_id = ? AND type = ? AND deleted = 0 ORDER BY id LIMIT 1`,
		userID, typ); err != nil {
		return nil, err
	}
	return row.folder(), nil
}

// FolderByName returns a live folder by display name (case-insensitive).
func (s *Store) FolderByName(userID, name string) (*Folder, error) {
	var row folderRow
	if err := s.get(&row,
		folderSelect+` WHERE user_id = ? AND lower(name) = ? AND deleted = 0 ORDER BY id LIMIT 1`,
		userID, strings.ToLower(strings.TrimSpace(name))); err != nil {
		return nil, err
	}
	return row.folder(), nil
}

// FoldersByUser returns every folder (including tombstones) ordered for
// FolderSync.
func (s *Store) FoldersByUser(userID string) ([]*Folder, error) {
	var rows []folderRow
	if err := s.getAll(&rows, folderSelect+` WHERE user_id = ? ORDER BY parent_id, id`, userID); err != nil {
		return nil, err
	}
	return folderRows(rows), nil
}

// CalendarFolders returns a user's live calendars, oldest first.
func (s *Store) CalendarFolders(userID string) ([]*Folder, error) {
	var rows []folderRow
	if err := s.getAll(&rows,
		folderSelect+` WHERE user_id = ? AND type = 8 AND deleted = 0 ORDER BY id`, userID); err != nil {
		return nil, err
	}
	return folderRows(rows), nil
}

func folderRows(rows []folderRow) []*Folder {
	out := make([]*Folder, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.folder())
	}
	return out
}

// RenameFolder changes a folder's display name.
func (s *Store) RenameFolder(id int64, name string) error {
	return s.touchFolder(id, `name = ?`, name)
}

// ReparentFolder moves a folder under a new parent.
func (s *Store) ReparentFolder(id, parentID int64) error {
	return s.touchFolder(id, `parent_id = ?`, parentID)
}

// DeleteFolder tombstones a folder. Child folders and their contents keep
// their rows so clients can still see the deletions.
func (s *Store) DeleteFolder(id int64) error {
	return s.touchFolder(id, `deleted = 1`)
}

// touchFolder applies a folder update together with a fresh sequence number.
func (s *Store) touchFolder(id int64, assignment string, args ...any) error {
	return s.inTx(func(tx *sqlx.Tx) error {
		var userID string
		if err := tx.Get(&userID, s.rebind(`SELECT user_id FROM folders WHERE id = ?`), id); err != nil {
			return err
		}
		var seq int64
		if err := tx.Get(&seq, s.rebind(
			`UPDATE users SET folder_seq = folder_seq + 1, updated_at = ? WHERE id = ? RETURNING folder_seq`),
			nowUnix(), userID); err != nil {
			return err
		}
		params := append([]any{}, args...)
		params = append(params, seq, id)
		_, err := tx.Exec(s.rebind(fmt.Sprintf(`UPDATE folders SET %s, seq = ? WHERE id = ?`, assignment)), params...)
		return err
	})
}

// IsSystemFolder reports whether a folder type is provisioned by the server.
func IsSystemFolder(typ int) bool {
	switch typ {
	case 2, 3, 4, 5, 6, 8, 9, 12:
		return true
	}
	return false
}

// FolderTypeCalendar is the EAS folder type for calendar collections.
const FolderTypeCalendar = 8

// NewUUID returns a random RFC 4122 version 4 UUID.
func NewUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand never fails on the platforms we support; fall back to a
		// time-based value rather than returning an empty identifier.
		binary.BigEndian.PutUint64(b[:8], uint64(time.Now().UnixNano()))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// FolderByUUID looks up a live calendar folder by its feed identifier.
func (s *Store) FolderByUUID(uuid string) (*Folder, error) {
	if strings.TrimSpace(uuid) == "" {
		return nil, ErrNotFound
	}
	var row folderRow
	if err := s.get(&row,
		folderSelect+` WHERE calendar_uuid = ? AND deleted = 0 ORDER BY id LIMIT 1`, uuid); err != nil {
		return nil, err
	}
	return row.folder(), nil
}

// SetFolderPublic toggles public read access to a calendar's feed. It is the
// owner's own decision, so it does not bump the folder sequence.
func (s *Store) SetFolderPublic(id int64, public bool) error {
	res, err := s.exec(
		`UPDATE folders SET calendar_public = ? WHERE id = ? AND type = 8 AND deleted = 0`,
		boolInt(public), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CalendarSummary is a calendar folder with its owner, used by the admin
// console to hand out subscription links.
type CalendarSummary struct {
	ID     int64
	UserID string
	Owner  string
	Name   string
	Public bool
	UUID   string
}

// AllCalendars lists every live calendar across accounts.
func (s *Store) AllCalendars() ([]*CalendarSummary, error) {
	type row struct {
		ID     int64  `db:"id"`
		UserID string `db:"user_id"`
		Owner  string `db:"owner"`
		Name   string `db:"name"`
		Public int    `db:"calendar_public"`
		UUID   string `db:"calendar_uuid"`
	}
	var rows []row
	if err := s.getAll(&rows,
		`SELECT f.id, f.user_id, COALESCE(p.address, f.user_id) AS owner, f.name,
		        f.calendar_public, f.calendar_uuid
		 FROM folders f
		 LEFT JOIN aliases p ON p.user_id = f.user_id AND p.is_primary = 1
		 WHERE f.type = 8 AND f.deleted = 0
		 ORDER BY owner, f.id`); err != nil {
		return nil, err
	}
	out := make([]*CalendarSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, &CalendarSummary{
			ID: r.ID, UserID: r.UserID, Owner: r.Owner, Name: r.Name,
			Public: r.Public != 0, UUID: r.UUID,
		})
	}
	return out, nil
}
