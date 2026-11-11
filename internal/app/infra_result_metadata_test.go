package app

import (
	"strings"
	"testing"
)

func TestFallbackRetainsUnavailableDataDiagnostics(t *testing.T) {
	var typedNil *ServiceInfo
	for _, data := range []any{nil, typedNil, make(chan int)} {
		got := modelFallbackResult("unknown", data, "fallback diagnostics")
		_, body, _ := strings.Cut(got, "\n\n")
		if body != "fallback diagnostics" {
			t.Fatalf("fallback lost for %T: %q", data, got)
		}
	}
	got := modelFallbackResult("unknown", map[string]int{"value": 42}, "stale fallback")
	_, body, _ := strings.Cut(got, "\n\n")
	if body != `{"value":42}` {
		t.Fatalf("available data not preferred: %q", got)
	}
}

func TestServiceResultsOnlyReportNonzeroExitCodes(t *testing.T) {
	for _, status := range []string{"running", "stopped", "failed"} {
		for _, exit := range []int{0, 7} {
			info := ServiceInfo{ID: "service", Status: status, ExitCode: exit}
			for _, got := range []string{
				renderServiceInfoResultForModel(info),
				renderServiceListResultForModel(ServiceListToolResult{Services: []ServiceSummary{{ID: info.ID, Status: status, ExitCode: exit}}}),
			} {
				if strings.Contains(got, " | exit=") != (exit != 0) {
					t.Fatalf("status=%s exit=%d: %s", status, exit, got)
				}
			}
		}
	}
}
