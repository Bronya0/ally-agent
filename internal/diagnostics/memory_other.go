// SPDX-License-Identifier: GPL-3.0-only
//go:build !windows

package diagnostics

func processMemory() ProcessMemory {
	return ProcessMemory{Error: "process counters unavailable on this platform"}
}
