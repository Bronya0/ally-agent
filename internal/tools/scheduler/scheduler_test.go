// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package scheduler

import (
	"strings"
	"testing"
	"time"
)

func TestValidateScheduleRejectsAtDescriptors(t *testing.T) {
	// @every/@interval descriptors are duration schedules; accepting them in the
	// cron branch would bypass the MinInterval floor and let a task fire every
	// few seconds.
	sched, err := ParseSchedule("@every 10s")
	if err != nil {
		t.Fatalf("ParseSchedule must classify the descriptor as cron: %v", err)
	}
	if sched.Type != "cron" {
		t.Fatalf("expected cron type for @every, got %q", sched.Type)
	}
	if err := ValidateSchedule(sched); err == nil {
		t.Fatalf("@every must not bypass MinInterval (%s): %+v", MinInterval, sched)
	}
}

func TestValidateScheduleIntervalFloor(t *testing.T) {
	// The floor is enforced at parse time already, with the same error code
	// the validator uses, so a raw tool call with a sub-floor duration can
	// never reach the scheduler.
	if _, err := ParseSchedule("10s"); err == nil {
		t.Fatalf("ParseSchedule(10s) must enforce the %s floor", MinInterval)
	}
	// ValidateSchedule re-checks the floor so a hand-constructed Schedule
	// (e.g. from NormalizeSchedule on untrusted input) is still rejected.
	subFloor := Schedule{Type: "interval", Every: "10s"}
	if err := ValidateSchedule(subFloor); err == nil {
		t.Fatalf("interval below %s must be rejected: %+v", MinInterval, subFloor)
	}

	ok, err := ParseSchedule("2h")
	if err != nil {
		t.Fatalf("ParseSchedule(2h) failed: %v", err)
	}
	if err := ValidateSchedule(ok); err != nil {
		t.Fatalf("valid interval must pass: %v", err)
	}
	base := time.Unix(0, 0).UTC()
	next, err := NextRunAt(ok, base)
	if err != nil {
		t.Fatalf("NextRunAt failed: %v", err)
	}
	if !next.After(base) {
		t.Fatalf("interval must schedule a future run, got %s", next)
	}
}

func TestValidateScheduleAcceptsStandardCron(t *testing.T) {
	sched, err := ParseSchedule("*/15 * * * *")
	if err != nil {
		t.Fatalf("ParseSchedule failed: %v", err)
	}
	if sched.Type != "cron" || !strings.Contains(sched.Cron, "*/15") {
		t.Fatalf("unexpected schedule: %+v", sched)
	}
	if err := ValidateSchedule(sched); err != nil {
		t.Fatalf("standard cron must pass validation: %v", err)
	}
}
