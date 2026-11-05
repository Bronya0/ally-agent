package app

import (
	"ally-dev/internal/tools/grep"
	"context"
	"path/filepath"
	"strings"
	"time"
)

// SearchExplorerFiles includes hidden/ignored project files, but prunes the
// shared dependency, build and cache directories during rg traversal.
func (a *App) SearchExplorerFiles(ctx context.Context, workspace, query string) (ListFilesResult, error) {
	cfg, err := a.configForWorkspace(workspace)
	if err != nil {
		return ListFilesResult{}, err
	}
	root, err := workspaceRoot(cfg)
	if err != nil {
		return ListFilesResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	query = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(query, "\\", "/")))
	args := []string{"--files", "--hidden", "--no-ignore"}
	for _, dir := range grep.ExcludedDirs() {
		args = append(args, "--iglob", "!"+dir+"/**", "--iglob", "!**/"+dir+"/**")
	}
	result := ListFilesResult{}
	_, result.Truncated, err = streamWorkspacePathsWithRipgrep(ctx, root,
		args, func(path string) bool {
			path = normalizeExplorerSearchPath(path)
			candidate := filepath.Base(filepath.FromSlash(path))
			if strings.Contains(query, "/") {
				candidate = path
			}
			if query == "" || !strings.Contains(strings.ToLower(candidate), query) {
				return true
			}
			if len(result.Entries) >= 1000 {
				return false
			}
			result.Entries = append(result.Entries, FileEntry{Path: path, Name: filepath.Base(filepath.FromSlash(path))})
			return true
		})
	return result, err
}

func normalizeExplorerSearchPath(path string) string {
	return strings.TrimPrefix(strings.ReplaceAll(path, "\\", "/"), "./")
}
