package store

import (
	"context"
	"database/sql"
)

// IngestedFile is one successfully copied file; the table doubles as the
// permanent dedup index (size, mtime, xxhash).
type IngestedFile struct {
	ID        int64  `json:"id"`
	JobID     int64  `json:"job_id"`
	SrcPath   string `json:"src_path"`
	DstPath   string `json:"dst_path"`
	Size      int64  `json:"size"`
	Mtime     string `json:"mtime"`  // RFC3339Nano UTC
	XXHash    string `json:"xxhash"` // XXH3-64, "%016x"
	MediaType string `json:"media_type"`
}

type FileRepo struct{ db *sql.DB }

func (r *FileRepo) Insert(ctx context.Context, f IngestedFile) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO ingested_files (job_id, src_path, dst_path, size, mtime, xxhash, media_type)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		f.JobID, f.SrcPath, f.DstPath, f.Size, f.Mtime, f.XXHash, f.MediaType)
	return err
}

// HasSizeMtime reports whether any ingested file matches (size, mtime) —
// the cheap first stage of dedup. A hit means the caller must hash the
// source and confirm with ExistsHash before skipping.
func (r *FileRepo) HasSizeMtime(ctx context.Context, size int64, mtime string) (bool, error) {
	var one int
	err := r.db.QueryRowContext(ctx,
		`SELECT 1 FROM ingested_files WHERE size = ? AND mtime = ? LIMIT 1`,
		size, mtime).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// ExistsHash is the authoritative dedup check.
func (r *FileRepo) ExistsHash(ctx context.Context, size int64, mtime, xxhash string) (bool, error) {
	var one int
	err := r.db.QueryRowContext(ctx,
		`SELECT 1 FROM ingested_files WHERE size = ? AND mtime = ? AND xxhash = ? LIMIT 1`,
		size, mtime, xxhash).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (r *FileRepo) ListByJob(ctx context.Context, jobID int64, limit, offset int) ([]IngestedFile, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM ingested_files WHERE job_id = ?`, jobID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, job_id, src_path, dst_path, size, mtime, xxhash, media_type
		 FROM ingested_files WHERE job_id = ? ORDER BY id LIMIT ? OFFSET ?`,
		jobID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []IngestedFile
	for rows.Next() {
		var f IngestedFile
		if err := rows.Scan(&f.ID, &f.JobID, &f.SrcPath, &f.DstPath, &f.Size,
			&f.Mtime, &f.XXHash, &f.MediaType); err != nil {
			return nil, 0, err
		}
		out = append(out, f)
	}
	return out, total, rows.Err()
}

// LargestByJob returns the n biggest files of a job (for the PNG report).
func (r *FileRepo) LargestByJob(ctx context.Context, jobID int64, n int) ([]IngestedFile, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, job_id, src_path, dst_path, size, mtime, xxhash, media_type
		 FROM ingested_files WHERE job_id = ? ORDER BY size DESC LIMIT ?`, jobID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IngestedFile
	for rows.Next() {
		var f IngestedFile
		if err := rows.Scan(&f.ID, &f.JobID, &f.SrcPath, &f.DstPath, &f.Size,
			&f.Mtime, &f.XXHash, &f.MediaType); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// StatsByJob aggregates count and bytes per media type.
func (r *FileRepo) StatsByJob(ctx context.Context, jobID int64) (map[string]struct {
	Count int
	Bytes int64
}, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT media_type, COUNT(*), COALESCE(SUM(size),0)
		 FROM ingested_files WHERE job_id = ? GROUP BY media_type`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]struct {
		Count int
		Bytes int64
	}{}
	for rows.Next() {
		var mt string
		var s struct {
			Count int
			Bytes int64
		}
		if err := rows.Scan(&mt, &s.Count, &s.Bytes); err != nil {
			return nil, err
		}
		out[mt] = s
	}
	return out, rows.Err()
}
