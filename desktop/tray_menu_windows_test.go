// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"syscall"
	"testing"

	"github.com/egoist/mygo"
)

func TestSystemTrayMenuThemeRestoresNativeMode(t *testing.T) {
	set, flush := trayMenuThemeProcs()
	if set == 0 || flush == 0 {
		t.Skip("system tray menu theme override is only used on Windows 10 1903+")
	}
	const forceDark = 2
	original, _, _ := syscall.SyscallN(set, forceDark)
	syscall.SyscallN(flush)
	t.Cleanup(func() {
		syscall.SyscallN(set, original)
		syscall.SyscallN(flush)
	})
	source := mygo.Theme.Source()
	for _, panics := range []bool{false, true} {
		shown := false
		func() {
			defer func() {
				if got := recover(); (got != nil) != panics {
					t.Errorf("panic=%v, want panic=%v", got, panics)
				}
			}()
			withSystemTrayMenuTheme(func() {
				shown = true
				mode, _, _ := syscall.SyscallN(set, 1)
				if mode != 1 {
					t.Errorf("popup forced native mode %d instead of following the system", mode)
				}
				if mygo.Theme.Source() != source {
					t.Error("popup changed the app's appearance source")
				}
				if panics {
					panic("menu callback failed")
				}
			})
		}()
		if !shown {
			t.Error("popup callback was not run")
		}
		mode, _, _ := syscall.SyscallN(set, forceDark)
		if mode != forceDark {
			t.Errorf("native mode was not restored after popup: got %d", mode)
		}
		if mygo.Theme.Source() != source {
			t.Error("popup left the app's appearance source changed")
		}
	}
}
