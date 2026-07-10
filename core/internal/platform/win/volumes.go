//go:build windows

package win

import (
	"context"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"

	"github.com/mateusgms/cardpit/core/internal/platform"
)

// winPlatform implements every platform interface with real Win32 calls.
type winPlatform struct{}

// New assembles the Windows platform bundle.
func New() platform.Platform {
	w := &winPlatform{}
	return platform.Platform{
		Volumes: w, Info: w, Slots: w, Eject: w, Dest: w, DestList: w, Space: w,
	}
}

func (w *winPlatform) ListRemovableVolumes(ctx context.Context) ([]platform.VolumeID, error) {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil, fmt.Errorf("GetLogicalDrives: %w", err)
	}
	var out []platform.VolumeID
	for i := 0; i < 26; i++ {
		if mask&(1<<i) == 0 {
			continue
		}
		letter := string(rune('A'+i)) + ":"
		root, err := windows.UTF16PtrFromString(letter + `\`)
		if err != nil {
			continue
		}
		if windows.GetDriveType(root) != windows.DRIVE_REMOVABLE {
			continue
		}
		guid, err := volumeGUIDForRoot(letter + `\`)
		if err != nil {
			// Volume can vanish between the two calls; skip quietly.
			continue
		}
		out = append(out, platform.VolumeID{DriveLetter: letter, GUIDPath: guid})
	}
	return out, nil
}

// ListDestCandidates enumerates fixed drives the user can pick as the
// destination in the UI. Best-effort per drive: a volume whose info cannot be
// read (e.g. locked BitLocker) is still listed, with label/fs/sizes empty.
func (w *winPlatform) ListDestCandidates(ctx context.Context) ([]platform.DestCandidate, error) {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil, fmt.Errorf("GetLogicalDrives: %w", err)
	}
	sysDrive := strings.ToUpper(os.Getenv("SystemDrive")) // "C:"
	var out []platform.DestCandidate
	for i := 0; i < 26; i++ {
		if mask&(1<<i) == 0 {
			continue
		}
		letter := string(rune('A'+i)) + ":"
		root, err := windows.UTF16PtrFromString(letter + `\`)
		if err != nil {
			continue
		}
		if windows.GetDriveType(root) != windows.DRIVE_FIXED {
			continue
		}
		guid, err := volumeGUIDForRoot(letter + `\`)
		if err != nil {
			// Volume can vanish between the two calls; skip quietly.
			continue
		}
		cand := platform.DestCandidate{
			DriveLetter: letter,
			GUIDPath:    guid,
			System:      letter == sysDrive,
		}
		if info, err := w.VolumeInfo(ctx, platform.VolumeID{DriveLetter: letter, GUIDPath: guid}); err == nil {
			cand.Label = info.Label
			cand.Filesystem = info.Filesystem
			cand.TotalBytes = info.TotalBytes
			cand.FreeBytes = info.FreeBytes
		}
		out = append(out, cand)
	}
	return out, nil
}

// volumeGUIDForRoot maps "E:\" to `\\?\Volume{...}\`.
func volumeGUIDForRoot(root string) (string, error) {
	rootP, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, 50) // documented minimum for volume GUID paths
	if err := windows.GetVolumeNameForVolumeMountPoint(rootP, &buf[0], uint32(len(buf))); err != nil {
		return "", fmt.Errorf("GetVolumeNameForVolumeMountPoint(%s): %w", root, err)
	}
	return windows.UTF16ToString(buf), nil
}

func (w *winPlatform) VolumeInfo(ctx context.Context, v platform.VolumeID) (platform.VolumeInfo, error) {
	root := v.DriveLetter + `\`
	rootP, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return platform.VolumeInfo{}, err
	}

	var serial, maxComp, fsFlags uint32
	label := make([]uint16, windows.MAX_PATH+1)
	fsName := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumeInformation(rootP,
		&label[0], uint32(len(label)),
		&serial, &maxComp, &fsFlags,
		&fsName[0], uint32(len(fsName))); err != nil {
		return platform.VolumeInfo{}, fmt.Errorf("GetVolumeInformation(%s): %w", root, err)
	}

	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(rootP, &free, &total, &totalFree); err != nil {
		return platform.VolumeInfo{}, fmt.Errorf("GetDiskFreeSpaceEx(%s): %w", root, err)
	}

	return platform.VolumeInfo{
		Serial:     fmt.Sprintf("%08X", serial),
		Label:      windows.UTF16ToString(label),
		Filesystem: windows.UTF16ToString(fsName),
		TotalBytes: total,
		FreeBytes:  free,
		Root:       root,
	}, nil
}

// ResolveDest maps a destination volume GUID path to its current mount
// point. Accepts the GUID with or without the trailing backslash.
func (w *winPlatform) ResolveDest(ctx context.Context, volumeGUID string) (string, error) {
	guid := volumeGUID
	if !strings.HasSuffix(guid, `\`) {
		guid += `\`
	}
	guidP, err := windows.UTF16PtrFromString(guid)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, 1024)
	var returned uint32
	if err := windows.GetVolumePathNamesForVolumeName(guidP, &buf[0], uint32(len(buf)), &returned); err != nil {
		return "", platform.ErrDestNotPresent
	}
	paths := splitUTF16MultiSz(buf)
	if len(paths) == 0 {
		// Volume exists but has no mount point — treat as absent, per the
		// "never copy to a fallback" rule.
		return "", platform.ErrDestNotPresent
	}
	return paths[0], nil
}

func (w *winPlatform) FreeSpace(ctx context.Context, path string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return 0, fmt.Errorf("GetDiskFreeSpaceEx(%s): %w", path, err)
	}
	return free, nil
}
