package store

import (
	"errors"
	"strings"

	"github.com/jmoiron/sqlx"
)

// User is an account served over EAS/IMAP/SMTP. The account itself is only a
// login name; the addresses that reach it live in the aliases table, one of
// which is flagged as primary and used as the default sender.
type User struct {
	ID           string
	Name         string
	Address      string
	DisplayName  string
	PasswordHash string
	FolderSeq    int64
	CreatedAt    int64
}

type userRow struct {
	ID           string `db:"id"`
	Name         string `db:"name"`
	Address      string `db:"address"`
	DisplayName  string `db:"display_name"`
	PasswordHash string `db:"password_hash"`
	FolderSeq    int64  `db:"folder_seq"`
	CreatedAt    int64  `db:"created_at"`
}

func (r userRow) user() *User {
	return &User{
		ID:           r.ID,
		Name:         r.Name,
		Address:      r.Address,
		DisplayName:  r.DisplayName,
		PasswordHash: r.PasswordHash,
		FolderSeq:    r.FolderSeq,
		CreatedAt:    r.CreatedAt,
	}
}

const userSelect = `SELECT u.id AS id, u.name AS name,
	COALESCE(p.address, '') AS address, u.display_name AS display_name,
	u.password_hash AS password_hash, u.folder_seq AS folder_seq,
	u.created_at AS created_at
	FROM users u
	LEFT JOIN aliases p ON p.user_id = u.id AND p.is_primary = 1`

func normalizeName(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

func validateName(name string) error {
	if name == "" {
		return errors.New("store: login name is required")
	}
	if strings.ContainsAny(name, "@ \t\r\n") {
		return errors.New("store: login name must not contain '@' or whitespace")
	}
	return nil
}

// defaultFolders are provisioned for every new account. Names are what EAS
// clients display; Spam is recognised by name by Android clients, because the
// protocol has no "junk" folder type.
var defaultFolders = []struct {
	name string
	typ  int
}{
	{"Inbox", 2},
	{"Drafts", 3},
	{"Outbox", 6},
	{"Sent Items", 5},
	{"Deleted Items", 4},
	{"Spam", 12},
	{"Calendar", 8},
	{"Contacts", 9},
}

// CreateUser inserts an account and provisions its folder tree atomically.
func (s *Store) CreateUser(name, address, displayName, passwordHash string) (*User, error) {
	name = normalizeName(name)
	if err := validateName(name); err != nil {
		return nil, err
	}
	if displayName == "" {
		displayName = name
	}
	addr := normalizeEmail(address)
	if addr != "" && (!strings.Contains(addr, "@") || strings.ContainsAny(addr, " \t\r\n")) {
		return nil, errors.New("store: invalid primary address")
	}
	id := newID()
	now := nowUnix()
	err := s.inTx(func(tx *sqlx.Tx) error {
		if _, err := tx.Exec(s.rebind(
			`INSERT INTO users (id, name, display_name, password_hash, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?)`),
			id, name, displayName, passwordHash, now, now); err != nil {
			return wrapDuplicate(err)
		}
		for _, folder := range defaultFolders {
			if _, err := insertFolderTx(s, tx, id, 0, folder.name, folder.typ); err != nil {
				return err
			}
		}
		if addr != "" {
			if _, err := tx.Exec(s.rebind(
				`INSERT INTO aliases (address, user_id, is_primary, created_at) VALUES (?, ?, 1, ?)`),
				addr, id, now); err != nil {
				return wrapDuplicate(err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.UserByID(id)
}

// UserByName looks a user up by login name (case-insensitive).
func (s *Store) UserByName(name string) (*User, error) {
	var row userRow
	if err := s.get(&row, userSelect+` WHERE u.name = ?`, normalizeName(name)); err != nil {
		return nil, err
	}
	return row.user(), nil
}

// UserByID looks a user up by primary key.
func (s *Store) UserByID(id string) (*User, error) {
	var row userRow
	if err := s.get(&row, userSelect+` WHERE u.id = ?`, id); err != nil {
		return nil, err
	}
	return row.user(), nil
}

// ListUsers returns all accounts ordered by login name.
func (s *Store) ListUsers() ([]*User, error) {
	var rows []userRow
	if err := s.getAll(&rows, userSelect+` ORDER BY u.name`); err != nil {
		return nil, err
	}
	out := make([]*User, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.user())
	}
	return out, nil
}

// SetPassword updates the password hash of the account with the given login
// name.
func (s *Store) SetPassword(name, passwordHash string) error {
	res, err := s.exec(`UPDATE users SET password_hash = ?, updated_at = ? WHERE name = ?`,
		passwordHash, nowUnix(), normalizeName(name))
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetPasswordByID updates the password hash of the user with the given id.
func (s *Store) SetPasswordByID(id, passwordHash string) error {
	res, err := s.exec(`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
		passwordHash, nowUnix(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateDisplayName renames a user.
func (s *Store) UpdateDisplayName(id, displayName string) error {
	res, err := s.exec(`UPDATE users SET display_name = ?, updated_at = ? WHERE id = ?`,
		displayName, nowUnix(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteUser removes the account. Rows owned by it cascade; payloads live
// outside the database and are removed explicitly.
func (s *Store) DeleteUser(id string) error {
	if _, err := s.UserByID(id); err != nil {
		return err
	}
	paths, err := s.payloadPaths(id)
	if err != nil {
		return err
	}
	res, err := s.exec(`DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if s.blobs != nil {
		for _, path := range paths {
			_ = s.blobs.Remove(path)
		}
	}
	return nil
}

// payloadPaths collects every blob key owned by an account.
func (s *Store) payloadPaths(userID string) ([]string, error) {
	if s.blobs == nil {
		return nil, nil
	}
	queries := []string{
		`SELECT a.path FROM attachments a
		 JOIN messages m ON m.id = a.message_id
		 WHERE a.path <> '' AND m.user_id = ?`,
		`SELECT raw_path AS path FROM messages WHERE raw_path <> '' AND user_id = ?`,
	}
	var out []string
	for _, query := range queries {
		var paths []string
		if err := s.getAll(&paths, query, userID); err != nil {
			return nil, err
		}
		out = append(out, paths...)
	}
	return out, nil
}

// TouchFolderSeq bumps and returns the per-user folder change counter.
func (s *Store) TouchFolderSeq(userID string) (int64, error) {
	var seq int64
	err := s.get(&seq,
		`UPDATE users SET folder_seq = folder_seq + 1, updated_at = ? WHERE id = ? RETURNING folder_seq`,
		nowUnix(), userID)
	if errors.Is(err, ErrNotFound) {
		return 0, ErrNotFound
	}
	return seq, err
}
