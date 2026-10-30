// SPDX-License-Identifier: GPL-3.0-only
package diagnostics

import (
	"golang.org/x/sys/windows"
	"unsafe"
)

var getProcessMemoryInfo = windows.NewLazySystemDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")

func processMemory() ProcessMemory {
	var counters struct {
		Size, PageFaults                         uint32
		PeakWorkingSet, WorkingSet               uintptr
		QuotaPeakPagedPool, QuotaPagedPool       uintptr
		QuotaPeakNonPagedPool, QuotaNonPagedPool uintptr
		Pagefile, PeakPagefile, Private          uintptr
	}
	counters.Size = uint32(unsafe.Sizeof(counters))
	ok, _, err := getProcessMemoryInfo.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&counters)), uintptr(counters.Size))
	if ok == 0 {
		return ProcessMemory{Error: err.Error()}
	}
	return ProcessMemory{WorkingSetBytes: uint64(counters.WorkingSet), PrivateBytes: uint64(counters.Private)}
}
