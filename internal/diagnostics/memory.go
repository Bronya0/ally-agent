// SPDX-License-Identifier: GPL-3.0-only
// Package diagnostics exports on-demand, local runtime profiles without a server.
package diagnostics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"sync"
	"time"
)

var captureMu sync.Mutex

type ProcessMemory struct {
	WorkingSetBytes uint64 `json:"workingSetBytes,omitempty"`
	PrivateBytes    uint64 `json:"privateBytes,omitempty"`
	Error           string `json:"error,omitempty"`
}

type Sample struct {
	Stage      string           `json:"stage"`
	Time       time.Time        `json:"time"`
	Process    ProcessMemory    `json:"process"`
	Go         runtime.MemStats `json:"go"`
	Goroutines int              `json:"goroutines"`
}

type Report struct {
	PID       int            `json:"pid"`
	GoVersion string         `json:"goVersion"`
	Platform  string         `json:"platform"`
	Counts    map[string]int `json:"uiCounts"`
	Samples   []Sample       `json:"samples"`
}

func sample(stage string) Sample {
	s := Sample{Stage: stage, Time: time.Now(), Process: processMemory(), Goroutines: runtime.NumGoroutine()}
	runtime.ReadMemStats(&s.Go)
	return s
}

// Capture writes allocation stacks, not chat contents or credentials. It runs GC
// and scavenging only on explicit request; never use it as periodic leak cleanup.
// counts must be a detached snapshot, not a live UI map.
func Capture(root string, counts map[string]int) (string, error) {
	captureMu.Lock()
	defer captureMu.Unlock()
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(root, "memory-"+time.Now().Format("20060102-150405")+"-")
	if err != nil {
		return "", err
	}
	report := Report{PID: os.Getpid(), GoVersion: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH, Counts: counts}
	report.Samples = append(report.Samples, sample("before_gc"))
	runtime.GC()
	report.Samples = append(report.Samples, sample("after_gc"))
	// Sample memory before profile serialization allocates its own buffers.
	debug.FreeOSMemory()
	report.Samples = append(report.Samples, sample("after_release"))
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return dir, err
	}
	if err := os.WriteFile(filepath.Join(dir, "memory.json"), data, 0600); err != nil {
		return dir, err
	}
	for _, name := range []string{"heap", "allocs", "goroutine"} {
		if err := writeProfile(filepath.Join(dir, name+".pprof"), name); err != nil {
			return dir, err
		}
	}
	return dir, nil
}

func writeProfile(path, name string) error {
	profile := pprof.Lookup(name)
	if profile == nil {
		return fmt.Errorf("profile unavailable: %s", name)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	err = profile.WriteTo(f, 0)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
