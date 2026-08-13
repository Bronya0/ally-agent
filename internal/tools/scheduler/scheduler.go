// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
// Package scheduler holds the scheduled-task tool's pure helpers: schedule
// parsing (RFC3339 / duration / cron), per-type validation, next-run
// calculation, and the bounded steps/timeout/interval constants.
//
// Nothing here may depend on App state, ConfigState, or any *App receiver.
// Errors that need a stable tool error code (E_SCHEDULED_TASK_*) are wrapped
// using internal/tools/shared so callers can extract codes uniformly.
package scheduler

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	toolerrors "ally-dev/internal/tools/shared"
)

// Bounded execution limits. These mirror the historical app-layer constants
// so callers can reference them by name without depending on app state.
const (
	DefaultSteps        = 100
	MaxSteps            = 1000
	DefaultTimeout      = 3600
	MaxTimeout          = 86400
	MinTimeout          = 60
	MinInterval         = time.Minute
	MaxTasks            = 100
	SummaryLimit        = 128 * 1024
	ErrorTextLimit      = 8 * 1024
	ListTruncateAt      = 50
)

// Schedule is the pure, app-agnostic representation of a scheduled-task
// schedule. It mirrors app.ScheduledTaskSchedule but lives here so the pure
// parsing/validation logic has no app dependency.
type Schedule struct {
	Type  string `json:"type"`
	At    string `json:"at,omitempty"`
	Every string `json:"every,omitempty"`
	Cron  string `json:"cron,omitempty"`
}

// ParseSchedule inspects value and decides which schedule type it represents:
//   - RFC3339 timestamp -> "once"
//   - Go duration        -> "interval" (must be >= MinInterval)
//   - anything else      -> "cron" (validated later)
//
func ParseSchedule(value string) (Schedule, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Schedule{}, toolerrors.New("E_SCHEDULED_TASK_SCHEDULE", errors.New("schedule is required"))
	}
	if _, err := time.Parse(time.RFC3339, value); err == nil {
		return Schedule{Type: "once", At: value}, nil
	}
	if duration, err := time.ParseDuration(value); err == nil {
		if duration < MinInterval {
			return Schedule{}, toolerrors.New("E_SCHEDULED_TASK_INTERVAL", fmt.Errorf("interval must be at least %s", MinInterval))
		}
		return Schedule{Type: "interval", Every: value}, nil
	}
	return Schedule{Type: "cron", Cron: value}, nil
}

// NormalizeSchedule trims whitespace on every field and lowercases the type.
// It does NOT validate; call ValidateSchedule afterwards. It mutates s in
// place and returns it for convenience.
func NormalizeSchedule(s *Schedule) *Schedule {
	s.Type = strings.ToLower(strings.TrimSpace(s.Type))
	s.At = strings.TrimSpace(s.At)
	s.Every = strings.TrimSpace(s.Every)
	s.Cron = strings.TrimSpace(s.Cron)
	return s
}

// ValidateSchedule checks that a normalized schedule is well-formed:
//   - once:     At is a valid RFC3339 timestamp
//   - interval: Every is a valid Go duration >= MinInterval
//   - cron:     Cron is a valid cron expression
func ValidateSchedule(s Schedule) error {
	switch s.Type {
	case "once":
		if s.At == "" {
			return toolerrors.New("E_SCHEDULED_TASK_AT", errors.New("schedule.at is required for once"))
		}
		if _, err := time.Parse(time.RFC3339, s.At); err != nil {
			return toolerrors.New("E_SCHEDULED_TASK_AT", fmt.Errorf("invalid RFC3339 time: %w", err))
		}
	case "interval":
		duration, err := time.ParseDuration(s.Every)
		if err != nil {
			return toolerrors.New("E_SCHEDULED_TASK_INTERVAL", fmt.Errorf("invalid interval: %w", err))
		}
		if duration < MinInterval {
			return toolerrors.New("E_SCHEDULED_TASK_INTERVAL", fmt.Errorf("interval must be at least %s", MinInterval))
		}
	case "cron":
		if s.Cron == "" {
			return toolerrors.New("E_SCHEDULED_TASK_CRON", errors.New("schedule.cron is required for cron"))
		}
		if _, err := cron.ParseStandard(s.Cron); err != nil {
			return toolerrors.New("E_SCHEDULED_TASK_CRON", fmt.Errorf("invalid cron expression: %w", err))
		}
	default:
		return toolerrors.New("E_SCHEDULED_TASK_TYPE", errors.New("schedule.type must be once, interval, or cron"))
	}
	return nil
}

// ParseCron parses spec into a cron Schedule. Callers that need the next-run
func ParseCron(spec string) (cron.Schedule, error) {
	return cron.ParseStandard(spec)
}

// EveryDuration returns the cron.Schedule for a duration-based interval.
func EveryDuration(d time.Duration) cron.Schedule {
	return cron.Every(d)
}

// NextRunAt computes the next time the schedule should fire after now.
// For "once" schedules whose time has passed, returns an error.
func NextRunAt(s Schedule, now time.Time) (time.Time, error) {
	switch s.Type {
	case "once":
		at, err := time.Parse(time.RFC3339, s.At)
		if err != nil {
			return time.Time{}, toolerrors.New("E_SCHEDULED_TASK_AT", fmt.Errorf("invalid RFC3339 time: %w", err))
		}
		if !at.After(now) {
			return time.Time{}, errors.New("one-time schedule time has passed")
		}
		return at, nil
	case "interval":
		duration, err := time.ParseDuration(s.Every)
		if err != nil {
			return time.Time{}, toolerrors.New("E_SCHEDULED_TASK_INTERVAL", fmt.Errorf("invalid interval: %w", err))
		}
		return now.Add(duration), nil
	case "cron":
		schedule, err := cron.ParseStandard(s.Cron)
		if err != nil {
			return time.Time{}, toolerrors.New("E_SCHEDULED_TASK_CRON", fmt.Errorf("invalid cron expression: %w", err))
		}
		return schedule.Next(now), nil
	default:
		return time.Time{}, toolerrors.New("E_SCHEDULED_TASK_TYPE", errors.New("schedule.type must be once, interval, or cron"))
	}
}

// ValidateSteps clamps steps to the default when zero and rejects values
// above MaxSteps. Returns the effective value.
func ValidateSteps(steps int) (int, error) {
	if steps <= 0 {
		return DefaultSteps, nil
	}
	if steps > MaxSteps {
		return 0, toolerrors.New("E_SCHEDULED_TASK_STEPS", fmt.Errorf("maxSteps must be <= %d", MaxSteps))
	}
	return steps, nil
}

// ValidateTimeout clamps timeoutSeconds to the default when zero and rejects
// values outside [MinTimeout, MaxTimeout]. Returns the effective value.
func ValidateTimeout(timeoutSeconds int) (int, error) {
	if timeoutSeconds <= 0 {
		return DefaultTimeout, nil
	}
	if timeoutSeconds < MinTimeout || timeoutSeconds > MaxTimeout {
		return 0, toolerrors.New("E_SCHEDULED_TASK_TIMEOUT", fmt.Errorf("timeoutSeconds must be between %d and %d", MinTimeout, MaxTimeout))
	}
	return timeoutSeconds, nil
}
