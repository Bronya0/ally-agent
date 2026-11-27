package app

import (
	"errors"
	"strings"
	"testing"
)

// The update path must abort when a background service refuses to stop:
// replacing files under an unconfirmed process tree corrupts the installation.
func TestUpdateStopServicesPropagatesFailure(t *testing.T) {
	t.Parallel()
	a := &App{}
	a.stopServicesStub = func() error { return errors.New("service svc-1 still alive") }
	err := a.stopAllServicesForUpdate()
	if err == nil || !strings.Contains(err.Error(), "svc-1 still alive") {
		t.Fatalf("stop failure swallowed: %v", err)
	}

	// Without the stub the real path runs: an empty registry stops nothing.
	a.stopServicesStub = nil
	if err := a.stopAllServicesForUpdate(); err != nil {
		t.Fatalf("empty registry reported an error: %v", err)
	}
}
