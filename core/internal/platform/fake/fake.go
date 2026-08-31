// Package fake is a directory-based platform implementation for development
// and tests on any OS.
//
// Layout under the root directory:
//
//	root/
//	├── slot1/          ← a "reader slot" (location path FAKE#slot1)
//	│   └── CARD01/     ← an "inserted card" (files inside are the media)
//	└── slot2/
//
// Creating a card directory simulates insertion; removing (or ejecting,
// which renames it to .ejected-NAME) simulates removal. The card's serial
// is read from an optional .cardpit-serial file, else derived from the card
// directory name — stable across re-insertions, like a real volume serial.
package fake

import (
	"context"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"

	"github.com/mateusgms/cardpit/core/internal/platform"
)

const (
	serialFile    = ".cardpit-serial"
	freeSpaceFile = ".cardpit-freespace"
	guidPrefix    = "fake://"
)

// New returns a Platform whose slots live under root and whose destination
// "SSD" is destDir (resolved for any volume GUID as long as destDir exists).
func New(root, destDir string) platform.Platform {
	f := &fakePlatform{root: root, dest: destDir}
	return platform.Platform{
		Volumes: f, Info: f, Slots: f, Eject: f, Dest: f, Space: f, DestList: f, Readers: f,
	}
}

type fakePlatform struct {
	root string
	dest string
}

func (f *fakePlatform) ListRemovableVolumes(ctx context.Context) ([]platform.VolumeID, error) {
	slots, err := os.ReadDir(f.root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []platform.VolumeID
	for _, slot := range slots {
		if !slot.IsDir() || strings.HasPrefix(slot.Name(), ".") {
			continue
		}
		cards, err := os.ReadDir(filepath.Join(f.root, slot.Name()))
		if err != nil {
			continue // slot dir vanished mid-scan
		}
		for _, card := range cards {
			if !card.IsDir() || strings.HasPrefix(card.Name(), ".") {
				continue
			}
			out = append(out, platform.VolumeID{
				GUIDPath: guidPrefix + slot.Name() + "/" + card.Name(),
			})
		}
	}
	return out, nil
}

// cardDir maps a fake volume GUID back to its directory.
func (f *fakePlatform) cardDir(v platform.VolumeID) (dir, slot, card string, err error) {
	rest, ok := strings.CutPrefix(v.GUIDPath, guidPrefix)
	if !ok {
		return "", "", "", fmt.Errorf("fake: not a fake volume: %q", v.GUIDPath)
	}
	slot, card, ok = strings.Cut(rest, "/")
	if !ok || slot == "" || card == "" {
		return "", "", "", fmt.Errorf("fake: malformed volume id: %q", v.GUIDPath)
	}
	return filepath.Join(f.root, slot, card), slot, card, nil
}

func (f *fakePlatform) VolumeInfo(ctx context.Context, v platform.VolumeID) (platform.VolumeInfo, error) {
	dir, _, card, err := f.cardDir(v)
	if err != nil {
		return platform.VolumeInfo{}, err
	}
	if _, err := os.Stat(dir); err != nil {
		return platform.VolumeInfo{}, err
	}
	serial := fmt.Sprintf("%08X", crc32.ChecksumIEEE([]byte(card)))
	if b, err := os.ReadFile(filepath.Join(dir, serialFile)); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			serial = s
		}
	}
	var total uint64
	filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += uint64(info.Size())
		}
		return nil
	})
	return platform.VolumeInfo{
		Serial:     serial,
		Label:      card,
		Filesystem: "fakefs",
		TotalBytes: total,
		FreeBytes:  0,
		Root:       dir,
	}, nil
}

func (f *fakePlatform) ResolveSlot(ctx context.Context, v platform.VolumeID) (platform.SlotKey, error) {
	_, slot, _, err := f.cardDir(v)
	if err != nil {
		return platform.SlotKey{}, platform.ErrSlotUnknown
	}
	return platform.SlotKey{LocationPath: "FAKE#" + slot, LUN: 0}, nil
}

func (f *fakePlatform) ListReaderSlots(ctx context.Context) ([]platform.ReaderSlot, error) {
	entries, err := os.ReadDir(f.root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []platform.ReaderSlot
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			out = append(out, platform.ReaderSlot{
				Key:        platform.SlotKey{LocationPath: "FAKE#" + entry.Name()},
				DeviceName: "Fake reader " + entry.Name(),
			})
		}
	}
	return out, nil
}

func (f *fakePlatform) Eject(ctx context.Context, v platform.VolumeID) error {
	dir, slot, card, err := f.cardDir(v)
	if err != nil {
		return err
	}
	return os.Rename(dir, filepath.Join(f.root, slot, ".ejected-"+card))
}

func (f *fakePlatform) ResolveDest(ctx context.Context, volumeGUID string) (string, error) {
	if info, err := os.Stat(f.dest); err == nil && info.IsDir() {
		return f.dest, nil
	}
	return "", platform.ErrDestNotPresent
}

// ListDestCandidates offers the configured dest dir as the single candidate,
// under the magic GUID "fake-dest" that ResolveDest accepts.
func (f *fakePlatform) ListDestCandidates(ctx context.Context) ([]platform.DestCandidate, error) {
	info, err := os.Stat(f.dest)
	if err != nil || !info.IsDir() {
		return nil, nil
	}
	free, _ := f.FreeSpace(ctx, f.dest)
	return []platform.DestCandidate{{
		GUIDPath:   "fake-dest",
		Label:      "Destino fake (" + f.dest + ")",
		Filesystem: "fakefs",
		TotalBytes: free,
		FreeBytes:  free,
	}}, nil
}

// FreeSpace reports a huge default so tests never trip the space check by
// accident; a .cardpit-freespace file in the directory overrides it.
func (f *fakePlatform) FreeSpace(ctx context.Context, path string) (uint64, error) {
	if b, err := os.ReadFile(filepath.Join(path, freeSpaceFile)); err == nil {
		var n uint64
		if _, err := fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &n); err == nil {
			return n, nil
		}
	}
	return 1 << 40, nil // 1 TiB
}
