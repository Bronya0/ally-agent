//go:build windows

// SPDX-License-Identifier: GPL-3.0-only
package chatgpt

import (
	"errors"
	"golang.org/x/sys/windows"
	"unsafe"
)

type systemProtector struct{}

func NewSystemProtector() Protector                      { return systemProtector{} }
func (systemProtector) Seal(data []byte) ([]byte, error) { return dpapi(data, false) }
func (systemProtector) Open(data []byte) ([]byte, error) { return dpapi(data, true) }
func dpapi(data []byte, decrypt bool) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty secure record")
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	var err error
	if decrypt {
		err = windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		err = windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, int(out.Size))...), nil
}
