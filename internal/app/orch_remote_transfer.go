// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ally-dev/internal/tools/pathutil"
	toolshared "ally-dev/internal/tools/shared"
)

const remoteTransferMarker = "ALLY_REMOTE_TRANSFER_JSON:"

// Transfer mode uses raw file bytes on stdin/stdout and JSON on stderr.
var remoteTransferPythonScript = strings.Replace(strings.Replace(remotePythonScript, "TRANSFER_MODE = False", "TRANSFER_MODE = True", 1), "__DELETE_SENSITIVE_TARGETS__", "[]", 1)

type RemoteTransferFile struct {
	LocalPath  string `json:"localPath"`
	RemotePath string `json:"remotePath"`
}

type RemoteTransferRequest struct {
	Alias     string               `json:"alias"`
	Action    string               `json:"action"`
	Files     []RemoteTransferFile `json:"files"`
	Overwrite bool                 `json:"overwrite,omitempty"`
}

type RemoteTransferFileResult struct {
	RemoteTransferFile
	OK         bool   `json:"ok"`
	Skipped    bool   `json:"skipped,omitempty"`
	TotalBytes int64  `json:"totalBytes"`
	Error      string `json:"error,omitempty"`
	ErrorCode  string `json:"errorCode,omitempty"`
}

type RemoteTransferResult struct {
	Action       string                     `json:"action"`
	Alias        string                     `json:"alias"`
	Files        []RemoteTransferFileResult `json:"files"`
	TotalBytes   int64                      `json:"totalBytes"`
	FileCount    int                        `json:"fileCount"`
	FailedCount  int                        `json:"failedCount"`
	SkippedCount int                        `json:"skippedCount"`
	Summary      string                     `json:"summary"`
}

type remoteTransferStats struct {
	TotalBytes int64  `json:"totalBytes"`
	Mode       uint32 `json:"mode"`
	ModTime    int64  `json:"modTime"`
}

func decodeRemoteTransferMarker(stderr []byte, out any) error {
	markerBytes := []byte(remoteTransferMarker)
	idx := bytes.LastIndex(stderr, markerBytes)
	if idx < 0 {
		msg := strings.TrimSpace(string(stderr))
		if msg == "" {
			return errors.New("remote transfer helper produced no response on stderr")
		}
		return fmt.Errorf("remote transfer failed: %s", msg)
	}
	line := bytes.TrimSpace(stderr[idx+len(markerBytes):])
	if end := bytes.IndexByte(line, '\n'); end >= 0 {
		line = line[:end]
	}
	var response remotePythonResponse
	if err := json.Unmarshal(line, &response); err != nil {
		return fmt.Errorf("parse remote transfer response: %w", err)
	}
	if !response.OK {
		if response.Error == "" {
			return errors.New("remote helper reported a transfer failure without an error message")
		}
		return remoteHelperError(response)
	}
	var stats struct {
		TotalBytes *int64  `json:"totalBytes"`
		Mode       *uint32 `json:"mode"`
		ModTime    *int64  `json:"modTime"`
	}
	if err := json.Unmarshal(response.Data, &stats); err != nil {
		return fmt.Errorf("invalid remote transfer statistics: %w", err)
	}
	if stats.TotalBytes == nil || *stats.TotalBytes < 0 || stats.Mode == nil || *stats.Mode > 0o777 || stats.ModTime == nil {
		return errors.New("remote transfer response has missing or invalid statistics")
	}
	if out != nil {
		return json.Unmarshal(response.Data, out)
	}
	return nil
}

func parseRemoteTransferError(host string, stderr []byte, err error) error {
	msg := strings.TrimSpace(string(stderr))
	if strings.Contains(msg, remoteTransferMarker) {
		if markerErr := decodeRemoteTransferMarker(stderr, nil); markerErr != nil {
			return fmt.Errorf("ssh transfer %s failed: %w: %w", host, markerErr, err)
		}
	}
	if strings.Contains(msg, "python") && (strings.Contains(msg, "not found") || strings.Contains(msg, "No such file")) {
		return fmt.Errorf("remote host %s has no python interpreter; remote_transfer needs python (install python3 or python, or add to PATH): %w", host, err)
	}
	if msg != "" {
		return fmt.Errorf("ssh transfer %s failed: %w: %s", host, err, msg)
	}
	return fmt.Errorf("ssh transfer %s failed: %w", host, err)
}

type remoteTransferFilePlan struct {
	RemoteTransferFile
	localAbs string
}

func planRemoteTransferFiles(workspace, action string, files []RemoteTransferFile) ([]remoteTransferFilePlan, error) {
	if len(files) == 0 || len(files) > toolshared.RemoteTransferFileLimit {
		return nil, codedToolError("E_BAD_ARGS", fmt.Errorf("files must contain 1 to %d file mappings", toolshared.RemoteTransferFileLimit))
	}
	plan := make([]remoteTransferFilePlan, 0, len(files))
	seen := make(map[string]bool, len(files))
	for _, file := range files {
		if strings.TrimSpace(file.LocalPath) == "" || strings.TrimSpace(file.RemotePath) == "" {
			return nil, codedToolError("E_BAD_ARGS", errors.New("each file requires localPath and remotePath"))
		}
		remotePath, err := validateRemotePath(file.RemotePath, false)
		if err != nil {
			return nil, codedToolError("E_BAD_ARGS", err)
		}
		localAbs, err := pathutil.SafeJoin(pathRuntime, []string{workspace}, file.LocalPath)
		if err != nil {
			return nil, codedToolError("E_PATH_OUTSIDE", err)
		}
		rel, err := filepath.Rel(workspace, localAbs)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, codedToolError("E_PATH_OUTSIDE", errors.New("localPath must name a file inside the workspace"))
		}
		_, localAbs, err = resolveLocalTransferPath(workspace, localAbs)
		if err != nil {
			return nil, err
		}
		key := remotePath
		if action == "download" {
			key = pathutil.CanonicalPath(localAbs)
			if pathutil.IsWindows {
				key = strings.ToLower(key)
			}
		}
		if seen[key] {
			return nil, codedToolError("E_BAD_ARGS", errors.New("files must not contain duplicate destinations"))
		}
		seen[key] = true
		plan = append(plan, remoteTransferFilePlan{RemoteTransferFile: RemoteTransferFile{LocalPath: filepath.ToSlash(rel), RemotePath: remotePath}, localAbs: localAbs})
	}
	return plan, nil
}

func (a *App) executeRemoteTransfer(ctx context.Context, req RemoteTransferRequest) (RemoteTransferResult, error) {
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	if req.Action != "upload" && req.Action != "download" {
		return RemoteTransferResult{}, codedToolError("E_BAD_ARGS", errors.New("action must be 'upload' or 'download'"))
	}
	if strings.TrimSpace(req.Alias) == "" {
		return RemoteTransferResult{}, codedToolError("E_BAD_ARGS", errors.New("alias is required"))
	}
	rt, err := a.resolveAndAuthorizeRemoteTarget(ctx, req.Alias)
	if err != nil {
		return RemoteTransferResult{}, err
	}
	workspace := toolWorkspace(ctx)
	if workspace == "" {
		a.mu.Lock()
		workspace = a.sessionWorkspaces[toolSessionID(ctx)]
		if workspace == "" {
			workspace = a.config.Workspace
		}
		a.mu.Unlock()
	}
	if workspace == "" {
		return RemoteTransferResult{}, errors.New("no workspace configured for local transfer")
	}
	plan, err := planRemoteTransferFiles(workspace, req.Action, req.Files)
	if err != nil {
		return RemoteTransferResult{}, err
	}
	result := runRemoteTransferBatch(ctx, req, plan, func(file remoteTransferFilePlan) (int64, error) {
		return a.runRemoteTransferStream(ctx, rt, req.Action, workspace, file.localAbs, file.RemotePath, req.Overwrite)
	})
	return result, nil
}

// Stop on the first error, retaining completed files and marking the rest skipped.
func runRemoteTransferBatch(ctx context.Context, req RemoteTransferRequest, plan []remoteTransferFilePlan, transfer func(remoteTransferFilePlan) (int64, error)) RemoteTransferResult {
	result := RemoteTransferResult{Action: req.Action, Alias: req.Alias, Files: make([]RemoteTransferFileResult, len(plan)), SkippedCount: len(plan)}
	for i, file := range plan {
		result.Files[i] = RemoteTransferFileResult{RemoteTransferFile: file.RemoteTransferFile, Skipped: true}
	}
	for i, file := range plan {
		item := &result.Files[i]
		if err := ctx.Err(); err != nil {
			item.Error = fmt.Sprintf("not transferred: %v", err)
			item.ErrorCode = "E_CANCELLED"
			break
		}
		item.Skipped = false
		result.SkippedCount--
		var err error
		item.TotalBytes, err = transfer(file)
		if err != nil {
			item.Error = err.Error()
			item.ErrorCode = toolErrorCode(err)
			if ctx.Err() != nil {
				item.ErrorCode = "E_CANCELLED"
			}
			result.FailedCount++
			break
		}
		item.OK = true
		result.FileCount++
		result.TotalBytes += item.TotalBytes
	}
	result.Summary = fmt.Sprintf("%d files transferred, %d failed, %d not transferred (%d bytes)", result.FileCount, result.FailedCount, result.SkippedCount, result.TotalBytes)
	return result
}

// Resolve parents before opening a bounded workspace handle. Keep the leaf
// name so atomic download replacement never follows a destination symlink.
func resolveLocalTransferPath(workspace, localAbs string) (string, string, error) {
	root, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", "", err
	}
	parent, err := evalExistingPrefix(filepath.Dir(localAbs))
	if err != nil {
		return "", "", err
	}
	if !pathutil.InsideRoot(root, parent) {
		return "", "", codedToolError("E_PATH_OUTSIDE", errors.New("local transfer parent resolves outside workspace"))
	}
	parentRel, err := filepath.Rel(root, parent)
	if err != nil {
		return "", "", err
	}
	return workspace, filepath.Join(workspace, parentRel, filepath.Base(localAbs)), nil
}

var remoteTransferCompressedScriptOnce struct {
	sync.Once
	encoded string
}

func getRemoteTransferCompressedScript() string {
	remoteTransferCompressedScriptOnce.Do(func() {
		var buf bytes.Buffer
		zw := zlib.NewWriter(&buf)
		_, _ = zw.Write([]byte(remoteTransferPythonScript))
		_ = zw.Close()
		remoteTransferCompressedScriptOnce.encoded = base64.StdEncoding.EncodeToString(buf.Bytes())
	})
	return remoteTransferCompressedScriptOnce.encoded
}

type remoteTransferStream func(map[string]any, io.Reader, io.Writer) (remoteTransferStats, error)

func (a *App) runRemoteTransferStream(ctx context.Context, rt remoteTarget, action, workspace, localAbs, remotePath string, overwrite bool) (int64, error) {
	entry, _ := a.sshCredentials.lookup(sshCredentialKey(rt.Host, rt.Port))
	if rt.credential != nil {
		entry = *rt.credential
	}
	sshConfig := a.remoteSSHConfig(rt, entry)
	poolKey, poolVersion := a.remotePoolIdentity(rt, sshConfig)
	return refreshHostKeyAndRetry(func() (int64, error) {
		return transferRemoteFile(ctx, action, workspace, localAbs, remotePath, overwrite, func(payload map[string]any, stdin io.Reader, stdout io.Writer) (remoteTransferStats, error) {
			raw, err := json.Marshal(payload)
			if err != nil {
				return remoteTransferStats{}, err
			}
			b64Payload := base64.RawURLEncoding.EncodeToString(raw)
			remoteCmd := fmt.Sprintf(`sh -c 'boot="import base64, sys, zlib; code = zlib.decompress(base64.b64decode(sys.argv.pop(1))); exec(compile(code, \"<ally-transfer>\", \"exec\"))"; command -v python3 >/dev/null 2>&1 && exec python3 -u -c "$boot" "$@" ; command -v python2 >/dev/null 2>&1 && exec python2 -u -c "$boot" "$@" ; exec python -u -c "$boot" "$@"' -- %s %s`, getRemoteTransferCompressedScript(), b64Payload)
			stderr, runErr := a.sshPool.RunStream(ctx, poolKey, poolVersion, sshConfig, remoteCmd, stdin, stdout)
			if runErr != nil {
				return remoteTransferStats{}, parseRemoteTransferError(rt.Host, stderr, runErr)
			}
			var stats remoteTransferStats
			err = decodeRemoteTransferMarker(stderr, &stats)
			return stats, err
		})
	})
}

func transferRemoteFile(ctx context.Context, action, workspace, localAbs, remotePath string, overwrite bool, run remoteTransferStream) (int64, error) {
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	localRel, err := filepath.Rel(workspace, localAbs)
	if err != nil || localRel == "." || localRel == ".." || strings.HasPrefix(localRel, ".."+string(filepath.Separator)) {
		return 0, codedToolError("E_PATH_OUTSIDE", errors.New("localPath must name a file inside the workspace"))
	}
	payload := map[string]any{"action": action, "remotePath": remotePath, "overwrite": overwrite}
	if blocked, reason := blockedSensitiveRead(localAbs); blocked {
		return 0, codedToolError("E_PROTECTED_PATH", fmt.Errorf("%s rejected: %s", action, reason))
	}
	if action == "upload" {
		info, err := root.Lstat(localRel)
		if err != nil {
			return 0, toolshared.PathError(localAbs, err)
		}
		if !info.Mode().IsRegular() {
			return 0, codedToolError("E_BAD_ARGS", errors.New("upload source must be a regular file; directories and symlinks are not supported"))
		}
		file, err := root.Open(localRel)
		if err != nil {
			return 0, toolshared.PathError(localAbs, err)
		}
		defer file.Close()
		opened, err := file.Stat()
		if err != nil {
			return 0, err
		}
		if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
			return 0, errors.New("local source changed before transfer")
		}
		payload["totalBytes"], payload["mode"], payload["modTime"] = opened.Size(), uint32(opened.Mode().Perm()&^0o022), opened.ModTime().Unix()
		stats, err := run(payload, io.NewSectionReader(file, 0, opened.Size()), nil)
		if err != nil {
			return 0, err
		}
		if stats.TotalBytes != opened.Size() {
			return 0, errors.New("remote transfer byte count does not match the source; inspect the destination before retrying")
		}
		return stats.TotalBytes, nil
	}
	if info, err := root.Lstat(localRel); err == nil {
		if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return 0, codedToolError("E_BAD_ARGS", errors.New("download destination must be a file path"))
		}
		if !overwrite {
			return 0, codedToolError("E_EXISTS", errors.New("local destination already exists; set overwrite=true to replace it"))
		}
	} else if !os.IsNotExist(err) {
		return 0, toolshared.PathError(localAbs, err)
	}
	if err := root.MkdirAll(filepath.Dir(localRel), 0o755); err != nil {
		return 0, err
	}
	tmpPath := filepath.Join(filepath.Dir(localRel), fmt.Sprintf(".ally-tmp-%d-%d", os.Getpid(), time.Now().UnixNano()))
	file, err := root.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	defer root.Remove(tmpPath)
	defer file.Close()
	stats, err := run(payload, nil, file)
	if err != nil {
		return 0, err
	}
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if info.Size() != stats.TotalBytes {
		return 0, errors.New("remote transfer byte count does not match the downloaded file")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := file.Sync(); err != nil {
		return 0, err
	}
	if err := file.Close(); err != nil {
		return 0, err
	}
	mode := os.FileMode(stats.Mode) & 0o777 &^ 0o022
	if mode == 0 {
		mode = 0o644
	}
	if err := root.Chmod(tmpPath, mode); err != nil {
		return 0, err
	}
	if stats.ModTime > 0 {
		mtime := time.Unix(stats.ModTime, 0)
		if err := root.Chtimes(tmpPath, mtime, mtime); err != nil {
			return 0, err
		}
	}
	if overwrite {
		err = root.Rename(tmpPath, localRel)
	} else {
		err = root.Link(tmpPath, localRel)
	}
	if err != nil {
		if os.IsExist(err) {
			return 0, codedToolError("E_EXISTS", errors.New("local destination already exists; set overwrite=true to replace it"))
		}
		return 0, fmt.Errorf("publish downloaded file: %w", toolshared.PathError(localAbs, err))
	}
	return stats.TotalBytes, nil
}
