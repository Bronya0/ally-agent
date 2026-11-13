// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"testing"
)

func TestAPIStateRefreshPreservesDraft(t *testing.T) {
	a := &desktop{}
	initial := backend.ApiServiceState{Port: 8080, Token: "saved"}
	a.applyAPIState(initial, "", 0)
	if a.settings.API.Token != "saved" || a.settings.APIPort != 8080 {
		t.Fatal("initial service state was not loaded")
	}

	// A status refresh or a service toggle must keep earlier unsaved changes.
	a.settings.API.Token = ""
	a.settings.APIPort = 9090
	updated := backend.ApiServiceState{Port: 8080, Token: "saved", Enabled: true}
	a.applyAPIState(updated, "", 9090)
	if !a.settings.API.Enabled || a.settings.API.Token != "" || a.settings.APIPort != 9090 {
		t.Fatal("service status update discarded the draft")
	}

	// Edits made after a request starts must survive its response, too.
	a.settings.API.Token = "typed while loading"
	a.settings.APIPort = 9191
	a.applyAPIState(updated, "", 9090)
	if a.settings.API.Token != "typed while loading" || a.settings.APIPort != 9191 {
		t.Fatal("late response discarded newer input")
	}

	// Returning fields to their saved values lets later refreshes update them.
	a.settings.API.Token = "saved"
	a.settings.APIPort = 8080
	updated.Token, updated.Port = "changed elsewhere", 8181
	a.applyAPIState(updated, "saved", 8080)
	if a.settings.API.Token != updated.Token || a.settings.APIPort != float64(updated.Port) {
		t.Fatal("clean fields did not follow the service state")
	}
}
