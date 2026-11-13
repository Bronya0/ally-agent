// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestServiceShortLivedProcessRetainsBothOutputs(t *testing.T) {
	a := newIsolatedToolTestApp(t)
	t.Cleanup(a.stopAllServices)
	for i := 0; i < 12; i++ {
		info, err := a.startServiceWithConfig(context.Background(), ConfigState{Workspace: a.config.Workspace}, StartServiceRequest{Description: "short output regression", Command: "echo stdout-complete && echo stderr-complete 1>&2"})
		if err != nil {
			t.Fatal(err)
		}
		a.servicesMu.Lock()
		done := a.services[info.ID].waitDone
		a.servicesMu.Unlock()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("short service did not finish")
		}
		output, err := a.GetServiceOutput(info.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.Output, "stdout-complete") || !strings.Contains(output.Output, "stderr-complete") {
			t.Fatalf("process %d lost output: %q", i, output.Output)
		}
	}
}
