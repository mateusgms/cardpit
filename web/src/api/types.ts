// Mirrors of the Go JSON DTOs.

export interface Card {
  id: number
  volume_serial: string
  label: string
  alias: string
  policy: 'copy' | 'ignore'
  created_at: string
  last_seen_at?: string
}

export interface Slot {
  id: number
  location_path: string
  lun: number
  alias: string
  created_at: string
}

export interface Job {
  id: number
  card_id?: number
  slot_id?: number
  volume_serial: string
  card_label: string
  slot_location: string
  slot_lun: number
  status:
    | 'awaiting_decision'
    | 'pending'
    | 'copying'
    | 'verifying'
    | 'done'
    | 'failed'
    | 'cancelled'
  started_at: string
  finished_at?: string
  files_total: number
  bytes_total: number
  files_copied: number
  bytes_copied: number
  files_skipped: number
  files_failed: number
  error?: string
}

export interface IngestedFile {
  id: number
  job_id: number
  src_path: string
  dst_path: string
  size: number
  mtime: string
  xxhash: string
  media_type: 'photo' | 'video' | 'other'
}

export interface WatcherVolume {
  volume_guid: string
  attached: boolean
  serial: string
  label: string
  location_path: string
  lun: number
}

export interface Status {
  slots: Slot[] | null
  volumes: WatcherVolume[] | null
  active_jobs: Job[] | null
  dest_mounted: boolean
  dest_guid: string
  watcher_paused: boolean
  calibrating?: { alias: string; armed_at: string; deadline: string }
  ui_placeholder: boolean
  version: string
}

export interface JobEventPayload {
  job_id: number
  volume_guid: string
  card_alias: string
  slot_alias: string
  status: string
  files_total: number
  files_copied: number
  files_skipped: number
  files_failed: number
  bytes_total: number
  bytes_copied: number
  error?: string
}

export interface Settings {
  [key: string]: string
}
