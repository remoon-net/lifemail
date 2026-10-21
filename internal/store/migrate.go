package store

import (
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// MigrationInfo describes one migration file and whether it has been applied.
type MigrationInfo struct {
	Version   int
	Name      string
	Applied   bool
	AppliedAt int64
}

type migration struct {
	version  int
	name     string
	file     string
	body     string
	checksum string
}

// migrationTableSQL is created by the runner itself, so an empty database only
// needs the embedded files to be usable.
const migrationTableSQL = `CREATE TABLE IF NOT EXISTS schema_migrations (
  version BIGINT PRIMARY KEY,
  name TEXT NOT NULL,
  checksum TEXT NOT NULL,
  applied_at BIGINT NOT NULL
)`

// Migrate applies every pending migration for the engine named by dsn.
//
// Each file runs inside a transaction. A file that has already been applied is
// never executed again; if its checksum changed, startup fails instead of
// silently drifting from what is deployed.
func Migrate(dsn string) error {
	_, err := migrate(dsn, nil)
	return err
}

// MigrateWithLog applies pending migrations and reports each one through log.
// It returns how many migrations were applied.
func MigrateWithLog(dsn string, log func(version int, name string)) (int, error) {
	return migrate(dsn, log)
}

// MigrationStatus lists the migrations known to this binary together with the
// ones recorded in the database. It does not change anything.
func MigrationStatus(dsn string) ([]MigrationInfo, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		dsn = "lifemail.db"
	}
	dialect := dialectFor(dsn)
	db, err := sql.Open(driverFor(dialect), migrateDSN(dsn, dialect))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, err
	}

	applied := map[int]int64{}
	rows, err := db.Query(`SELECT version, applied_at FROM schema_migrations`)
	if err != nil {
		// An unmigrated database has no bookkeeping table yet.
		if !strings.Contains(strings.ToLower(err.Error()), "no such table") &&
			!strings.Contains(strings.ToLower(err.Error()), "does not exist") {
			return nil, err
		}
	} else {
		for rows.Next() {
			var version, at int64
			if err := rows.Scan(&version, &at); err != nil {
				rows.Close()
				return nil, err
			}
			applied[int(version)] = at
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}

	list, err := loadMigrations(dialect)
	if err != nil {
		return nil, err
	}
	out := make([]MigrationInfo, 0, len(list))
	for _, m := range list {
		info := MigrationInfo{Version: m.version, Name: m.name}
		if at, ok := applied[m.version]; ok {
			info.Applied = true
			info.AppliedAt = at
			delete(applied, m.version)
		}
		out = append(out, info)
	}
	for version := range applied {
		out = append(out, MigrationInfo{Version: version, Name: "(unknown to this binary)", Applied: true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

func migrate(dsn string, log func(version int, name string)) (int, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		dsn = "lifemail.db"
	}
	dialect := dialectFor(dsn)
	db, err := sql.Open(driverFor(dialect), migrateDSN(dsn, dialect))
	if err != nil {
		return 0, err
	}
	defer db.Close()
	// One connection: migrations are serial and must not race each other.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return 0, err
	}
	if _, err := db.Exec(migrationTableSQL); err != nil {
		return 0, fmt.Errorf("store: create schema_migrations: %w", err)
	}

	type appliedRow struct {
		checksum string
	}
	applied := map[int]appliedRow{}
	rows, err := db.Query(`SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var version int
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			rows.Close()
			return 0, err
		}
		applied[version] = appliedRow{checksum: checksum}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}

	list, err := loadMigrations(dialect)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, m := range list {
		if prev, ok := applied[m.version]; ok {
			if prev.checksum != m.checksum {
				return count, fmt.Errorf(
					"store: migration %04d_%s was applied with a different body; add a new migration instead of editing it",
					m.version, m.name)
			}
			continue
		}
		if err := applyOne(db, dialect, m); err != nil {
			return count, err
		}
		count++
		if log != nil {
			log(m.version, m.name)
		}
	}
	return count, nil
}

func applyOne(db *sql.DB, dialect string, m migration) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(m.body); err != nil {
		return fmt.Errorf("store: migration %04d_%s: %w", m.version, m.name, err)
	}
	insert := sqlx.Rebind(placeholderStyle(dialect),
		`INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (?, ?, ?, ?)`)
	if _, err := tx.Exec(insert, m.version, m.name, m.checksum, nowUnix()); err != nil {
		return fmt.Errorf("store: record migration %04d_%s: %w", m.version, m.name, err)
	}
	return tx.Commit()
}

func placeholderStyle(dialect string) int {
	if dialect == "postgres" {
		return sqlx.DOLLAR
	}
	return sqlx.QUESTION
}

// loadMigrations reads the files for one dialect and returns them ordered by
// version. File names are "NNNN_name.<dialect>.sql".
func loadMigrations(dialect string) ([]migration, error) {
	names, err := fs.Glob(migrationFS, "migrations/*."+dialect+".sql")
	if err != nil {
		return nil, err
	}
	out := make([]migration, 0, len(names))
	seen := map[int]string{}
	for _, name := range names {
		base := path.Base(name)
		rest := strings.TrimSuffix(base, "."+dialect+".sql")
		prefix, label, ok := strings.Cut(rest, "_")
		if !ok {
			return nil, fmt.Errorf("store: migration file %q must look like NNNN_name.%s.sql", base, dialect)
		}
		version, err := strconv.Atoi(prefix)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("store: migration file %q has an invalid version", base)
		}
		if other, dup := seen[version]; dup {
			return nil, fmt.Errorf("store: migrations %q and %q share version %d", other, base, version)
		}
		seen[version] = base
		body, err := migrationFS.ReadFile(name)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		out = append(out, migration{
			version:  version,
			name:     label,
			file:     base,
			body:     string(body),
			checksum: hex.EncodeToString(sum[:]),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	if len(out) == 0 {
		return nil, errors.New("store: no migrations embedded for " + dialect)
	}
	return out, nil
}

// MigrationStatus reports the migrations known to this binary and whether the
// connected database has applied them.
func (s *Store) MigrationStatus() ([]MigrationInfo, error) {
	return MigrationStatus(s.dsn)
}
