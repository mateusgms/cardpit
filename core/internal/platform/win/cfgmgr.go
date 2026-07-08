//go:build windows

package win

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Raw CfgMgr32 bindings. Everything here is loaded lazily so importing the
// package never fails; the first real call resolves the procs.
//
// The full drive-letter → USB-port chain is documented in
// docs/windows-syscalls.md.

var (
	cfgmgr32 = windows.NewLazySystemDLL("cfgmgr32.dll")

	procCMGetDeviceInterfaceListSizeW = cfgmgr32.NewProc("CM_Get_Device_Interface_List_SizeW")
	procCMGetDeviceInterfaceListW     = cfgmgr32.NewProc("CM_Get_Device_Interface_ListW")
	procCMGetDeviceInterfacePropertyW = cfgmgr32.NewProc("CM_Get_Device_Interface_PropertyW")
	procCMLocateDevNodeW              = cfgmgr32.NewProc("CM_Locate_DevNodeW")
	procCMGetParent                   = cfgmgr32.NewProc("CM_Get_Parent")
	procCMGetDevNodePropertyW         = cfgmgr32.NewProc("CM_Get_DevNode_PropertyW")
)

// CONFIGRET codes we care about.
const (
	crSuccess     = 0x00
	crBufferSmall = 0x1A
	crNoSuchValue = 0x25
)

type configRet uint32

func (cr configRet) ok() bool { return cr == crSuccess }
func (cr configRet) err(op string) error {
	return fmt.Errorf("%s: CONFIGRET 0x%02X", op, uint32(cr))
}

// devPropKey mirrors DEVPROPKEY.
type devPropKey struct {
	fmtid windows.GUID
	pid   uint32
}

// DEVPROP_TYPE_* values.
const (
	devPropTypeString     = 0x00000012
	devPropTypeStringList = 0x00002012
	devPropTypeUint32     = 0x00000007
)

var (
	// DEVPKEY_Device_InstanceId {78c34fc8-104a-4aca-9ea4-524d52996e57}, 256
	devpkeyDeviceInstanceID = devPropKey{
		fmtid: windows.GUID{Data1: 0x78c34fc8, Data2: 0x104a, Data3: 0x4aca,
			Data4: [8]byte{0x9e, 0xa4, 0x52, 0x4d, 0x52, 0x99, 0x6e, 0x57}},
		pid: 256,
	}
	// DEVPKEY_Device_LocationPaths {a45c254e-df1c-4efd-8020-67d146a850e0}, 37
	devpkeyDeviceLocationPaths = devPropKey{
		fmtid: windows.GUID{Data1: 0xa45c254e, Data2: 0xdf1c, Data3: 0x4efd,
			Data4: [8]byte{0x80, 0x20, 0x67, 0xd1, 0x46, 0xa8, 0x50, 0xe0}},
		pid: 37,
	}
	// DEVPKEY_Device_Address (same fmtid), 30 — for USB mass storage
	// children this address is the LUN.
	devpkeyDeviceAddress = devPropKey{
		fmtid: windows.GUID{Data1: 0xa45c254e, Data2: 0xdf1c, Data3: 0x4efd,
			Data4: [8]byte{0x80, 0x20, 0x67, 0xd1, 0x46, 0xa8, 0x50, 0xe0}},
		pid: 30,
	}

	// GUID_DEVINTERFACE_DISK {53f56307-b6bf-11d0-94f2-00a0c91efb8b}
	guidDevInterfaceDisk = windows.GUID{Data1: 0x53f56307, Data2: 0xb6bf, Data3: 0x11d0,
		Data4: [8]byte{0x94, 0xf2, 0x00, 0xa0, 0xc9, 0x1e, 0xfb, 0x8b}}
)

const cmGetDeviceInterfaceListPresent = 0 // only interfaces that are alive

// diskInterfacePaths returns the \\?\...#{GUID_DEVINTERFACE_DISK} device
// paths of every present disk.
func diskInterfacePaths() ([]string, error) {
	var size uint32
	r0, _, _ := procCMGetDeviceInterfaceListSizeW.Call(
		uintptr(unsafe.Pointer(&size)),
		uintptr(unsafe.Pointer(&guidDevInterfaceDisk)),
		0,
		cmGetDeviceInterfaceListPresent,
	)
	if cr := configRet(r0); !cr.ok() {
		return nil, cr.err("CM_Get_Device_Interface_List_SizeW")
	}
	if size <= 1 {
		return nil, nil
	}
	buf := make([]uint16, size)
	r0, _, _ = procCMGetDeviceInterfaceListW.Call(
		uintptr(unsafe.Pointer(&guidDevInterfaceDisk)),
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(size),
		cmGetDeviceInterfaceListPresent,
	)
	if cr := configRet(r0); !cr.ok() {
		return nil, cr.err("CM_Get_Device_Interface_ListW")
	}
	return splitUTF16MultiSz(buf), nil
}

// interfaceInstanceID resolves a device interface path to its device
// instance ID (e.g. USBSTOR\DISK&VEN_...\7&1F30C9&0&058F63666485&1).
func interfaceInstanceID(ifacePath string) (string, error) {
	p, err := windows.UTF16PtrFromString(ifacePath)
	if err != nil {
		return "", err
	}
	var propType uint32
	var size uint32
	r0, _, _ := procCMGetDeviceInterfacePropertyW.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&devpkeyDeviceInstanceID)),
		uintptr(unsafe.Pointer(&propType)),
		0,
		uintptr(unsafe.Pointer(&size)),
		0,
	)
	if cr := configRet(r0); cr != crBufferSmall && !cr.ok() {
		return "", cr.err("CM_Get_Device_Interface_PropertyW (size)")
	}
	buf := make([]byte, size)
	r0, _, _ = procCMGetDeviceInterfacePropertyW.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&devpkeyDeviceInstanceID)),
		uintptr(unsafe.Pointer(&propType)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		0,
	)
	if cr := configRet(r0); !cr.ok() {
		return "", cr.err("CM_Get_Device_Interface_PropertyW")
	}
	if propType != devPropTypeString {
		return "", fmt.Errorf("instance id: unexpected DEVPROP_TYPE 0x%X", propType)
	}
	return windows.UTF16ToString(bytesToUTF16(buf)), nil
}

// locateDevNode turns an instance ID into a devnode handle.
func locateDevNode(instanceID string) (uint32, error) {
	p, err := windows.UTF16PtrFromString(instanceID)
	if err != nil {
		return 0, err
	}
	var devInst uint32
	r0, _, _ := procCMLocateDevNodeW.Call(
		uintptr(unsafe.Pointer(&devInst)),
		uintptr(unsafe.Pointer(p)),
		0, // CM_LOCATE_DEVNODE_NORMAL
	)
	if cr := configRet(r0); !cr.ok() {
		return 0, cr.err("CM_Locate_DevNodeW")
	}
	return devInst, nil
}

func parentDevNode(devInst uint32) (uint32, error) {
	var parent uint32
	r0, _, _ := procCMGetParent.Call(
		uintptr(unsafe.Pointer(&parent)),
		uintptr(devInst),
		0,
	)
	if cr := configRet(r0); !cr.ok() {
		return 0, cr.err("CM_Get_Parent")
	}
	return parent, nil
}

// devNodeProperty reads a raw devnode property; returns nil when the
// property does not exist on this node.
func devNodeProperty(devInst uint32, key devPropKey, wantType uint32) ([]byte, error) {
	var propType uint32
	var size uint32
	r0, _, _ := procCMGetDevNodePropertyW.Call(
		uintptr(devInst),
		uintptr(unsafe.Pointer(&key)),
		uintptr(unsafe.Pointer(&propType)),
		0,
		uintptr(unsafe.Pointer(&size)),
		0,
	)
	cr := configRet(r0)
	if cr == crNoSuchValue {
		return nil, nil
	}
	if cr != crBufferSmall && !cr.ok() {
		return nil, cr.err("CM_Get_DevNode_PropertyW (size)")
	}
	if size == 0 {
		return nil, nil
	}
	buf := make([]byte, size)
	r0, _, _ = procCMGetDevNodePropertyW.Call(
		uintptr(devInst),
		uintptr(unsafe.Pointer(&key)),
		uintptr(unsafe.Pointer(&propType)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		0,
	)
	if cr := configRet(r0); !cr.ok() {
		return nil, cr.err("CM_Get_DevNode_PropertyW")
	}
	if propType != wantType {
		return nil, nil // property exists but is not the shape we expect
	}
	return buf[:size], nil
}

// --- utf16 helpers -----------------------------------------------------------

func bytesToUTF16(b []byte) []uint16 {
	out := make([]uint16, len(b)/2)
	for i := range out {
		out[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return out
}

// splitUTF16MultiSz splits a REG_MULTI_SZ-style double-NUL-terminated list.
func splitUTF16MultiSz(buf []uint16) []string {
	var out []string
	start := 0
	for i, c := range buf {
		if c == 0 {
			if i > start {
				out = append(out, windows.UTF16ToString(buf[start:i]))
			}
			start = i + 1
		}
	}
	return out
}
