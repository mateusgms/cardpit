//go:build windows

package win

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// DPAPIBox seals secrets with Windows Data Protection.
//
// CRYPTPROTECT_LOCAL_MACHINE is required: the worker runs as LocalSystem
// while the tray runs in the user session, and both must read the same
// sealed settings rows.
type DPAPIBox struct{}

const (
	dpapiPrefix = "dpapi:"

	cryptprotectUIForbidden  = 0x1
	cryptprotectLocalMachine = 0x4
)

func (DPAPIBox) Seal(plain []byte) (string, error) {
	desc, err := windows.UTF16PtrFromString("cardpit")
	if err != nil {
		return "", err
	}
	in := windows.DataBlob{Size: uint32(len(plain))}
	if len(plain) > 0 {
		in.Data = &plain[0]
	}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, desc, nil, 0, nil,
		cryptprotectUIForbidden|cryptprotectLocalMachine, &out); err != nil {
		return "", fmt.Errorf("CryptProtectData: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	sealed := unsafe.Slice(out.Data, out.Size)
	return dpapiPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

func (DPAPIBox) Open(sealed string) ([]byte, error) {
	s, ok := strings.CutPrefix(sealed, dpapiPrefix)
	if !ok {
		return nil, fmt.Errorf("secret: blob was not sealed by DPAPI (prefix missing)")
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	in := windows.DataBlob{Size: uint32(len(raw))}
	if len(raw) > 0 {
		in.Data = &raw[0]
	}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil,
		cryptprotectUIForbidden, &out); err != nil {
		return nil, fmt.Errorf("CryptUnprotectData: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	plain := make([]byte, out.Size)
	copy(plain, unsafe.Slice(out.Data, out.Size))
	return plain, nil
}
