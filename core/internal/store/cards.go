package store

import (
	"context"
	"database/sql"
	"errors"
)

// Card is a whitelisted memory card. A row existing at all means the card is
// "known"; unknown cards have no row until the user decides what to do with
// them ("always ignore" creates a row with policy=ignore).
type Card struct {
	ID           int64  `json:"id"`
	VolumeSerial string `json:"volume_serial"`
	Label        string `json:"label"`
	Alias        string `json:"alias"`
	Policy       string `json:"policy"` // "copy" | "ignore"
	CreatedAt    string `json:"created_at"`
	LastSeenAt   string `json:"last_seen_at,omitempty"`
}

type CardRepo struct{ db *sql.DB }

var ErrNotFound = errors.New("store: not found")

const cardCols = "id, volume_serial, COALESCE(label,''), alias, policy, created_at, COALESCE(last_seen_at,'')"

func scanCard(row interface{ Scan(...any) error }) (Card, error) {
	var c Card
	err := row.Scan(&c.ID, &c.VolumeSerial, &c.Label, &c.Alias, &c.Policy, &c.CreatedAt, &c.LastSeenAt)
	return c, err
}

// TouchSeen updates label and last_seen_at for a known card and returns it.
// Returns (zero, false, nil) when the serial is not whitelisted.
func (r *CardRepo) TouchSeen(ctx context.Context, serial, label string) (Card, bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE cards SET label = ?, last_seen_at = ? WHERE volume_serial = ?`,
		label, now(), serial)
	if err != nil {
		return Card{}, false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Card{}, false, nil
	}
	c, err := r.FindBySerial(ctx, serial)
	return c, err == nil, err
}

func (r *CardRepo) FindBySerial(ctx context.Context, serial string) (Card, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+cardCols+` FROM cards WHERE volume_serial = ?`, serial)
	c, err := scanCard(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Card{}, ErrNotFound
	}
	return c, err
}

func (r *CardRepo) Get(ctx context.Context, id int64) (Card, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+cardCols+` FROM cards WHERE id = ?`, id)
	c, err := scanCard(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Card{}, ErrNotFound
	}
	return c, err
}

// Create registers a card. Alias defaults to label, then serial.
func (r *CardRepo) Create(ctx context.Context, serial, label, alias, policy string) (Card, error) {
	if alias == "" {
		alias = label
	}
	if alias == "" {
		alias = serial
	}
	if policy == "" {
		policy = "copy"
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO cards (volume_serial, label, alias, policy, created_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		serial, label, alias, policy, now(), now())
	if err != nil {
		return Card{}, err
	}
	return r.FindBySerial(ctx, serial)
}

func (r *CardRepo) List(ctx context.Context) ([]Card, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+cardCols+` FROM cards ORDER BY alias COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Card
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *CardRepo) Update(ctx context.Context, id int64, alias, policy string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE cards SET alias = ?, policy = ? WHERE id = ?`, alias, policy, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *CardRepo) Delete(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM cards WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
