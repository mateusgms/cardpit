package bus

// Payload structs, one per topic. They carry flat display-ready fields so
// subscribers (Telegram, SSE) rarely need extra lookups, and so this package
// stays dependency-free.

// VolumeAttached — TopicVolumeAttached.
type VolumeAttached struct {
	VolumeGUID string `json:"volume_guid"` // stable identity for this attach session
	Root       string `json:"root"`        // path files are read from ("E:\" or fake dir)
	Serial     string `json:"serial"`      // volume serial, "%08X"
	Label      string `json:"label"`
	Filesystem string `json:"filesystem"`
	TotalBytes uint64 `json:"total_bytes"`
	FreeBytes  uint64 `json:"free_bytes"`

	// Physical slot; LocationPath empty when resolution failed.
	SlotLocationPath string `json:"slot_location_path"`
	SlotLUN          int    `json:"slot_lun"`
}

// VolumeDetached — TopicVolumeDetached.
type VolumeDetached struct {
	VolumeGUID string `json:"volume_guid"`
}

// JobEvent — TopicJobStarted/Progress/Completed/Failed.
type JobEvent struct {
	JobID      int64  `json:"job_id"`
	VolumeGUID string `json:"volume_guid"`
	CardAlias  string `json:"card_alias"`
	SlotAlias  string `json:"slot_alias"`
	Status     string `json:"status"`

	FilesTotal   int   `json:"files_total"`
	FilesCopied  int   `json:"files_copied"`
	FilesSkipped int   `json:"files_skipped"`
	FilesFailed  int   `json:"files_failed"`
	BytesTotal   int64 `json:"bytes_total"`
	BytesCopied  int64 `json:"bytes_copied"`

	Error string `json:"error,omitempty"`
}

// CardUnknown — TopicCardUnknown: an unregistered card appeared and the
// unknown-card policy is "ask".
type CardUnknown struct {
	JobID      int64  `json:"job_id"`
	VolumeGUID string `json:"volume_guid"`
	Serial     string `json:"serial"`
	Label      string `json:"label"`
	SlotAlias  string `json:"slot_alias"`
}

// CardDecision — TopicCardDecision: the user answered (Telegram button or UI).
type CardDecision struct {
	Serial string `json:"serial"`
	Action string `json:"action"` // "copy" | "ignore" | "always_ignore"
}

// DestMissing — TopicDestMissing: destination SSD not mounted.
type DestMissing struct {
	VolumeGUID string `json:"volume_guid"` // card waiting to be copied
	CardAlias  string `json:"card_alias"`
	SlotAlias  string `json:"slot_alias"`
}

// SlotCalibrated — TopicSlotCalibrated: calibration wizard bound a slot.
type SlotCalibrated struct {
	SlotID       int64  `json:"slot_id"`
	Alias        string `json:"alias"`
	LocationPath string `json:"location_path"`
	LUN          int    `json:"lun"`
}

// SlotAutoNamed — TopicSlotAutoNamed: a never-seen reader slot was assigned
// a fixed name automatically, so the operator can label it physically.
type SlotAutoNamed struct {
	SlotID       int64  `json:"slot_id"`
	Alias        string `json:"alias"`
	LocationPath string `json:"location_path"`
	LUN          int    `json:"lun"`
}
