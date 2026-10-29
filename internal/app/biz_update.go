// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"archive/zip"
	"context"
	"debug/macho"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"time"
)

// Ally self-update module.
//
// Windows:
//   - Download the platform-matched ZIP through Ally's proxy-aware HTTP client.
//   - Extract to ~/.ally_agent/updates/<tag>/staged/.
//   - Stop all runs, background services, and MCP servers.
//   - Rename Ally.exe → Ally.exe.bak, copy the new EXE in, then replace
//     supporting resource files via temp-file + rename.
//   - On any failure, roll back by restoring Ally.exe.bak.
//   - On next startup, clean up any leftover Ally.exe.bak.
//
// macOS (unsigned DMG distribution):
//   - Download the universal DMG through Ally's proxy-aware HTTP client.
//   - Validate the DMG, mount it, and validate the contained Ally.app.
//   - Rename the current bundle (e.g. /Applications/Ally.app) to Ally.app.bak,
//     copy the new bundle in place with ditto, then detach.
//   - On failure, restore the backup. On success, the new bundle is relaunched
//     automatically after the old process quits.
//   - Ally-downloaded DMGs carry no com.apple.quarantine attribute, so the
//     replaced bundle launches without the unsigned-app workaround.
//
// Other platforms: the platform check returns unsupported and the frontend
// keeps the existing "open browser" behavior.

const (
	allyReleaseDownloads = "https://github.com/Bronya0/ally-agent/releases/download"
	// allyReleasesAtom is the public Atom feed for releases. It does not go
	// through the GitHub REST API and is therefore NOT subject to the 60/hour
	// anonymous rate limit. Used for the periodic "is there a new version?"
	// poll; the body is small and parsed with encoding/xml.
	allyReleasesAtom      = "https://github.com/Bronya0/ally-agent/releases.atom"
	updateSubDir          = "updates"
	updateStagedDirName   = "staged"
	updateZipFileName     = "download.zip"
	updateDMGFileName     = "download.dmg"
	exeBackupSuffix       = ".bak"
	appBackupSuffix       = ".bak"
	updateDownloadTimeout = 30 * time.Minute
	updateHTTPTimeout     = 60 * time.Second
	maxUpdateArchiveBytes = 512 << 20
	maxUpdateZipEntries   = 4096
	maxUpdateExtractBytes = 1 << 30
)

var updateTagPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)

func validateUpdateTag(tag string) error {
	if !updateTagPattern.MatchString(strings.TrimSpace(tag)) {
		return fmt.Errorf("invalid release tag %q", tag)
	}
	return nil
}

// UpdateAssetInfo describes a single release asset matched for the current platform.
type UpdateAssetInfo struct {
	OK    bool   `json:"ok"`
	Name  string `json:"name,omitempty"`
	Size  int64  `json:"size,omitempty"`
	URL   string `json:"url,omitempty"`
	Tag   string `json:"tag,omitempty"`
	Error string `json:"error,omitempty"`
}

// UpdateDownloadResult is returned by DownloadUpdate.
type UpdateDownloadResult struct {
	OK        bool   `json:"ok"`
	Version   string `json:"version,omitempty"`
	StagedDir string `json:"stagedDir,omitempty"`
	Error     string `json:"error,omitempty"`
}

// UpdateApplyResult is returned by ApplyUpdate.
type UpdateApplyResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// SkipUpdateResult is returned by SkipUpdate.
type SkipUpdateResult struct {
	OK      bool   `json:"ok"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Update support follows the replacement mechanisms available on each OS.
func updatePlatformSupported() bool {
	return (goruntime.GOOS == "windows" || goruntime.GOOS == "darwin") && (goruntime.GOARCH == "amd64" || goruntime.GOARCH == "arm64")
}

func expectedAssetNameForPlatform(tag, goos, arch string) string {
	cpu := arch
	if arch == "amd64" {
		cpu = "x64"
	}
	if goos == "darwin" {
		return fmt.Sprintf("Ally-%s-macos-%s.dmg", tag, cpu)
	}
	if goos == "linux" {
		return fmt.Sprintf("Ally-%s-linux-%s.tar.gz", tag, cpu)
	}
	return fmt.Sprintf("Ally-%s-windows-%s.zip", tag, cpu)
}

func expectedAssetName(tag string) string {
	arch := goruntime.GOARCH
	if goruntime.GOOS == "darwin" {
		if exe, err := os.Executable(); err == nil {
			if fat, err := macho.OpenFat(exe); err == nil {
				fat.Close()
				arch = "universal"
			}
		}
	}
	return expectedAssetNameForPlatform(tag, goruntime.GOOS, arch)
}

func updateBaseDir() string {
	return filepath.Join(appDataDir(), updateSubDir)
}

// updateReleaseCache persists the last known release tag so a transient
// network/parse failure in CheckForUpdates can still return a useful result.
// Only LastTag and LastChecked are written now; the legacy etag/lastModified
// JSON fields are tolerated on read (ignored) for backward compatibility
// with cache files written by older builds.
type updateReleaseCache struct {
	LastTag     string    `json:"lastTag,omitempty"`
	LastChecked time.Time `json:"lastChecked,omitempty"`
}

func updateCachePath() string {
	return filepath.Join(appDataDir(), "update_cache.json")
}

func loadUpdateCache() updateReleaseCache {
	var c updateReleaseCache
	data, err := os.ReadFile(updateCachePath())
	if err != nil {
		return c
	}
	_ = json.Unmarshal(data, &c)
	// 来自写坏或被手改的缓存的 tag 绝不能进文件系统：它会成为一个路径分量，
	// 而调用方会递归删除该路径。非法就直接当成“没有缓存”。
	if c.LastTag != "" {
		if err := validateUpdateTag(c.LastTag); err != nil {
			c.LastTag = ""
		}
	}
	return c
}

func saveUpdateCache(c updateReleaseCache) {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return
	}
	// 原子写：半截文件虽然读起来只是“没缓存”，但被截断的内容仍可能把野 tag
	// 交给更新界面，所以和其它状态文件一样落盘。
	_ = writeAtomicBytes(updateCachePath(), data, 0o600)
}

func updateVersionDir(tag string) string {
	return filepath.Join(updateBaseDir(), tag)
}

func updateStagedDir(tag string) string {
	return filepath.Join(updateVersionDir(tag), updateStagedDirName)
}

func updateZipPath(tag string) string {
	return filepath.Join(updateVersionDir(tag), updateZipFileName)
}

// updateAssetPath returns the downloaded update archive path for the current
// platform: ZIP on Windows, DMG on macOS.
func updateAssetPath(tag string) string {
	if goruntime.GOOS == "darwin" {
		return filepath.Join(updateVersionDir(tag), updateDMGFileName)
	}
	return updateZipPath(tag)
}

// allyExecutableDir returns the directory of the currently running Ally binary.
func allyExecutableDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

// ListUpdateAsset resolves the fixed release filename without a REST API call.
func (a *App) ListUpdateAsset(tag string) UpdateAssetInfo {
	if !updatePlatformSupported() {
		return UpdateAssetInfo{Error: "automatic update is only supported on Windows x64/ARM64 and macOS"}
	}
	tag = strings.TrimSpace(tag)
	if err := validateUpdateTag(tag); err != nil {
		return UpdateAssetInfo{Error: err.Error()}
	}
	if strings.ContainsAny(tag, "-+") {
		return UpdateAssetInfo{Error: "automatic update requires a numeric release tag"}
	}
	name := expectedAssetName(tag)
	return UpdateAssetInfo{OK: true, Name: name, URL: fmt.Sprintf("%s/%s/%s", allyReleaseDownloads, tag, name), Tag: tag}
}

// downloadAsset streams an asset URL to destPath using the proxy-aware client.
// Progress is emitted via the update:progress event on the cadence declared for
// that event in eventCadenceTable.
func (a *App) downloadAsset(ctx context.Context, assetURL, destPath, version string, totalBytes int64) error {
	cfg := updateNetworkConfig(a.effectiveConfigSafe())
	client := updateHTTPClient(cfg, updateDownloadTimeout)
	ctx, cancel := context.WithTimeout(ctx, updateDownloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ally-agent-update-download")
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download status %d", resp.StatusCode)
	}
	if totalBytes > maxUpdateArchiveBytes {
		return fmt.Errorf("update archive exceeds %d bytes", maxUpdateArchiveBytes)
	}
	if resp.ContentLength > maxUpdateArchiveBytes {
		return fmt.Errorf("update response exceeds %d bytes", maxUpdateArchiveBytes)
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(destPath), ".download-*.part")
	if err != nil {
		return err
	}
	tmpPath := f.Name()
	defer func() {
		f.Close()
		_ = os.Remove(tmpPath)
	}()

	// Determine total size for progress reporting.
	total := totalBytes
	if total <= 0 && resp.ContentLength > 0 {
		total = resp.ContentLength
	}

	// 节流参数收口在 eventCadenceTable（updateProgressEvent 档）。
	var throttle emitThrottle
	cadence := cadenceForEvent(updateProgressEvent)
	var received int64
	buf := make([]byte, 64*1024)
	flushProgress := func(force bool) {
		now := time.Now()
		if force {
			throttle.force(now)
		} else if !throttle.allow(now, cadence, 0) {
			return
		}
		percent := 0
		if total > 0 {
			percent = int(received * 100 / total)
			if percent > 100 {
				percent = 100
			}
		}
		a.emit(updateProgressEvent, map[string]any{
			"stage":           "download",
			"version":         version,
			"bytesDownloaded": received,
			"bytesTotal":      total,
			"percent":         percent,
		})
	}
	flushProgress(true)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if received > maxUpdateArchiveBytes-int64(n) {
				return fmt.Errorf("update response exceeds %d bytes", maxUpdateArchiveBytes)
			}
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			received += int64(n)
			flushProgress(false)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if total > 0 && received != total {
		return errors.New("incomplete update download")
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return err
	}
	flushProgress(true)
	return nil
}

// extractZip extracts a ZIP file to destDir, with Zip Slip protection.
func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	absDest, err := filepath.Abs(destDir)
	if err != nil {
		return err
	}
	if len(r.File) > maxUpdateZipEntries {
		return fmt.Errorf("zip contains too many entries: %d", len(r.File))
	}
	var extractedBytes uint64
	for _, f := range r.File {
		if f.UncompressedSize64 > maxUpdateExtractBytes || extractedBytes > maxUpdateExtractBytes-f.UncompressedSize64 {
			return fmt.Errorf("zip expands beyond %d bytes", maxUpdateExtractBytes)
		}
		extractedBytes += f.UncompressedSize64
		if err := extractZipEntry(f, absDest); err != nil {
			return err
		}
	}
	return nil
}

func extractZipEntry(f *zip.File, absDest string) error {
	name := filepath.FromSlash(f.Name)
	if name == "." || filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return fmt.Errorf("zip entry has an absolute path: %s", f.Name)
	}
	for _, part := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return fmt.Errorf("zip entry contains parent reference: %s", f.Name)
		}
	}
	if f.FileInfo().Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("zip entry is a symlink: %s", f.Name)
	}
	name = filepath.Clean(name)
	if name == ".." || strings.HasPrefix(name, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("zip entry escapes destination: %s", f.Name)
	}
	target := filepath.Join(absDest, name)
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(absTarget+string(os.PathSeparator), absDest+string(os.PathSeparator)) && absTarget != absDest {
		return fmt.Errorf("zip entry escapes destination: %s", f.Name)
	}
	if f.FileInfo().IsDir() {
		return os.MkdirAll(absTarget, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(absTarget), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(absTarget, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, io.LimitReader(rc, maxUpdateExtractBytes+1))
	if err != nil {
		return err
	}
	if info, err := out.Stat(); err != nil {
		return err
	} else if info.Size() > int64(maxUpdateExtractBytes) {
		return fmt.Errorf("zip entry exceeds extraction limit: %s", f.Name)
	}
	return nil
}

// macAppBundleDir returns the .app bundle directory containing the currently
// running Ally binary, or an error if the executable is not inside an .app.
func macAppBundleDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe) // .../Contents/MacOS
	bundle := filepath.Dir(dir)
	if filepath.Base(bundle) != "Contents" {
		return "", fmt.Errorf("executable is not inside an app bundle: %s", exe)
	}
	appDir := filepath.Dir(bundle)
	if filepath.Ext(appDir) != ".app" {
		return "", fmt.Errorf("executable is not inside an app bundle: %s", exe)
	}
	return appDir, nil
}

// mountDMG mounts a DMG read-only at mountPoint with hdiutil and returns the
// mount point. The caller must detach it afterwards.
func mountDMG(dmgPath, mountPoint string) (string, error) {
	if err := os.MkdirAll(mountPoint, 0o755); err != nil {
		return "", err
	}
	cmd := exec.Command("/usr/bin/hdiutil", "attach", "-nobrowse", "-readonly", "-mountpoint", mountPoint, dmgPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("hdiutil attach: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return mountPoint, nil
}

// detachDMG unmounts a previously mounted DMG.
func detachDMG(mountPoint string) error {
	cmd := exec.Command("/usr/bin/hdiutil", "detach", mountPoint)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("hdiutil detach: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// copyDir copies a directory tree with ditto, preserving permissions,
// symlinks, and extended attributes.
func copyDir(src, dst string) error {
	cmd := exec.Command("/usr/bin/ditto", src, dst)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ditto: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DownloadUpdate downloads the platform-matched archive (ZIP on Windows, DMG
// on macOS) for the given tag. Emits update:progress during download and
// update:ready (or update:error) on completion.
func (a *App) DownloadUpdate(tag string) UpdateDownloadResult {
	if !updatePlatformSupported() {
		return UpdateDownloadResult{Error: "automatic update is only supported on Windows x64/ARM64 and macOS"}
	}
	if err := a.beginUpdateOperation("downloading"); err != nil {
		return UpdateDownloadResult{Error: err.Error()}
	}
	defer a.endUpdateOperation()
	lock, err := lockUpdateDownloads()
	if err != nil {
		return UpdateDownloadResult{Error: err.Error()}
	}
	defer lock.Close()
	asset, err := a.ListUpdateAsset(tag).okOrError()
	if err != nil {
		a.emit("update:error", map[string]any{"stage": "match", "error": err.Error()})
		return UpdateDownloadResult{Error: err.Error()}
	}
	versionDir := updateVersionDir(asset.Tag)
	stagedDir := updateStagedDir(asset.Tag)
	archivePath := updateAssetPath(asset.Tag)

	// Register a cancel handle so CancelUpdate can abort this download.
	dlCtx, dlCancel := context.WithCancel(context.Background())
	a.updateMu.Lock()
	a.updateCancel = dlCancel
	cancelNow := a.updateCancelRequested
	a.updateMu.Unlock()
	// A cancel request arrived while the asset URL was still being resolved;
	// abort immediately instead of starting the download.
	if cancelNow {
		dlCancel()
	}
	defer func() {
		dlCancel()
		a.updateMu.Lock()
		a.updateCancel = nil
		a.updateMu.Unlock()
	}()

	// cancelled reports whether the user aborted the download; when so it
	// cleans up the staged version dir and emits update:cancelled.
	cancelled := func() bool {
		if dlCtx.Err() == nil {
			return false
		}
		_ = removeAllWithinBase(updateBaseDir(), versionDir)
		a.emit("update:cancelled", map[string]any{"version": asset.Tag})
		return true
	}

	// Clean any previous staged state for this version.
	_ = removeAllWithinBase(updateBaseDir(), versionDir)
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		msg := fmt.Sprintf("create version dir: %v", err)
		a.emit("update:error", map[string]any{"stage": "prepare", "error": msg})
		return UpdateDownloadResult{Error: msg}
	}

	if err := a.downloadAsset(dlCtx, asset.URL, archivePath, asset.Tag, asset.Size); err != nil {
		if cancelled() {
			return UpdateDownloadResult{Error: "cancelled"}
		}
		msg := fmt.Sprintf("download: %v", err)
		a.emit("update:error", map[string]any{"stage": "download", "error": msg})
		_ = removeAllWithinBase(updateBaseDir(), versionDir)
		return UpdateDownloadResult{Error: msg}
	}
	if cancelled() {
		return UpdateDownloadResult{Error: "cancelled"}
	}
	if err := saveUpdateReceipt(archivePath, asset.Tag, asset.Name); err != nil {
		_ = removeAllWithinBase(updateBaseDir(), versionDir)
		return UpdateDownloadResult{Error: err.Error()}
	}

	if goruntime.GOOS == "darwin" {
		// macOS: no extraction. Validate the DMG and report ready; the actual
		// mount and bundle validation happen at apply time.
		if err := validateStagedDMG(archivePath); err != nil {
			msg := fmt.Sprintf("invalid staged dmg: %v", err)
			a.emit("update:error", map[string]any{"stage": "verify", "error": msg})
			_ = removeAllWithinBase(updateBaseDir(), versionDir)
			return UpdateDownloadResult{Error: msg}
		}
		if cancelled() {
			return UpdateDownloadResult{Error: "cancelled"}
		}
		a.emit("update:ready", map[string]any{
			"version":   asset.Tag,
			"stagedDir": versionDir,
		})
		return UpdateDownloadResult{
			OK:        true,
			Version:   asset.Tag,
			StagedDir: versionDir,
		}
	}

	a.emit(updateProgressEvent, map[string]any{
		"stage":   "extract",
		"version": asset.Tag,
		"percent": 0,
	})
	if err := extractZip(archivePath, stagedDir); err != nil {
		msg := fmt.Sprintf("extract: %v", err)
		a.emit("update:error", map[string]any{"stage": "extract", "error": msg})
		_ = removeAllWithinBase(updateBaseDir(), versionDir)
		return UpdateDownloadResult{Error: msg}
	}
	a.emit(updateProgressEvent, map[string]any{
		"stage":   "extract",
		"version": asset.Tag,
		"percent": 100,
	})

	// Verify the staged Ally.exe before declaring ready.
	stagedExe := filepath.Join(stagedDir, "Ally.exe")
	if err := validateStagedExecutable(stagedExe); err != nil {
		msg := fmt.Sprintf("invalid staged executable: %v", err)
		a.emit("update:error", map[string]any{"stage": "verify", "error": msg})
		_ = removeAllWithinBase(updateBaseDir(), versionDir)
		return UpdateDownloadResult{Error: msg}
	}
	if cancelled() {
		return UpdateDownloadResult{Error: "cancelled"}
	}

	a.emit("update:ready", map[string]any{
		"version":   asset.Tag,
		"stagedDir": stagedDir,
	})
	return UpdateDownloadResult{
		OK:        true,
		Version:   asset.Tag,
		StagedDir: stagedDir,
	}
}

// CancelUpdate aborts an in-progress DownloadUpdate. It returns true when a
// download was active and got cancelled; the pending DownloadUpdate call then
// finishes with Error "cancelled" and cleans up its staged files.
func (a *App) CancelUpdate() bool {
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	if a.updateCancel != nil {
		a.updateCancel()
		return true
	}
	// No download handle registered yet (e.g. still resolving the asset URL):
	// record the request so DownloadUpdate aborts once it registers.
	if a.updateOperation == "downloading" {
		a.updateCancelRequested = true
		return true
	}
	return false
}

// okOrError is a small helper to convert UpdateAssetInfo into an error when not OK.
func (u UpdateAssetInfo) okOrError() (UpdateAssetInfo, error) {
	if !u.OK {
		return u, errors.New(u.Error)
	}
	return u, nil
}

// isDirWritable reports whether a directory is writable by creating and
// removing a temporary file.
func isDirWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".ally-write-test-*")
	if err != nil {
		return err
	}
	tmp.Close()
	return os.Remove(tmp.Name())
}

// stopAllRuns cancels every active run and waits for them to exit. It returns
// an error if some runs are still alive after the timeout, so the caller can
// abort the update instead of replacing an EXE while a run is still unwinding.
func (a *App) stopAllRuns() error {
	a.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(a.runs))
	for _, cancel := range a.runs {
		cancels = append(cancels, cancel)
	}
	a.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	// Wait up to 10 seconds for runs to unwind. Spec requires "all chats,
	// background services and commands are stopped" before renaming Ally.exe.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		remaining := len(a.runs)
		a.mu.Unlock()
		if remaining == 0 {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	a.mu.Lock()
	remaining := len(a.runs)
	a.mu.Unlock()
	return fmt.Errorf("timed out waiting for %d running task(s) to stop", remaining)
}

// stagedFileSet returns the list of files inside stagedDir that should be
// copied into the install directory.
func stagedFileSet(stagedDir string) ([]string, error) {
	var files []string
	err := filepath.Walk(stagedDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("staged resource is not regular: %s", path)
		}
		rel, err := filepath.Rel(stagedDir, path)
		if err != nil {
			return err
		}
		if err := updateLocalPath(rel); err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})
	return files, err
}

// cleanAppliedUpdateDirs removes stale staged update directories under
// ~/.ally_agent/updates/ after a successful apply, keeping only the directory
// of the just-applied tag. Only directory names that pass validateUpdateTag
// are ever removed; the tag regex forbids path separators, so a corrupted or
// malicious entry can never cause a deletion outside the updates root.
func cleanAppliedUpdateDirs(rootDir, keepTag string) {
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		tag := entry.Name()
		if tag == keepTag {
			continue
		}
		if err := validateUpdateTag(tag); err != nil {
			continue // not a release-tag directory; leave it untouched
		}
		_ = removeAllWithinBase(rootDir, filepath.Join(rootDir, tag))
	}
}

// ApplyUpdate stops all runs and services, then replaces the current
// installation from the staged archive (Windows EXE/replace flow or macOS
// bundle replace flow). On any failure it rolls back so the previous
// installation keeps running.
func (a *App) ApplyUpdate(tag string) UpdateApplyResult {
	// 校验与使用必须看到同一个值：validateUpdateTag 内部会 trim，这里若保留原值，
	// " v1.2.3 " 能通过校验、随后按带空格的路径去 stat 暂存目录。
	tag = strings.TrimSpace(tag)
	if !updatePlatformSupported() {
		return UpdateApplyResult{Error: "automatic update is only supported on Windows x64/ARM64 and macOS"}
	}
	if err := validateUpdateTag(tag); err != nil {
		return UpdateApplyResult{Error: err.Error()}
	}
	if err := a.beginUpdateOperation("applying"); err != nil {
		return UpdateApplyResult{Error: err.Error()}
	}
	defer a.endUpdateOperation()
	a.updateRunGate.Lock()
	a.updateRunGate.Unlock()
	lock, err := lockUpdateDownloads()
	if err != nil {
		return UpdateApplyResult{Error: err.Error()}
	}
	defer lock.Close()
	if err := verifyStagedUpdate(tag); err != nil {
		return UpdateApplyResult{Error: err.Error()}
	}
	if goruntime.GOOS == "windows" {
		if err := removeAllWithinBase(updateBaseDir(), updateStagedDir(tag)); err != nil {
			return UpdateApplyResult{Error: err.Error()}
		}
		if err := extractZip(updateAssetPath(tag), updateStagedDir(tag)); err != nil {
			return UpdateApplyResult{Error: err.Error()}
		}
	}
	var result UpdateApplyResult
	if goruntime.GOOS == "darwin" {
		result = a.applyMacUpdate(tag)
	} else {
		result = a.applyWindowsUpdate(tag)
	}
	if result.OK {
		a.updateMu.Lock()
		a.updateOperation = "applied"
		a.updateMu.Unlock()
	}
	return result
}

// applyWindowsUpdate backs up resources before changing the live executable.
// Backups survive until the next frontend has initialized successfully.
func (a *App) applyWindowsUpdate(tag string) UpdateApplyResult {
	stagedDir := updateStagedDir(tag)
	if err := validateStagedExecutable(filepath.Join(stagedDir, "Ally.exe")); err != nil {
		return UpdateApplyResult{Error: err.Error()}
	}
	exeDir, err := allyExecutableDir()
	if err != nil {
		return UpdateApplyResult{Error: err.Error()}
	}
	files, err := stagedFileSet(stagedDir)
	if err != nil {
		return UpdateApplyResult{Error: err.Error()}
	}
	live, err := os.OpenRoot(exeDir)
	if err != nil {
		return UpdateApplyResult{Error: err.Error()}
	}
	defer live.Close()
	for _, name := range files {
		if err := updateNoLinks(live, name, true); err != nil {
			return UpdateApplyResult{Error: err.Error()}
		}
	}
	if err := a.stopAllRuns(); err != nil {
		return UpdateApplyResult{Error: err.Error()}
	}
	a.stopAllServices()
	backupDir := filepath.Join(exeDir, ".ally-update-backup")
	if err := removeAllWithinBase(exeDir, backupDir); err != nil {
		return UpdateApplyResult{Error: err.Error()}
	}
	if err := os.Mkdir(backupDir, 0o700); err != nil {
		return UpdateApplyResult{Error: err.Error()}
	}
	for _, name := range files {
		if name == "Ally.exe" {
			continue
		}
		if _, err := live.Lstat(name); err == nil {
			if err := copyUpdatePath(exeDir, backupDir, name); err != nil {
				return UpdateApplyResult{Error: fmt.Sprintf("backup resource %s: %v", name, err)}
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return UpdateApplyResult{Error: err.Error()}
		}
	}
	currentExe := filepath.Join(exeDir, "Ally.exe")
	backupExe := currentExe + exeBackupSuffix
	if err := live.Remove("Ally.exe" + exeBackupSuffix); err != nil && !errors.Is(err, os.ErrNotExist) {
		return UpdateApplyResult{Error: err.Error()}
	}
	if err := live.Rename("Ally.exe", "Ally.exe"+exeBackupSuffix); err != nil {
		return UpdateApplyResult{Error: err.Error()}
	}
	replaced := make([]string, 0, len(files))
	failed := func(err error) UpdateApplyResult {
		message := err.Error()
		if rollback := rollbackReplacedResources(backupDir, exeDir, replaced, backupExe, currentExe); rollback != "" {
			message += "; " + rollback + "; backups retained in " + backupDir
		}
		a.emit("update:error", map[string]any{"stage": "apply", "error": message})
		return UpdateApplyResult{Error: message}
	}
	if err := copyUpdatePath(stagedDir, exeDir, "Ally.exe"); err != nil {
		return failed(err)
	}
	for _, name := range files {
		if name == "Ally.exe" {
			continue
		}
		if err := copyUpdatePath(stagedDir, exeDir, name); err != nil {
			return failed(fmt.Errorf("replace resource %s: %w", name, err))
		}
		replaced = append(replaced, name)
	}
	cleanAppliedUpdateDirs(updateBaseDir(), tag)
	a.emit("update:applied", map[string]any{"version": tag})
	return UpdateApplyResult{OK: true}
}

func rollbackReplacedResources(backupDir, exeDir string, replaced []string, backupExe, currentExe string) string {
	var failures []string
	live, err := os.OpenRoot(exeDir)
	if err != nil {
		return err.Error()
	}
	defer live.Close()
	backup, err := os.OpenRoot(backupDir)
	if err != nil {
		return err.Error()
	}
	defer backup.Close()
	for i := len(replaced) - 1; i >= 0; i-- {
		name := replaced[i]
		if err := updateNoLinks(live, name, true); err != nil {
			failures = append(failures, err.Error())
			continue
		}
		if _, err := backup.Lstat(name); err == nil {
			if err := copyUpdateFile(backup, live, name, name); err != nil {
				failures = append(failures, err.Error())
			}
		} else if errors.Is(err, os.ErrNotExist) {
			if err := live.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
				failures = append(failures, err.Error())
			}
		} else {
			failures = append(failures, err.Error())
		}
	}
	if filepath.Dir(backupExe) != exeDir || filepath.Dir(currentExe) != exeDir {
		failures = append(failures, "executable rollback path is outside installation")
	} else if err := live.Rename(filepath.Base(backupExe), filepath.Base(currentExe)); err != nil {
		failures = append(failures, err.Error())
	}
	return strings.Join(failures, "; ")
}

// applyMacUpdate replaces the running .app bundle with the app inside the
// staged DMG. It requires the current bundle to live in a writable directory
// (/Applications for admin users, or ~/Applications). The old bundle is
// renamed to a .bak sibling and restored on failure.
func (a *App) applyMacUpdate(tag string) UpdateApplyResult {
	archivePath := updateAssetPath(tag)
	if _, err := os.Stat(archivePath); err != nil {
		return UpdateApplyResult{Error: fmt.Sprintf("staged dmg not found: %v", err)}
	}
	if err := validateStagedDMG(archivePath); err != nil {
		return UpdateApplyResult{Error: fmt.Sprintf("invalid staged dmg: %v", err)}
	}
	appDir, err := macAppBundleDir()
	if err != nil {
		return UpdateApplyResult{Error: fmt.Sprintf("locate app bundle: %v", err)}
	}
	if err := isDirWritable(filepath.Dir(appDir)); err != nil {
		return UpdateApplyResult{Error: fmt.Sprintf("install dir not writable: %v", err)}
	}

	a.emit(updateProgressEvent, map[string]any{"stage": "apply", "version": tag, "percent": 0})

	// Stop everything that could interfere. The bundle can be renamed while
	// the old process is still running (macOS does not lock the executable),
	// but runs and services are stopped for a clean exit.
	if err := a.stopAllRuns(); err != nil {
		a.emit("update:error", map[string]any{"stage": "apply", "error": err.Error()})
		return UpdateApplyResult{Error: err.Error()}
	}
	a.stopAllServices()

	mountPoint := filepath.Join(updateVersionDir(tag), "mnt")
	mounted, err := mountDMG(archivePath, mountPoint)
	if err != nil {
		msg := fmt.Sprintf("mount dmg: %v", err)
		a.emit("update:error", map[string]any{"stage": "apply", "error": msg})
		return UpdateApplyResult{Error: msg}
	}
	defer func() { _ = detachDMG(mounted) }()

	stagedApp := filepath.Join(mounted, "Ally.app")
	if err := validateMacUpdateBundle(stagedApp, tag); err != nil {
		msg := fmt.Sprintf("invalid staged app: %v", err)
		a.emit("update:error", map[string]any{"stage": "apply", "error": msg})
		return UpdateApplyResult{Error: msg}
	}

	backupApp := appDir + appBackupSuffix
	appParent := filepath.Dir(appDir)
	if err := removeAllWithinBase(appParent, backupApp); err != nil {
		return UpdateApplyResult{Error: err.Error()}
	}
	// Prepare beside the live bundle. Copy failures leave the current app intact.
	if err := copyDir(stagedApp, backupApp); err != nil {
		_ = removeAllWithinBase(appParent, backupApp)
		return UpdateApplyResult{Error: err.Error()}
	}
	if err := validateMacUpdateBundle(backupApp, tag); err != nil {
		_ = removeAllWithinBase(appParent, backupApp)
		return UpdateApplyResult{Error: err.Error()}
	}
	// The old bundle becomes .bak in one atomic exchange; there is no interval
	// with a missing or partially copied live application.
	if err := exchangeUpdateBundles(appDir, backupApp); err != nil {
		_ = removeAllWithinBase(appParent, backupApp)
		return UpdateApplyResult{Error: err.Error()}
	}

	cleanAppliedUpdateDirs(updateBaseDir(), tag)
	a.emit(updateProgressEvent, map[string]any{"stage": "apply", "version": tag, "percent": 100})
	a.emit("update:applied", map[string]any{"version": tag})
	return UpdateApplyResult{OK: true}
}

// QuitForUpdate asks the current Ally process to quit. On Windows the user
// relaunches manually; on macOS the new bundle is opened automatically after
// the old process exits. Spawning a replacement from the old process on
// Windows is intentionally avoided: if the new EXE fails to start (PE
// corruption, missing dependency, signature issue), the user is left with no
// running Ally at all. On macOS the new bundle was already validated before
// replacement, and the .bak sibling remains until the new process starts.
func (a *App) QuitForUpdate() error {
	if !updatePlatformSupported() {
		return errors.New("automatic update is only supported on Windows x64/ARM64 and macOS")
	}
	if a.ctx == nil {
		return errors.New("app context not initialized")
	}
	a.updateMu.Lock()
	applied := a.updateOperation == "applied"
	a.updateMu.Unlock()
	if !applied {
		return errors.New("no update has been applied")
	}
	if goruntime.GOOS == "darwin" {
		appDir, err := macAppBundleDir()
		if err != nil {
			return err
		}
		if err := startUpdateRelaunchHelper(appDir); err != nil {
			return fmt.Errorf("start update relaunch helper: %w", err)
		}
	}
	go func() {
		// Small delay so the frontend can render the "closing" state before
		// the window disappears.
		time.Sleep(500 * time.Millisecond)
		a.quitApp()
	}()
	return nil
}

const macUpdateBackupCleanupDelay = 10 * time.Second

// scheduleUpdateBackupCleanup retains the old macOS bundle until the new
// process has survived its startup path. If startup crashes, the .bak bundle
// remains available for manual recovery instead of being deleted immediately.
func scheduleUpdateBackupCleanup(ctx context.Context) {
	if !updatePlatformSupported() {
		return
	}
	if goruntime.GOOS != "darwin" {
		cleanupUpdateBackup()
		return
	}
	appDir, err := macAppBundleDir()
	if err != nil {
		return
	}
	backupPath := appDir + appBackupSuffix
	original, err := os.Stat(backupPath)
	if err != nil {
		return
	}
	go func() {
		timer := time.NewTimer(macUpdateBackupCleanupDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			cleanupUpdateBackupIfUnchanged(appDir, backupPath, original)
		}
	}()
}

func cleanupUpdateBackupIfUnchanged(appDir, path string, original os.FileInfo) {
	info, err := os.Stat(path)
	if err != nil || !os.SameFile(original, info) {
		return
	}
	_ = removeAllWithinBase(filepath.Dir(appDir), path)
}

// cleanupUpdateBackup removes leftover backups from a previous self-update
// once the new process has settled.
func cleanupUpdateBackup() {
	if !updatePlatformSupported() {
		return
	}
	if goruntime.GOOS == "darwin" {
		appDir, err := macAppBundleDir()
		if err != nil {
			return
		}
		_ = removeAllWithinBase(filepath.Dir(appDir), appDir+appBackupSuffix)
		return
	}
	exeDir, err := allyExecutableDir()
	if err != nil {
		return
	}
	backup := filepath.Join(exeDir, "Ally.exe"+exeBackupSuffix)
	if err := os.Remove(backup); err == nil || errors.Is(err, os.ErrNotExist) {
		_ = removeAllWithinBase(exeDir, filepath.Join(exeDir, ".ally-update-backup"))
	}
}

// findStagedUpdate scans ~/.ally_agent/updates/<tag>/staged/ for any directory
// whose staged Ally.exe still exists, and returns the most recently modified
// tag. Returns empty string if none found.
func (a *App) findStagedUpdate() string {
	rootDir := updateBaseDir()
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return ""
	}
	var bestTag string
	var bestMod time.Time
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		tag := entry.Name()
		var probe string
		if goruntime.GOOS == "darwin" {
			probe = updateAssetPath(tag)
		} else {
			probe = filepath.Join(updateStagedDir(tag), "Ally.exe")
		}
		info, err := os.Stat(probe)
		if err != nil {
			continue
		}
		if bestTag == "" || info.ModTime().After(bestMod) {
			bestTag = tag
			bestMod = info.ModTime()
		}
	}
	return bestTag
}

// SkipUpdate marks a release tag as skipped. Future automatic download checks
// will ignore this tag until the user clears the skip list. Any staged files
// for the tag are removed so disk space is not held by a skipped version.
func (a *App) SkipUpdate(version string) SkipUpdateResult {
	version = strings.TrimSpace(version)
	if version == "" {
		return SkipUpdateResult{Error: "version is required"}
	}
	// The tag becomes a path component below (updateVersionDir) and that
	// directory is removed recursively, so it must be validated exactly like
	// every other tag that reaches the filesystem: filepath.Join cleans `..`
	// away, and "../../.." would delete well outside ~/.ally_agent/updates.
	if err := validateUpdateTag(version); err != nil {
		return SkipUpdateResult{Error: err.Error()}
	}
	if err := a.beginUpdateOperation("skipping"); err != nil {
		return SkipUpdateResult{Error: err.Error()}
	}
	defer a.endUpdateOperation()
	lock, err := lockUpdateDownloads()
	if err != nil {
		return SkipUpdateResult{Error: err.Error()}
	}
	defer lock.Close()
	if err := a.updateConfigAndPersist(func(cfg *ConfigState) {
		for _, skipped := range cfg.SkippedUpdates {
			if skipped == version {
				return
			}
		}
		cfg.SkippedUpdates = append(cfg.SkippedUpdates, version)
	}); err != nil {
		return SkipUpdateResult{Error: fmt.Sprintf("persist skip list: %v", err)}
	}

	// Remove staged files for the skipped version so disk space is released.
	_ = removeAllWithinBase(updateBaseDir(), updateVersionDir(version))
	return SkipUpdateResult{OK: true, Version: version}
}

// ── Release check ────────────────────────────────────────────

// CheckForUpdatesResult describes the outcome of a latest-release lookup.
type CheckForUpdatesResult struct {
	OK                bool   `json:"ok"`
	Tag               string `json:"tag,omitempty"`
	Error             string `json:"error,omitempty"`
	CanAutoUpdate     bool   `json:"canAutoUpdate,omitempty"`
	AutoUpdateEnabled bool   `json:"autoUpdateEnabled,omitempty"`
	Skipped           bool   `json:"skipped,omitempty"`
	StagedVersion     string `json:"stagedVersion,omitempty"`
}

// allyReleaseAtomTagRe extracts the version tag from a release URL such as
// "https://github.com/Bronya0/ally-agent/releases/tag/v1.6.0". Used as a
// fallback when the <id> element does not end with the tag.
var allyReleaseAtomTagRe = regexp.MustCompile(`/releases/tag/([^/?#]+)`)

// atomFeed is a minimal subset of the GitHub releases Atom feed. Only the
// fields used for version detection are decoded; <content> is skipped.
type atomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	ID    string     `xml:"id"`
	Title string     `xml:"title"`
	Links []atomLink `xml:"link"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
}

// parseAtomLatestTag extracts the version tag of the first (newest) entry in
// the Atom feed. It prefers the /releases/tag/<tag> segment from the
// alternate link and falls back to the trailing path segment of <id>.
func parseAtomLatestTag(body []byte) (string, error) {
	var feed atomFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return "", fmt.Errorf("parse atom feed: %w", err)
	}
	if len(feed.Entries) == 0 {
		return "", errors.New("atom feed has no entries")
	}
	entry := feed.Entries[0]
	for _, l := range entry.Links {
		if l.Rel == "alternate" && l.Href != "" {
			if m := allyReleaseAtomTagRe.FindStringSubmatch(l.Href); len(m) == 2 {
				return strings.TrimSpace(m[1]), nil
			}
		}
	}
	// Fallback: <id>tag:github.com,2008:Repository/<repo>/v1.6.0</id>
	if id := strings.TrimSpace(entry.ID); id != "" {
		if idx := strings.LastIndex(id, "/"); idx >= 0 && idx < len(id)-1 {
			return id[idx+1:], nil
		}
	}
	return "", errors.New("could not locate release tag in atom feed")
}

// CheckForUpdates fetches the public GitHub releases Atom feed (no API token,
// no rate limit) and reports the newest release tag. It uses a one-minute
// timeout and never panics; the caller (frontend) treats any non-OK result as
// "no update detected".
//
// On a network/parse failure, the last known tag persisted in
// ~/.ally_agent/update_cache.json is returned so the UI still works.
//
// The result also reports whether automatic background download is enabled,
// whether the latest tag was previously skipped, and the version currently
// staged on disk (if any) so the frontend can decide whether to silently
// download, prompt the user, or do nothing.
func (a *App) CheckForUpdates() CheckForUpdatesResult {
	cfg := updateNetworkConfig(a.effectiveConfigSafe())
	client := updateHTTPClient(cfg, updateHTTPTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, allyReleasesAtom, nil)
	if err != nil {
		return a.fallbackUpdateResult(cfg, err.Error())
	}
	req.Header.Set("Accept", "application/atom+xml,application/xml,text/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("User-Agent", "ally-agent-update-check")
	resp, err := client.Do(req)
	if err != nil {
		return a.fallbackUpdateResult(cfg, err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return a.fallbackUpdateResult(cfg, fmt.Sprintf("atom feed status %d", resp.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return a.fallbackUpdateResult(cfg, err.Error())
	}
	tag, err := parseAtomLatestTag(body)
	if err != nil {
		return a.fallbackUpdateResult(cfg, err.Error())
	}
	if err := validateUpdateTag(tag); err != nil {
		return a.fallbackUpdateResult(cfg, err.Error())
	}
	cache := loadUpdateCache()
	cache.LastTag = tag
	cache.LastChecked = time.Now()
	saveUpdateCache(cache)
	return a.buildUpdateResult(cfg, tag)
}

// fallbackUpdateResult returns the cached latest tag when available, so the
// UI keeps working during a transient network/parse failure. If no cache is
// available, it returns the original error.
func (a *App) fallbackUpdateResult(cfg ConfigState, errMsg string) CheckForUpdatesResult {
	cache := loadUpdateCache()
	if tag := strings.TrimSpace(cache.LastTag); tag != "" {
		return a.buildUpdateResult(cfg, tag)
	}
	return CheckForUpdatesResult{Error: errMsg}
}

// buildUpdateResult assembles a CheckForUpdatesResult from a known tag,
// resolving skipped / staged state. Shared by the success, fallback, and
// cached-tag paths so they produce identical result shapes.
func (a *App) buildUpdateResult(cfg ConfigState, tag string) CheckForUpdatesResult {
	result := CheckForUpdatesResult{
		OK:                true,
		Tag:               tag,
		CanAutoUpdate:     updatePlatformSupported(),
		AutoUpdateEnabled: cfg.autoUpdateEnabled(),
	}
	for _, skipped := range cfg.SkippedUpdates {
		if skipped == tag {
			result.Skipped = true
			break
		}
	}
	if staged := a.findStagedUpdate(); staged != "" {
		result.StagedVersion = staged
	}
	return result
}
