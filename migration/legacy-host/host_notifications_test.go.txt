//go:build !server

package app

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestNotificationFailuresAreLoggedWithRateLimit(t *testing.T) {
	var output bytes.Buffer
	a := &App{errorLogger: slog.New(slog.NewJSONHandler(&output, nil))}
	s := &SafeNotificationsService{app: a}
	failure := errors.New("notification daemon unavailable")
	s.logFailure(failure)
	s.logFailure(failure)
	if strings.Count(output.String(), "notification daemon unavailable") != 1 {
		t.Fatalf("expected one diagnostic during cooldown: %s", output.String())
	}
	s.lastErrorAt.Store(time.Now().Add(-2 * time.Minute).UnixNano())
	s.logFailure(failure)
	if strings.Count(output.String(), "notification daemon unavailable") != 2 {
		t.Fatalf("diagnostics did not resume after cooldown: %s", output.String())
	}
}
