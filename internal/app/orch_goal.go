// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

func goalSessionKey(sessionID string) string {
	if strings.TrimSpace(sessionID) == "" {
		return "__default__"
	}
	return sessionID
}

func (a *App) createGoal(sessionID string, objective string, completionCriterion string, maxTurns int) (*GoalState, error) {
	if strings.TrimSpace(objective) == "" {
		return nil, errors.New("objective is required")
	}
	if maxTurns <= 0 {
		maxTurns = 200
	}
	key := goalSessionKey(sessionID)
	goal := &GoalState{
		GoalID:              newID(),
		Objective:           objective,
		CompletionCriterion: completionCriterion,
		Status:              "active",
		TurnBudget:          maxTurns,
		TurnsUsed:           0,
		TokensUsed:          0,
		WallClockMs:         0,
		CreatedAt:           time.Now().Unix(),
	}
	a.mu.Lock()
	if existing := a.goalStates[key]; existing != nil && existing.Status == "active" {
		a.mu.Unlock()
		return nil, errors.New("an active goal already exists; complete or pause it before creating a new one")
	}
	a.goalStates[key] = goal
	a.mu.Unlock()
	a.emitGoalUpdate(sessionID, goal)
	return goal, nil
}

func (a *App) updateGoal(sessionID, status, reason string) (*GoalState, error) {
	a.mu.Lock()
	goal := a.goalStates[goalSessionKey(sessionID)]
	if goal == nil {
		a.mu.Unlock()
		return nil, errors.New("no active goal")
	}
	switch status {
	case "complete", "blocked", "paused":
		goal.Status = status
		goal.StatusReason = reason
	default:
		a.mu.Unlock()
		return nil, fmt.Errorf("invalid goal status: %s", status)
	}
	result := *goal
	a.mu.Unlock()
	a.emitGoalUpdate(sessionID, &result)
	return &result, nil
}

func (a *App) emitGoalUpdate(sessionID string, goal *GoalState) {
	if strings.TrimSpace(sessionID) == "" || goal == nil {
		return
	}
	cp := *goal
	a.emit("goal:update", map[string]any{"sessionId": sessionID, "goal": cp})
}

func (a *App) recordGoalTurn(sessionID string, tokens int, elapsed time.Duration) *GoalState {
	a.mu.Lock()
	goal := a.goalStates[goalSessionKey(sessionID)]
	if goal == nil || goal.Status != "active" {
		a.mu.Unlock()
		return nil
	}
	goal.TurnsUsed++
	goal.TokensUsed += tokens
	if goal.TurnsUsed == 1 {
		goal.WallClockMs = elapsed.Milliseconds()
	}
	result := *goal
	a.mu.Unlock()
	a.emitGoalUpdate(sessionID, &result)
	return &result
}

func (a *App) getActiveGoal(sessionID string) *GoalState {
	a.mu.Lock()
	defer a.mu.Unlock()
	goal := a.goalStates[goalSessionKey(sessionID)]
	if goal != nil && goal.Status == "active" {
		cp := *goal
		return &cp
	}
	return nil
}

func (a *App) getGoalResult(sessionID string) any {
	a.mu.Lock()
	defer a.mu.Unlock()
	goal := a.goalStates[goalSessionKey(sessionID)]
	if goal == nil {
		return map[string]any{"hasGoal": false}
	}
	return map[string]any{
		"hasGoal":   true,
		"goalId":    goal.GoalID,
		"objective": goal.Objective,
		"status":    goal.Status,
		"reason":    goal.StatusReason,
		"turnsUsed": goal.TurnsUsed,
		"maxTurns":  goal.TurnBudget,
	}
}

// GetGoal returns the current goal state for one frontend session.
func (a *App) GetGoal(sessionID string) any {
	return a.getGoalResult(sessionID)
}
