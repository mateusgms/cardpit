-- Append-only log of automatic slot name assignments. Names recorded here
-- are never handed out again, so physical labels on the readers stay valid
-- even after a slot is deleted or a reader is replaced.
CREATE TABLE slot_name_log (
  id            INTEGER PRIMARY KEY,
  alias         TEXT NOT NULL,
  location_path TEXT NOT NULL,
  lun           INTEGER NOT NULL DEFAULT 0,
  assigned_at   TEXT NOT NULL
);
