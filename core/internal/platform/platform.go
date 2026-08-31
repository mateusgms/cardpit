// Package platform abstracts every OS-specific operation behind small
// interfaces. The Windows implementation lives in platform/win (build-tagged);
// platform/fake is a directory-based implementation used for development and
// tests on any OS. Everything above this seam is portable.
package platform

import (
	"context"
	"errors"
)

// VolumeID identifies an attached removable volume for the duration of one
// attach session. GUIDPath is the stable identity ("\\?\Volume{...}\" on
// Windows, "fake://slot/card" on the fake).
type VolumeID struct {
	DriveLetter string // "E:" — empty on the fake
	GUIDPath    string
}

// VolumeInfo is everything the engine needs to know about an attached volume.
type VolumeInfo struct {
	Serial     string // volume serial number, "%08X"
	Label      string
	Filesystem string
	TotalBytes uint64
	FreeBytes  uint64
	Root       string // directory files are read from: "E:\" or the fake card dir
}

// DestCandidate describes a volume that can be offered to the user as the
// copy destination (a fixed drive on Windows; the configured dest dir on the
// fake).
type DestCandidate struct {
	DriveLetter string // "D:" — empty on the fake
	GUIDPath    string // "\\?\Volume{...}\" (or "fake-dest")
	Label       string
	Filesystem  string
	TotalBytes  uint64
	FreeBytes   uint64
	System      bool   // the OS system drive; shown to the user with a warning
	DeviceName  string // hardware model, e.g. "Samsung Portable SSD T5"
	Removable   bool   // Windows reports removable media for the backing disk
}

// SlotKey is the stable identity of a physical reader slot: the USB location
// path of the reader device plus the LUN (multi-slot readers expose several
// volumes on one device, differing only by LUN).
type SlotKey struct {
	LocationPath string
	LUN          int
}

// ReaderSlot is a reader interface Windows can see even when no volume is
// mounted. DeviceName is diagnostic/display metadata; Key is the identity.
type ReaderSlot struct {
	Key        SlotKey
	DeviceName string
}

var (
	// ErrSlotUnknown means the letter→port chain could not be resolved.
	// Ingestion proceeds; the job just cannot name a calibrated slot.
	ErrSlotUnknown = errors.New("platform: slot could not be resolved")

	// ErrDestNotPresent means the destination volume GUID is not mounted.
	ErrDestNotPresent = errors.New("platform: destination volume not present")
)

type VolumeLister interface {
	// ListRemovableVolumes returns the removable volumes currently present.
	// Called on every poll tick; must be cheap.
	ListRemovableVolumes(ctx context.Context) ([]VolumeID, error)
}

type VolumeInfoReader interface {
	VolumeInfo(ctx context.Context, v VolumeID) (VolumeInfo, error)
}

type SlotResolver interface {
	// ResolveSlot maps a volume to its physical slot. Best-effort: returns
	// ErrSlotUnknown when the chain breaks.
	ResolveSlot(ctx context.Context, v VolumeID) (SlotKey, error)
}

type Ejector interface {
	Eject(ctx context.Context, v VolumeID) error
}

type DestResolver interface {
	// ResolveDest maps a destination volume GUID to its current mount path,
	// or ErrDestNotPresent. The destination may be a fixed drive.
	ResolveDest(ctx context.Context, volumeGUID string) (string, error)
}

type FreeSpacer interface {
	FreeSpace(ctx context.Context, path string) (uint64, error)
}

type DestCandidateLister interface {
	// ListDestCandidates returns the volumes that can be offered as the copy
	// destination. Called on demand from the UI, never on a poll loop.
	ListDestCandidates(ctx context.Context) ([]DestCandidate, error)
}

type ReaderSlotLister interface {
	// ListReaderSlots returns USB removable-media disk interfaces. Some
	// readers expose no interface until a card is inserted, so this is
	// intentionally best-effort.
	ListReaderSlots(ctx context.Context) ([]ReaderSlot, error)
}

// Platform bundles the full set; app wiring picks the implementation.
type Platform struct {
	Volumes  VolumeLister
	Info     VolumeInfoReader
	Slots    SlotResolver
	Eject    Ejector
	Dest     DestResolver
	Space    FreeSpacer
	DestList DestCandidateLister
	Readers  ReaderSlotLister
}
