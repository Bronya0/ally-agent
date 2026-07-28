package app

// Section 4: Grep (was grep.go)
// App-owned grep orchestration that binds internal/tools/grep ripgrep wrapper
// to workspace resolution, readable-path resolution, safety checks, and the
// dependency:missing event sink.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	goruntime "runtime"

	"ally-dev/internal/tools/grep"
)

func (a *App) GrepFiles(req GrepRequest) (*GrepResult, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return a.grepFilesWithConfig(ctx, a.effectiveConfig(ConfigState{}), req)
}

func (a *App) grepFilesWithConfig(ctx context.Context, cfg ConfigState, req GrepRequest) (*GrepResult, error) {
	root, err := workspaceRoot(cfg)
	if err != nil {
		return nil, codedToolError("E_GREP_WORKSPACE", err)
	}
	if strings.TrimSpace(req.Pattern) == "" {
		return nil, codedToolError("E_GREP_BAD_PATTERN", errors.New("grep_files requires a non-empty pattern"))
	}
	searchRoot := root
	if strings.TrimSpace(req.Path) != "" {
		searchRoot, err = resolveReadablePath(cfg, req.Path)
		if err != nil {
			return nil, codedToolError("E_GREP_PATH", err)
		}
	}
	if _, err := os.Stat(searchRoot); err != nil {
		return nil, codedToolError("E_GREP_PATH", err)
	}

	// Safety: block broad/system searches only outside the selected workspace.
	if !insideRoot(root, searchRoot) {
		if blocked, reason := isDangerousSearchRoot(searchRoot); blocked {
			return nil, codedToolError("E_SEARCH_ROOT_BLOCKED", fmt.Errorf("%s\n\nThis search has been blocked for safety. If you need to search this path, do it manually.", reason))
		}
	}

	rgPath, err := grep.Find()
	if err != nil {
		a.emitRipgrepMissingIfNeeded()
		return nil, grep.MissingError()
	}

	timeoutSeconds := grep.TimeoutSeconds(req)
	grepCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	result, err := grep.Search(grepCtx, rgPath, root, searchRoot, req)
	if err != nil {
		return nil, grep.NormalizeError(err, timeoutSeconds)
	}
	return result, nil
}

func displayPathForConfig(cfg ConfigState, p string) string {
	root, err := workspaceRoot(cfg)
	if err != nil {
		return filepath.ToSlash(filepath.Clean(p))
	}
	return grep.DisplayPathForRoot(root, p)
}

func (a *App) emitRipgrepMissingIfNeeded() {
	if _, err := grep.Find(); err == nil {
		return
	}
	a.emit("dependency:missing", map[string]any{
		"tool":         "rg",
		"name":         "ripgrep",
		"message":      "grep_files requires ripgrep (`rg`), but it was not found. Install ripgrep and restart Ally.",
		"installSteps": grep.InstallInstructions(),
	})
}

func (a *App) emitGitBashMissingIfNeeded() {
	if goruntime.GOOS != "windows" {
		return
	}
	cfg, err := a.getConfig()
	if err != nil {
		return
	}
	if _, bashName := findWindowsBash(cfg.GitBashPath); bashName != "" {
		return
	}
	a.emit("dependency:missing", map[string]any{
		"tool":       "bash",
		"name":       "Git Bash",
		"messageKey": "app.dependency.gitBashMissing",
		"message":    "Git Bash was not found. run_command will fall back to PowerShell. Install Git for Windows, or set the Git Bash path in Settings → General → Git Bash Path.",
		"installStepKeys": []string{
			"app.dependency.gitBashDownload",
			"app.dependency.gitBashConfigure",
		},
		"installSteps": []string{
			"Download from https://git-scm.com/download/win",
			"Or set the path manually in Settings, e.g. C:\\Program Files\\Git\\bin\\bash.exe",
		},
	})
}
