package app

import "fmt"

// ── Sub-agent management (frontend bindings) ──

// GetSubagents returns all sub-agent runs, both running and finished.
func (a *App) GetSubagents() []*SubagentRun {
	a.subRunsMu.Lock()
	defer a.subRunsMu.Unlock()
	result := make([]*SubagentRun, 0, len(a.subRuns))
	for _, r := range a.subRuns {
		result = append(result, cloneSubagentRun(r))
	}
	return result
}

func cloneSubagentRun(r *SubagentRun) *SubagentRun {
	if r == nil {
		return nil
	}
	c := *r
	c.cancel = nil
	c.FilesRead = append([]string(nil), r.FilesRead...)
	c.FilesEdited = append([]string(nil), r.FilesEdited...)
	c.ToolCalls = append([]SubToolEvent(nil), r.ToolCalls...)
	return &c
}

// StopSubagent cancels a running sub-agent.
func (a *App) StopSubagent(subID string) error {
	a.subRunsMu.Lock()
	run := a.subRuns[subID]
	if run == nil {
		a.subRunsMu.Unlock()
		return fmt.Errorf("sub-agent not found: %s", subID)
	}
	if run.Status != "running" {
		a.subRunsMu.Unlock()
		return fmt.Errorf("sub-agent is not running: %s", subID)
	}
	cancel := run.cancel
	a.subRunsMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}
