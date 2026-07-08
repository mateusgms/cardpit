CREATE TABLE cards (
  id            INTEGER PRIMARY KEY,
  volume_serial TEXT NOT NULL UNIQUE,          -- GetVolumeInformation, "%08X"
  label         TEXT,
  alias         TEXT NOT NULL,                 -- "SanDisk 128 A"
  policy        TEXT NOT NULL DEFAULT 'copy'
                CHECK (policy IN ('copy','ignore')),
  created_at    TEXT NOT NULL,
  last_seen_at  TEXT
);

CREATE TABLE slots (
  id            INTEGER PRIMARY KEY,
  location_path TEXT NOT NULL,                 -- DEVPKEY_Device_LocationPaths
  lun           INTEGER NOT NULL DEFAULT 0,
  alias         TEXT NOT NULL,                 -- "Leitor esquerdo"
  created_at    TEXT NOT NULL,
  UNIQUE (location_path, lun)
);

CREATE TABLE jobs (
  id            INTEGER PRIMARY KEY,
  card_id       INTEGER REFERENCES cards(id),
  slot_id       INTEGER REFERENCES slots(id),
  -- Denormalized volume identity: lets awaiting_decision jobs be resolved
  -- by serial after a restart and history render without joins.
  volume_serial TEXT NOT NULL DEFAULT '',
  card_label    TEXT NOT NULL DEFAULT '',
  slot_location TEXT NOT NULL DEFAULT '',
  slot_lun      INTEGER NOT NULL DEFAULT 0,
  -- awaiting_decision: unknown card, waiting for the Telegram/UI answer
  -- (persisted so a restart does not lose the pending question).
  status        TEXT NOT NULL
                CHECK (status IN ('awaiting_decision','pending','copying',
                                  'verifying','done','failed','cancelled')),
  started_at    TEXT NOT NULL,
  finished_at   TEXT,
  files_total   INTEGER,
  bytes_total   INTEGER,
  files_copied  INTEGER,
  bytes_copied  INTEGER,
  files_skipped INTEGER,                       -- dedup
  files_failed  INTEGER,
  error         TEXT,
  tg_message_id INTEGER                        -- for editMessageText
);
CREATE INDEX idx_jobs_status ON jobs (status);

CREATE TABLE ingested_files (
  id         INTEGER PRIMARY KEY,
  job_id     INTEGER NOT NULL REFERENCES jobs(id),
  src_path   TEXT NOT NULL,
  dst_path   TEXT NOT NULL,
  size       INTEGER NOT NULL,
  mtime      TEXT NOT NULL,                    -- RFC3339 UTC
  xxhash     TEXT NOT NULL,                    -- XXH3-64, "%016x"
  media_type TEXT NOT NULL
             CHECK (media_type IN ('photo','video','other'))
);
CREATE INDEX idx_dedup ON ingested_files (size, mtime, xxhash);
CREATE INDEX idx_files_job ON ingested_files (job_id);

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
