// Package store owns the SQLite database: schema migrations and typed
// repositories. modernc.org/sqlite keeps the build CGO-free.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DB wraps the sql handle and exposes the repositories.
type DB struct {
	sql *sql.DB

	Cards     *CardRepo
	Slots     *SlotRepo
	SlotNames *SlotNameLogRepo
	Jobs      *JobRepo
	Files     *FileRepo
	Settings  *SettingsRepo
}

// Open opens (creating if needed) the database at path, applies PRAGMAs and
// any pending embedded migrations.
//
// A single connection serializes all writes, which together with WAL mode
// satisfies the "serialized writes" requirement with zero SQLITE_BUSY
// handling; the write rate here (progress flushes) is far below any limit.
func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)", path)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := migrate(sqlDB); err != nil {
		sqlDB.Close()
		return nil, err
	}
	db := &DB{sql: sqlDB}
	db.Cards = &CardRepo{db: sqlDB}
	db.Slots = &SlotRepo{db: sqlDB}
	db.SlotNames = &SlotNameLogRepo{db: sqlDB}
	db.Jobs = &JobRepo{db: sqlDB}
	db.Files = &FileRepo{db: sqlDB}
	db.Settings = &SettingsRepo{db: sqlDB}
	return db, nil
}

func (d *DB) Close() error { return d.sql.Close() }

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("creating schema_migrations: %w", err)
	}
	applied := map[string]bool{}
	rows, err := db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	names, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		if applied[name] {
			continue
		}
		body, err := migrationsFS.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("applying %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			name, now()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// now returns the canonical timestamp format used across all tables.
func now() string { return time.Now().UTC().Format(time.RFC3339) }

// ExecContext is a small escape hatch for callers with one-off statements
// (kept narrow on purpose; repositories cover the normal paths).
func (d *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.sql.ExecContext(ctx, query, args...)
}
