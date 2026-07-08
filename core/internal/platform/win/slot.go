//go:build windows

package win

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/mateusgms/cardpit/core/internal/platform"
)

// IOCTLs (winioctl.h — values are ABI-stable).
const (
	ioctlStorageGetDeviceNumber = 0x002D1080
	ioctlStorageEjectMedia      = 0x002D4808
	fsctlLockVolume             = 0x00090018
	fsctlDismountVolume         = 0x00090020

	fileDeviceDisk = 0x00000007
)

type storageDeviceNumber struct {
	DeviceType      uint32
	DeviceNumber    uint32
	PartitionNumber int32
}

// ResolveSlot walks the chain
//
//	volume GUID → physical disk number → disk device interface →
//	device instance ID → devnode → (LUN via DEVPKEY_Device_Address)
//	→ parent walk until DEVPKEY_Device_LocationPaths answers.
//
// The location path names the *reader device's* USB port and is stable
// across reboots and reinsertions; multi-slot readers share it and differ
// by LUN — exactly the PRD's slot identity (RF-02.2).
func (w *winPlatform) ResolveSlot(ctx context.Context, v platform.VolumeID) (platform.SlotKey, error) {
	devNum, err := volumeDeviceNumber(v.GUIDPath)
	if err != nil {
		return platform.SlotKey{}, fmt.Errorf("%w: %v", platform.ErrSlotUnknown, err)
	}

	instanceID, err := diskInstanceIDByNumber(devNum)
	if err != nil {
		return platform.SlotKey{}, fmt.Errorf("%w: %v", platform.ErrSlotUnknown, err)
	}

	devInst, err := locateDevNode(instanceID)
	if err != nil {
		return platform.SlotKey{}, fmt.Errorf("%w: %v", platform.ErrSlotUnknown, err)
	}

	lun := resolveLUN(devInst, instanceID)

	locationPath, err := findLocationPath(devInst)
	if err != nil {
		return platform.SlotKey{}, fmt.Errorf("%w: %v", platform.ErrSlotUnknown, err)
	}

	return platform.SlotKey{LocationPath: locationPath, LUN: lun}, nil
}

// volumeDeviceNumber opens the volume (GUID path without the trailing
// backslash) and asks which physical disk it lives on.
func volumeDeviceNumber(guidPath string) (uint32, error) {
	h, err := openDeviceHandle(strings.TrimSuffix(guidPath, `\`))
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(h)

	var sdn storageDeviceNumber
	var returned uint32
	if err := windows.DeviceIoControl(h, ioctlStorageGetDeviceNumber,
		nil, 0,
		(*byte)(unsafe.Pointer(&sdn)), uint32(unsafe.Sizeof(sdn)),
		&returned, nil); err != nil {
		return 0, fmt.Errorf("IOCTL_STORAGE_GET_DEVICE_NUMBER: %w", err)
	}
	return sdn.DeviceNumber, nil
}

// diskInstanceIDByNumber enumerates present disk interfaces and returns the
// instance ID of the disk whose device number matches.
func diskInstanceIDByNumber(devNum uint32) (string, error) {
	paths, err := diskInterfacePaths()
	if err != nil {
		return "", err
	}
	for _, p := range paths {
		h, err := openDeviceHandle(p)
		if err != nil {
			continue
		}
		var sdn storageDeviceNumber
		var returned uint32
		err = windows.DeviceIoControl(h, ioctlStorageGetDeviceNumber,
			nil, 0,
			(*byte)(unsafe.Pointer(&sdn)), uint32(unsafe.Sizeof(sdn)),
			&returned, nil)
		windows.CloseHandle(h)
		if err != nil || sdn.DeviceType != fileDeviceDisk || sdn.DeviceNumber != devNum {
			continue
		}
		return interfaceInstanceID(p)
	}
	return "", fmt.Errorf("no disk interface matches device number %d", devNum)
}

// openDeviceHandle opens a device path with zero access rights — enough for
// metadata IOCTLs without needing admin on the data path.
func openDeviceHandle(path string) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(p,
		0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, 0, 0)
}

// resolveLUN prefers DEVPKEY_Device_Address on the disk devnode (for
// USBSTOR children the address IS the LUN); falls back to the instance-ID
// suffix ("...&058F63666485&1" → 1); defaults to 0.
func resolveLUN(devInst uint32, instanceID string) int {
	if raw, err := devNodeProperty(devInst, devpkeyDeviceAddress, devPropTypeUint32); err == nil && len(raw) >= 4 {
		return int(binary.LittleEndian.Uint32(raw))
	}
	if i := strings.LastIndex(instanceID, "&"); i >= 0 && i+1 < len(instanceID) {
		if n, err := strconv.Atoi(instanceID[i+1:]); err == nil && n >= 0 && n < 64 {
			return n
		}
	}
	return 0
}

// findLocationPath climbs from the disk devnode toward the USB host
// controller and returns the first non-empty DEVPKEY_Device_LocationPaths.
// USBSTOR-level nodes usually have none; the first ancestor that answers is
// the physical reader device — which is what calibration keys on.
func findLocationPath(devInst uint32) (string, error) {
	cur := devInst
	for depth := 0; depth < 10; depth++ {
		raw, err := devNodeProperty(cur, devpkeyDeviceLocationPaths, devPropTypeStringList)
		if err != nil {
			slog.Debug("win: location path read failed; climbing", "depth", depth, "err", err)
		}
		if len(raw) > 0 {
			if paths := splitUTF16MultiSz(bytesToUTF16(raw)); len(paths) > 0 && paths[0] != "" {
				return paths[0], nil
			}
		}
		parent, err := parentDevNode(cur)
		if err != nil {
			return "", fmt.Errorf("no ancestor with location paths: %w", err)
		}
		cur = parent
	}
	return "", fmt.Errorf("no location path within 10 ancestor levels")
}

// Eject flushes, locks, dismounts and ejects the volume — the physical
// "you may remove the card" signal (RF-03.7).
func (w *winPlatform) Eject(ctx context.Context, v platform.VolumeID) error {
	p, err := windows.UTF16PtrFromString(strings.TrimSuffix(v.GUIDPath, `\`))
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(p,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return fmt.Errorf("opening volume for eject: %w", err)
	}
	defer windows.CloseHandle(h)

	var returned uint32
	locked := false
	for attempt := 0; attempt < 5; attempt++ {
		if err := windows.DeviceIoControl(h, fsctlLockVolume,
			nil, 0, nil, 0, &returned, nil); err == nil {
			locked = true
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	if !locked {
		return fmt.Errorf("FSCTL_LOCK_VOLUME: volume busy after retries")
	}
	if err := windows.DeviceIoControl(h, fsctlDismountVolume,
		nil, 0, nil, 0, &returned, nil); err != nil {
		return fmt.Errorf("FSCTL_DISMOUNT_VOLUME: %w", err)
	}
	if err := windows.DeviceIoControl(h, ioctlStorageEjectMedia,
		nil, 0, nil, 0, &returned, nil); err != nil {
		return fmt.Errorf("IOCTL_STORAGE_EJECT_MEDIA: %w", err)
	}
	return nil
}
