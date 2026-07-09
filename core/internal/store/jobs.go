package store

import (
	"context"
	"database/sql"
	"errors"
)

// Job statuses. awaiting_decision is an extension over the PRD enum: an
// unknown card was seen, the question went out via Telegram/UI, and the
// answer has not arrived yet — persisted so a restart keeps the question
// answerable.
const (
	StatusAwaitingDecision = "awaiting_decision"
	StatusPending          = "pending"
	StatusCopying          = "copying"
	StatusVerifying        = "verifying"
	StatusDone             = "done"
	StatusFailed           = "failed"
	StatusCancelled        = "cancelled"
)

type Job struct {
	ID           int64  `json:"id"`
	CardID       int64  `json:"card_id,omitempty"`
	SlotID       int64  `json:"slot_id,omitempty"`
	VolumeSerial string `json:"volume_serial"`
	CardLabel    string `json:"card_label"`
	SlotLocation string `json:"slot_location"`
	SlotLUN      int    `json:"slot_lun"`
	Status       string `json:"status"`
	StartedAt    string `json:"started_at"`
	FinishedAt   string `json:"finished_at,omitempty"`
	FilesTotal   int    `json:"files_total"`
	BytesTotal   int64  `json:"bytes_total"`
	FilesCopied  int    `json:"files_copied"`
	BytesCopied  int64  `json:"bytes_copied"`
	FilesSkipped int    `json:"files_skipped"`
	FilesFailed  int    `json:"files_failed"`
	Error        string `json:"error,omitempty"`
	TgMessageID  int64  `json:"tg_message_id,omitempty"`
}

type JobRepo struct{ db *sql.DB }

const jobCols = `id, COALESCE(card_id,0), COALESCE(slot_id,0), volume_serial, card_label,
	slot_location, slot_lun, status, started_at, COALESCE(finished_at,''),
	COALESCE(files_total,0), COALESCE(bytes_total,0), COALESCE(files_copied,0),
	COALESCE(bytes_copied,0), COALESCE(files_skipped,0), COALESCE(files_failed,0),
	COALESCE(error,''), COALESCE(tg_message_id,0)`

func scanJob(row interface{ Scan(...any) error }) (Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.CardID, &j.SlotID, &j.VolumeSerial, &j.CardLabel,
		&j.SlotLocation, &j.SlotLUN, &j.Status, &j.StartedAt, &j.FinishedAt,
		&j.FilesTotal, &j.BytesTotal, &j.FilesCopied, &j.BytesCopied,
		&j.FilesSkipped, &j.FilesFailed, &j.Error, &j.TgMessageID)
	return j, err
}

// Create inserts a new job; zero CardID/SlotID are stored as NULL.
func (r *JobRepo) Create(ctx context.Context, j Job) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO jobs (card_id, slot_id, volume_serial, card_label, slot_location,
		    slot_lun, status, started_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		nullableID(j.CardID), nullableID(j.SlotID), j.VolumeSerial, j.CardLabel,
		j.SlotLocation, j.SlotLUN, j.Status, now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func (r *JobRepo) Get(ctx context.Context, id int64) (Job, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE id = ?`, id)
	j, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return j, err
}

func (r *JobRepo) SetStatus(ctx context.Context, id int64, status string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE jobs SET status = ? WHERE id = ?`, status, id)
	return err
}

// SetCard fills card_id once an unknown card gets registered mid-flow.
func (r *JobRepo) SetCard(ctx context.Context, id, cardID int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE jobs SET card_id = ? WHERE id = ?`, cardID, id)
	return err
}

func (r *JobRepo) SetTotals(ctx context.Context, id int64, filesTotal int, bytesTotal int64, filesSkipped int) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET files_total = ?, bytes_total = ?, files_skipped = ? WHERE id = ?`,
		filesTotal, bytesTotal, filesSkipped, id)
	return err
}

func (r *JobRepo) UpdateProgress(ctx context.Context, id int64, filesCopied int, bytesCopied int64, filesFailed int) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET files_copied = ?, bytes_copied = ?, files_failed = ? WHERE id = ?`,
		filesCopied, bytesCopied, filesFailed, id)
	return err
}

// Finish records a terminal status; errMsg may be empty.
func (r *JobRepo) Finish(ctx context.Context, id int64, status, errMsg string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = ?, error = NULLIF(?, ''), finished_at = ? WHERE id = ?`,
		status, errMsg, now(), id)
	return err
}

func (r *JobRepo) SetTgMessageID(ctx context.Context, id, msgID int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE jobs SET tg_message_id = ? WHERE id = ?`, msgID, id)
	return err
}

// ListPage returns jobs newest-first plus the total row count.
func (r *JobRepo) ListPage(ctx context.Context, limit, offset int) ([]Job, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+jobCols+` FROM jobs ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, j)
	}
	return out, total, rows.Err()
}

// Active returns jobs in a non-terminal status, oldest first.
func (r *JobRepo) Active(ctx context.Context) ([]Job, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+jobCols+` FROM jobs
		 WHERE status IN (?, ?, ?, ?) ORDER BY id`,
		StatusAwaitingDecision, StatusPending, StatusCopying, StatusVerifying)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// FindAwaitingBySerial locates the open question for a card serial.
func (r *JobRepo) FindAwaitingBySerial(ctx context.Context, serial string) (Job, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+jobCols+` FROM jobs WHERE status = ? AND volume_serial = ?
		 ORDER BY id DESC LIMIT 1`, StatusAwaitingDecision, serial)
	j, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return j, err
}

// RecentCompleted returns the last N jobs with status "done", newest first.
func (r *JobRepo) RecentCompleted(ctx context.Context, limit int) ([]Job, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+jobCols+` FROM jobs WHERE status = ? ORDER BY id DESC LIMIT ?`,
		StatusDone, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// FailInterrupted is boot recovery: anything mid-copy when the process died
// becomes failed, and stale questions are cancelled (the watcher re-detects
// still-inserted cards and re-asks).
func (r *JobRepo) FailInterrupted(ctx context.Context, errMsg string) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = ?, error = ?, finished_at = ?
		 WHERE status IN (?, ?)`,
		StatusFailed, errMsg, now(), StatusCopying, StatusVerifying)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if _, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = ?, finished_at = ? WHERE status IN (?, ?)`,
		StatusCancelled, now(), StatusAwaitingDecision, StatusPending); err != nil {
		return n, err
	}
	return n, nil
}
