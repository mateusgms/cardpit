package store

import (
	"context"
	"database/sql"
	"errors"
)

// Slot is a calibrated physical reader slot, identified by the stable USB
// location path plus LUN (multi-slot readers share the path, differ by LUN).
type Slot struct {
	ID           int64  `json:"id"`
	LocationPath string `json:"location_path"`
	LUN          int    `json:"lun"`
	Alias        string `json:"alias"`
	CreatedAt    string `json:"created_at"`
}

type SlotRepo struct{ db *sql.DB }

const slotCols = "id, location_path, lun, alias, created_at"

func scanSlot(row interface{ Scan(...any) error }) (Slot, error) {
	var s Slot
	err := row.Scan(&s.ID, &s.LocationPath, &s.LUN, &s.Alias, &s.CreatedAt)
	return s, err
}

// Upsert creates or renames the slot for (locationPath, lun).
func (r *SlotRepo) Upsert(ctx context.Context, locationPath string, lun int, alias string) (Slot, error) {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO slots (location_path, lun, alias, created_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT (location_path, lun) DO UPDATE SET alias = excluded.alias`,
		locationPath, lun, alias, now())
	if err != nil {
		return Slot{}, err
	}
	s, _, err := r.FindByKey(ctx, locationPath, lun)
	return s, err
}

func (r *SlotRepo) FindByKey(ctx context.Context, locationPath string, lun int) (Slot, bool, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+slotCols+` FROM slots WHERE location_path = ? AND lun = ?`, locationPath, lun)
	s, err := scanSlot(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Slot{}, false, nil
	}
	return s, err == nil, err
}

func (r *SlotRepo) List(ctx context.Context) ([]Slot, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+slotCols+` FROM slots ORDER BY alias COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Slot
	for rows.Next() {
		s, err := scanSlot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *SlotRepo) Rename(ctx context.Context, id int64, alias string) error {
	res, err := r.db.ExecContext(ctx, `UPDATE slots SET alias = ? WHERE id = ?`, alias, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *SlotRepo) Delete(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM slots WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SlotNameEntry is one automatic slot name assignment. The log is append-only
// so assigned names are never reused, keeping physical labels on the readers
// valid even after a slot is deleted or a reader is replaced.
type SlotNameEntry struct {
	ID           int64  `json:"id"`
	Alias        string `json:"alias"`
	LocationPath string `json:"location_path"`
	LUN          int    `json:"lun"`
	AssignedAt   string `json:"assigned_at"`
}

type SlotNameLogRepo struct{ db *sql.DB }

func (r *SlotNameLogRepo) Append(ctx context.Context, alias, locationPath string, lun int) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO slot_name_log (alias, location_path, lun, assigned_at) VALUES (?, ?, ?, ?)`,
		alias, locationPath, lun, now())
	return err
}

func (r *SlotNameLogRepo) List(ctx context.Context) ([]SlotNameEntry, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, alias, location_path, lun, assigned_at FROM slot_name_log ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SlotNameEntry
	for rows.Next() {
		var e SlotNameEntry
		if err := rows.Scan(&e.ID, &e.Alias, &e.LocationPath, &e.LUN, &e.AssignedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// UsedAliases returns every alias ever assigned automatically, plus the count
// of log entries (the basis for the "Leitor N" fallback numbering).
func (r *SlotNameLogRepo) UsedAliases(ctx context.Context) (map[string]bool, int, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT alias FROM slot_name_log`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	used := map[string]bool{}
	n := 0
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, 0, err
		}
		used[a] = true
		n++
	}
	return used, n, rows.Err()
}
