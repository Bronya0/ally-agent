// SPDX-License-Identifier: GPL-3.0-only
package pluginhost

import (
	"testing"
)

func TestPluginNotifyRPCRechecksAtUIDelivery(t *testing.T) {
	h, info, workspace, queue := queuedPluginHost(t)
	p := fakeServerPage(t, h, info, workspace, &fakePluginSurface{})
	h.pages = map[string]*page{info.ID: p}
	count := 0
	h.notify = func(string, string) { count++ }
	if code, body := pluginTestRPC(t, p, "notify", "old", "info"); code != 200 {
		t.Fatal(code, body)
	}
	h.Reload(info.ID)
	(<-queue)()
	if count != 0 {
		t.Fatal("queued old notification delivered after retirement")
	}
	pumpPluginUI(t, queue, func() bool { return h.retiring[info.ID] == nil })
}
