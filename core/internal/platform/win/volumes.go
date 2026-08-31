//go:build windows

package win

import (
	"context"
	"errors"
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
		Volumes: w, Info: w, Slots: w, Eject: w, Dest: w, Space: w, DestList: w, Readers: w,
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
		// DRIVE_REMOVABLE alone is not enough: some portable SSD bridges use
		// it too. A storage descriptor that explicitly says the backing disk
		// is not removable media is a destination disk, not a source card.
		if devNum, err := volumeDeviceNumber(guid); err == nil {
			if meta, err := diskMetadataByNumber(devNum); err == nil && !meta.removable {
				continue
			}
		}
		out = append(out, platform.VolumeID{DriveLetter: letter, GUIDPath: guid})
	}
	return out, nil
}

// ListDestCandidates enumerates mounted local volumes by stable volume GUID,
// rather than only drive letters. Portable SSDs are variously reported as
// DRIVE_FIXED or DRIVE_REMOVABLE and may be mounted into a directory.
func (w *winPlatform) ListDestCandidates(ctx context.Context) ([]platform.DestCandidate, error) {
	volumes, err := mountedVolumes()
	if err != nil {
		return nil, err
	}
	sysRoot := strings.ToUpper(strings.TrimSuffix(os.Getenv("SystemDrive"), `\`) + `\`)
	var out []platform.DestCandidate
	for _, vol := range volumes {
		rootP, err := windows.UTF16PtrFromString(vol.mount)
		if err != nil {
			continue
		}
		driveType := windows.GetDriveType(rootP)
		if driveType != windows.DRIVE_FIXED && driveType != windows.DRIVE_REMOVABLE {
			continue
		}
		cand := platform.DestCandidate{
			DriveLetter: driveLetter(vol.mount),
			GUIDPath:    vol.guid,
			System:      strings.EqualFold(vol.mount, sysRoot),
			Removable:   driveType == windows.DRIVE_REMOVABLE,
		}
		// Best effort: a BitLocker-locked or dying volume still shows up,
		// just without label/sizes.
		if info, err := volumeInfoForRoot(vol.mount); err == nil {
			cand.Label = info.Label
			cand.Filesystem = info.Filesystem
			cand.TotalBytes = info.TotalBytes
			cand.FreeBytes = info.FreeBytes
		}
		if devNum, err := volumeDeviceNumber(vol.guid); err == nil {
			if meta, err := diskMetadataByNumber(devNum); err == nil {
				cand.DeviceName = meta.name
				cand.Removable = meta.removable
			}
		}
		out = append(out, cand)
	}
	return out, nil
}

type mountedVolume struct{ guid, mount string }

func mountedVolumes() ([]mountedVolume, error) {
	buf := make([]uint16, 1024)
	h, err := windows.FindFirstVolume(&buf[0], uint32(len(buf)))
	if err != nil {
		return nil, fmt.Errorf("FindFirstVolume: %w", err)
	}
	defer windows.FindVolumeClose(h)
	var out []mountedVolume
	for {
		guid := windows.UTF16ToString(buf)
		guidP, convErr := windows.UTF16PtrFromString(guid)
		if convErr == nil {
			paths := make([]uint16, 4096)
			var returned uint32
			if err := windows.GetVolumePathNamesForVolumeName(guidP, &paths[0], uint32(len(paths)), &returned); err == nil {
				for _, mount := range splitUTF16MultiSz(paths) {
					if mount != "" {
						out = append(out, mountedVolume{guid: guid, mount: mount})
						break
					}
				}
			}
		}
		for i := range buf {
			buf[i] = 0
		}
		if err := windows.FindNextVolume(h, &buf[0], uint32(len(buf))); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				break
			}
			return nil, fmt.Errorf("FindNextVolume: %w", err)
		}
	}
	return out, nil
}

func driveLetter(mount string) string {
	if len(mount) >= 3 && mount[1] == ':' && (mount[2] == '\\' || mount[2] == '/') {
		return strings.ToUpper(mount[:2])
	}
	return ""
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
	return volumeInfoForRoot(root)
}

func volumeInfoForRoot(root string) (platform.VolumeInfo, error) {
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
