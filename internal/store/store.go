// Package store is the persistence layer for the mail server.
//
// It targets SQLite (default, single file) and PostgreSQL (shared team
// deployments). Queries are written with ? placeholders and rebound by sqlx
// for PostgreSQL.
//
// The schema is owned by the numbered migrations in ./migrations and applied
// by this package on startup; there is no "create table if not exists"
// compatibility layer and no in-place column patching.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

var (
	// errNoBlobDir is returned when payload storage is not configured.
	errNoBlobDir = errors.New("store: blob directory is not configured")

	// ErrNotFound is returned when a row does not exist.
	ErrNotFound = errors.New("store: not found")
	// ErrDuplicate is returned on unique constraint violations.
	ErrDuplicate = errors.New("store: duplicate")
	// ErrPrimaryAddress is returned when deleting an account's default address.
	ErrPrimaryAddress = errors.New("store: address is the primary address of an account")
)

// Store is the database handle.
type Store struct {
	db      *sqlx.DB
	dialect string
	dsn     string
	blobs   Blobs
}

// Blobs stores attachment and MIME payloads outside the database. Keys are
// relative paths; the store writes them and reads them back when a body is
// requested.
type Blobs interface {
	Put(key string, data []byte) error
	Get(key string) ([]byte, error)
	Remove(key string) error
}

// SetBlobs configures payload storage. It is required before messages,
// attachments or queued mail can be written; the database only keeps
// searchable columns.
func (s *Store) SetBlobs(b Blobs) { s.blobs = b }

// Open connects to the database described by dsn, applies pending migrations
// and returns the handle.
//
// A dsn with a postgres:// or postgresql:// scheme opens PostgreSQL; anything
// else is a SQLite file path (default "lifemail.db").
func Open(dsn string) (*Store, error) {
	return OpenWithMigrations(dsn, true)
}

// OpenWithMigrations connects without applying migrations when apply is false.
// Deployments that run "lifemail db migrate" as a separate step use this so a
// server never waits on DDL at boot.
func OpenWithMigrations(dsn string, apply bool) (*Store, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		dsn = "lifemail.db"
	}
	dialect := dialectFor(dsn)
	if apply {
		if err := Migrate(dsn); err != nil {
			return nil, err
		}
	}
	db, err := sqlx.Open(driverFor(dialect), connectDSN(dsn, dialect))
	if err != nil {
		return nil, err
	}
	if dialect == "sqlite" {
		// WAL allows readers and the writer to make progress at the same time.
		// _txlock=immediate keeps two writers from failing midway through an
		// upgrade from a read transaction.
		db.SetMaxOpenConns(8)
		db.SetMaxIdleConns(4)
		db.SetConnMaxIdleTime(5 * time.Minute)
	} else {
		db.SetMaxOpenConns(10)
		db.SetMaxIdleConns(5)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, dialect: dialect, dsn: dsn}, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the underlying handle for packages that need a transaction.
func (s *Store) DB() *sqlx.DB { return s.db }

// Dialect reports the backing database ("sqlite" or "postgres").
func (s *Store) Dialect() string { return s.dialect }

// DSN reports the connection string the store was opened with.
func (s *Store) DSN() string { return s.dsn }

// dialectFor reports which engine a DSN addresses.
func dialectFor(dsn string) string {
	lower := strings.ToLower(dsn)
	if strings.HasPrefix(lower, "postgres://") || strings.HasPrefix(lower, "postgresql://") {
		return "postgres"
	}
	return "sqlite"
}

func driverFor(dialect string) string {
	if dialect == "postgres" {
		return "pgx"
	}
	return "sqlite"
}

// connectDSN adds the pragmas the server relies on. They are connection
// strings, not per-connection statements, so every pooled connection gets
// them.
func connectDSN(dsn, dialect string) string {
	if dialect == "postgres" {
		return dsn
	}
	if !strings.HasPrefix(dsn, "file:") {
		dsn = "file:" + dsn
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "_pragma=busy_timeout(10000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_txlock=immediate"
}

// migrateDSN returns a connection string that can execute a whole migration
// file at once. pgx refuses multi-statement strings in its default extended
// protocol, so the migration connection uses the simple protocol; sqlx
// handles SQLite's multi-statement Exec natively.
func migrateDSN(dsn, dialect string) string {
	if dialect != "postgres" {
		return connectDSN(dsn, dialect)
	}
	if strings.Contains(dsn, "default_query_exec_mode") {
		return dsn
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "default_query_exec_mode=simple_protocol"
}

func (s *Store) rebind(q string) string {
	if s.dialect == "postgres" {
		return sqlx.Rebind(sqlx.DOLLAR, q)
	}
	return q
}

func (s *Store) exec(q string, args ...any) (sql.Result, error) {
	return s.db.Exec(s.rebind(q), args...)
}

func (s *Store) query(q string, args ...any) (*sql.Rows, error) {
	return s.db.Query(s.rebind(q), args...)
}

func (s *Store) queryRow(q string, args ...any) *sql.Row {
	return s.db.QueryRow(s.rebind(q), args...)
}

// get runs a query that must return exactly one row.
func (s *Store) get(dest any, q string, args ...any) error {
	err := s.db.Get(dest, s.rebind(q), args...)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// getAll runs a query and scans every row into dest.
func (s *Store) getAll(dest any, q string, args ...any) error {
	return s.db.Select(dest, s.rebind(q), args...)
}

// inTx runs fn inside a transaction, rolling back on error or panic.
func (s *Store) inTx(fn func(tx *sqlx.Tx) error) error {
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func nowUnix() int64 { return time.Now().Unix() }

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// ClassForType maps an EAS folder type to a collection class name.
func ClassForType(t int) string {
	switch t {
	case 8:
		return "Calendar"
	case 9:
		return "Contacts"
	}
	return "Email"
}

// isUniqueViolation reports whether err came from a unique constraint. Both
// engines mention "unique" (SQLite, PostgreSQL "duplicate key ... unique"), so
// the message is enough and avoids driver-specific error types.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate key")
}

func wrapDuplicate(err error) error {
	if isUniqueViolation(err) {
		return ErrDuplicate
	}
	return err
}

func (s *Store) String() string {
	return fmt.Sprintf("store(%s)", s.dialect)
}
