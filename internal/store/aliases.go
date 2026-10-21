package store

import (
	"errors"
	"strings"

	"github.com/jmoiron/sqlx"
)

// Alias maps an address onto an account. Accounts are not tied to a domain:
// every address they answer on is a row here, and exactly one of them is the
// primary address (default sender, shown in client settings).
type Alias struct {
	Address   string
	UserID    string
	IsPrimary bool
	CreatedAt int64
}

type aliasRow struct {
	Address   string `db:"address"`
	UserID    string `db:"user_id"`
	IsPrimary int    `db:"is_primary"`
	CreatedAt int64  `db:"created_at"`
}

func (r aliasRow) alias() *Alias {
	return &Alias{Address: r.Address, UserID: r.UserID, IsPrimary: r.IsPrimary != 0, CreatedAt: r.CreatedAt}
}

const aliasColumns = `address, user_id, is_primary, created_at`

func aliasRows(rows []aliasRow) []*Alias {
	out := make([]*Alias, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.alias())
	}
	return out
}

// validAddress reports whether value looks like a bare mailbox address.
func validAddress(value string) bool {
	return value != "" && strings.Contains(value, "@") && !strings.ContainsAny(value, " \t\r\n")
}

// CreateAlias registers address as an alias of userID.
func (s *Store) CreateAlias(address, userID string) (*Alias, error) {
	addr := normalizeEmail(address)
	if !validAddress(addr) {
		return nil, errors.New("store: invalid alias address")
	}
	if _, err := s.UserByID(userID); err != nil {
		return nil, err
	}
	if _, err := s.exec(
		`INSERT INTO aliases (address, user_id, is_primary, created_at) VALUES (?, ?, 0, ?)`,
		addr, userID, nowUnix()); err != nil {
		return nil, wrapDuplicate(err)
	}
	return s.AliasByAddress(addr)
}

// AliasByAddress looks up an alias. Addresses are stored lowercase, so this is
// an index lookup rather than a function scan.
func (s *Store) AliasByAddress(address string) (*Alias, error) {
	var row aliasRow
	if err := s.get(&row,
		`SELECT `+aliasColumns+` FROM aliases WHERE address = ?`, normalizeEmail(address)); err != nil {
		return nil, err
	}
	return row.alias(), nil
}

// AliasesByUser returns the addresses of one account, primary first.
func (s *Store) AliasesByUser(userID string) ([]*Alias, error) {
	var rows []aliasRow
	if err := s.getAll(&rows,
		`SELECT `+aliasColumns+` FROM aliases WHERE user_id = ? ORDER BY is_primary DESC, address`,
		userID); err != nil {
		return nil, err
	}
	return aliasRows(rows), nil
}

// ListAliases returns every alias ordered by address.
func (s *Store) ListAliases() ([]*Alias, error) {
	var rows []aliasRow
	if err := s.getAll(&rows, `SELECT `+aliasColumns+` FROM aliases ORDER BY address`); err != nil {
		return nil, err
	}
	return aliasRows(rows), nil
}

// DeleteAlias removes an address. The primary address cannot be removed
// because the account would lose its default sender.
func (s *Store) DeleteAlias(address string) error {
	addr := normalizeEmail(address)
	existing, err := s.AliasByAddress(addr)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if existing != nil && existing.IsPrimary {
		return ErrPrimaryAddress
	}
	res, err := s.exec(`DELETE FROM aliases WHERE address = ?`, addr)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetPrimaryAddress makes address the primary address of the account: the
// sender used by default and the address advertised to mail clients. The
// previous primary address stays on the account as a regular alias.
func (s *Store) SetPrimaryAddress(userID, address string) (*Alias, error) {
	addr := normalizeEmail(address)
	if !validAddress(addr) {
		return nil, errors.New("store: invalid address")
	}
	if _, err := s.UserByID(userID); err != nil {
		return nil, err
	}
	existing, err := s.AliasByAddress(addr)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if existing != nil && existing.UserID != userID {
		return nil, ErrDuplicate
	}
	err = s.inTx(func(tx *sqlx.Tx) error {
		if _, err := tx.Exec(s.rebind(`DELETE FROM aliases WHERE address = ?`), addr); err != nil {
			return err
		}
		if _, err := tx.Exec(s.rebind(
			`UPDATE aliases SET is_primary = 0 WHERE user_id = ? AND is_primary = 1`), userID); err != nil {
			return err
		}
		_, err := tx.Exec(s.rebind(
			`INSERT INTO aliases (address, user_id, is_primary, created_at) VALUES (?, ?, 1, ?)`),
			addr, userID, nowUnix())
		return wrapDuplicate(err)
	})
	if err != nil {
		return nil, err
	}
	return s.AliasByAddress(addr)
}

// UserByLogin resolves a client login to an account. Clients send either the
// bare login name ("box") or any address the account answers on.
func (s *Store) UserByLogin(login string) (*User, error) {
	name := strings.TrimSpace(login)
	if name == "" {
		return nil, ErrNotFound
	}
	if strings.Contains(name, "@") {
		return s.UserByAddress(name)
	}
	return s.UserByName(name)
}

// UserByAddress resolves a recipient or sender address to the account that
// owns it: an exact address first, then plus addressing
// (user+tag@example.com -> user@example.com).
func (s *Store) UserByAddress(address string) (*User, error) {
	addr := normalizeEmail(address)
	if addr == "" {
		return nil, ErrNotFound
	}
	user, err := s.userByAlias(addr)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	at := strings.LastIndex(addr, "@")
	if at <= 0 {
		return nil, ErrNotFound
	}
	local := addr[:at]
	if i := strings.Index(local, "+"); i > 0 {
		return s.userByAlias(local[:i] + addr[at:])
	}
	return nil, ErrNotFound
}

// userByAlias finds the account that owns an address, primary or not.
func (s *Store) userByAlias(addr string) (*User, error) {
	var row userRow
	if err := s.get(&row, `SELECT u.id AS id, u.name AS name,
			COALESCE(p.address, '') AS address, u.display_name AS display_name,
			u.password_hash AS password_hash, u.folder_seq AS folder_seq,
			u.created_at AS created_at
		FROM aliases a
		JOIN users u ON u.id = a.user_id
		LEFT JOIN aliases p ON p.user_id = u.id AND p.is_primary = 1
		WHERE a.address = ?`, addr); err != nil {
		return nil, err
	}
	return row.user(), nil
}
