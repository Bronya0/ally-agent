// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"strings"
	"testing"
)

func TestManagedServiceSnapshotKeepsBoundedPreview(t *testing.T) {
	buffer := newRollingBuffer(serviceOutputLimit)
	text := strings.Repeat("earlier output\n", 20000) + "latest line\n"
	_, _ = buffer.Write([]byte(text))
	svc := &managedService{output: buffer}
	info := svc.snapshot()
	if want := tailString(text, serviceOutputPreview); info.OutputTail != want {
		t.Fatalf("preview must keep only the last %d bytes, got %d", serviceOutputPreview, len(info.OutputTail))
	}
	if info.OutputBytes != int64(len(text)) || !info.OutputTruncated {
		t.Fatalf("preview must preserve lifetime counters: %+v", info)
	}
	full, _, _ := svc.outputSnapshot()
	if len(full) != 128*1024 || !strings.HasSuffix(full, "latest line\n") {
		t.Fatalf("full output must keep the latest 128 KiB, got %d bytes", len(full))
	}
	_, _ = buffer.Write([]byte(strings.Repeat("z", serviceOutputLimit)))
	if info.OutputTail != tailString(text, serviceOutputPreview) {
		t.Fatal("a published preview must not change when new output arrives")
	}
}

func BenchmarkManagedServiceSnapshot(b *testing.B) {
	buffer := newRollingBuffer(serviceOutputLimit)
	_, _ = buffer.Write([]byte(strings.Repeat("x", serviceOutputLimit)))
	svc := &managedService{output: buffer}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = svc.snapshot()
	}
}
