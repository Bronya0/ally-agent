package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
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

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
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
	allyReleasesAPI       = "https://api.github.com/repos/Bronya0/ally-agent/releases"
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

// StagedUpdateInfo describes an already-downloaded update on disk.
type StagedUpdateInfo struct {
	OK        bool   `json:"ok"`
	Version   string `json:"version,omitempty"`
	StagedDir string `json:"stagedDir,omitempty"`
	Error     string `json:"error,omitempty"`
}

// SkipUpdateResult is returned by SkipUpdate.
type SkipUpdateResult struct {
	OK      bool   `json:"ok"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

// updatePlatformSupported returns true on windows/amd64 (ZIP replace flow)
// and on darwin (universal DMG replace flow). All other platforms never
// trigger an automatic download.
func updatePlatformSupported() bool {
	return (goruntime.GOOS == "windows" && goruntime.GOARCH == "amd64") || goruntime.GOOS == "darwin"
}

// expectedAssetName returns the strictly-matched asset filename for a tag.
// Windows: "Ally-v1.2.3-windows-x64.zip"; macOS: "Ally-v1.2.3-macos-universal.dmg".
func expectedAssetName(tag string) string {
	if goruntime.GOOS == "darwin" {
		return fmt.Sprintf("Ally-%s-macos-universal.dmg", tag)
	}
	return fmt.Sprintf("Ally-%s-windows-x64.zip", tag)
}

func updateBaseDir() string {
	return filepath.Join(appDataDir(), updateSubDir)
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

// githubAsset is a minimal subset of the GitHub Release API asset object.
type githubAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// fetchReleaseByTag queries the GitHub Release API for a specific tag and
// returns the tag (normalized) and its assets. Uses Ally's proxy-aware client.
func (a *App) fetchReleaseByTag(tag string) (string, []githubAsset, error) {
	tag = strings.TrimSpace(tag)
	if err := validateUpdateTag(tag); err != nil {
		return "", nil, err
	}
	cfg := updateNetworkConfig(a.effectiveConfigSafe())
	client := proxyHTTPClient(cfg, false, updateHTTPTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), updateHTTPTimeout)
	defer cancel()
	apiURL := fmt.Sprintf("%s/tags/%s", allyReleasesAPI, tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ally-agent-update-check")
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("github api status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", nil, err
	}
	var parsed struct {
		TagName    string        `json:"tag_name"`
		Prerelease bool          `json:"prerelease"`
		Draft      bool          `json:"draft"`
		Assets     []githubAsset `json:"assets"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", nil, err
	}
	normalized := strings.TrimSpace(parsed.TagName)
	if err := validateUpdateTag(normalized); err != nil {
		return "", nil, fmt.Errorf("invalid release response: %w", err)
	}
	if parsed.Prerelease || parsed.Draft {
		return "", nil, fmt.Errorf("release %s is a pre-release or draft; automatic download is not allowed", normalized)
	}
	return normalized, parsed.Assets, nil
}

// ListUpdateAsset returns the Windows x64 ZIP asset info for a tag.
// On non-Windows platforms it returns OK=false with an explanatory error so
// the frontend can fall back to opening the browser.
func (a *App) ListUpdateAsset(tag string) UpdateAssetInfo {
	if !updatePlatformSupported() {
		return UpdateAssetInfo{Error: "automatic update is only supported on windows x64 and macOS"}
	}
	normalized, assets, err := a.fetchReleaseByTag(tag)
	if err != nil {
		return UpdateAssetInfo{Error: err.Error()}
	}
	want := expectedAssetName(normalized)
	for _, asset := range assets {
		if asset.Name == want {
			return UpdateAssetInfo{
				OK:   true,
				Name: asset.Name,
				Size: asset.Size,
				URL:  asset.BrowserDownloadURL,
				Tag:  normalized,
			}
		}
	}
	return UpdateAssetInfo{Error: fmt.Sprintf("no windows x64 asset found for tag %s (expected %s)", normalized, want)}
}

// downloadAsset streams an asset URL to destPath using the proxy-aware client.
// Progress is emitted via the update:progress event approximately every 500ms.
func (a *App) downloadAsset(assetURL, destPath, version string, totalBytes int64) error {
	cfg := updateNetworkConfig(a.effectiveConfigSafe())
	client := proxyHTTPClient(cfg, false, updateDownloadTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), updateDownloadTimeout)
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
	tmpPath := destPath + ".part"
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	defer func() {
		f.Close()
		_ = os.Remove(tmpPath)
	}()

	// Determine total size for progress reporting.
	total := totalBytes
	if total <= 0 && resp.ContentLength > 0 {
		total = resp.ContentLength
	}

	var lastEmit time.Time
	var received int64
	buf := make([]byte, 64*1024)
	flushProgress := func(force bool) {
		now := time.Now()
		if !force && now.Sub(lastEmit) < 500*time.Millisecond {
			return
		}
		lastEmit = now
		percent := 0
		if total > 0 {
			percent = int(received * 100 / total)
			if percent > 100 {
				percent = 100
			}
		}
		a.emit("update:progress", map[string]any{
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

// validateStagedExecutable performs a lightweight PE sanity check before the
// current installation is touched. It is not a signature check, but rejects
// empty, non-regular, truncated, and obviously non-Windows payloads.
func validateStagedExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() < 64 {
		return errors.New("staged executable is not a valid regular PE file")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	header := make([]byte, 64)
	if _, err := io.ReadFull(f, header); err != nil {
		return err
	}
	if header[0] != 'M' || header[1] != 'Z' {
		return errors.New("staged executable is missing the MZ header")
	}
	peOffset := int64(uint32(header[0x3c]) | uint32(header[0x3d])<<8 | uint32(header[0x3e])<<16 | uint32(header[0x3f])<<24)
	if peOffset < 64 || peOffset > info.Size()-4 {
		return errors.New("staged executable has an invalid PE header offset")
	}
	signature := make([]byte, 4)
	if _, err := f.ReadAt(signature, peOffset); err != nil {
		return err
	}
	if string(signature) != "PE\x00\x00" {
		return errors.New("staged executable is missing the PE signature")
	}
	return nil
}

// validateStagedDMG performs a lightweight sanity check on a downloaded DMG
// before it is mounted. It is not a cryptographic check; the mounted app
// bundle is validated separately before the live install is touched.
func validateStagedDMG(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() < 1024*1024 {
		return errors.New("staged update is not a valid DMG file")
	}
	// UDIF disk images end with a "koly" trailer. Check the last 512 bytes so
	// truncated or corrupt downloads fail early.
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	const trailerLen = 512
	buf := make([]byte, trailerLen)
	if _, err := f.ReadAt(buf, info.Size()-trailerLen); err != nil {
		return err
	}
	if !bytes.Contains(buf, []byte("koly")) {
		return errors.New("staged update is missing the UDIF trailer")
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

// validateMacAppBundle checks that a staged .app has the expected structure and
// a Mach-O (or universal) main executable before the live bundle is replaced.
func validateMacAppBundle(appDir string) error {
	info, err := os.Stat(appDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("staged app bundle is not a directory")
	}
	exePath := filepath.Join(appDir, "Contents", "MacOS", "Ally")
	exeInfo, err := os.Stat(exePath)
	if err != nil {
		return err
	}
	if !exeInfo.Mode().IsRegular() || exeInfo.Size() < 64 {
		return errors.New("staged app main executable is missing or invalid")
	}
	f, err := os.Open(exePath)
	if err != nil {
		return err
	}
	defer f.Close()
	magic := make([]byte, 4)
	if _, err := io.ReadFull(f, magic); err != nil {
		return err
	}
	switch {
	case magic[0] == 0xFE && magic[1] == 0xED && magic[2] == 0xFA && magic[3] == 0xCF: // MH_MAGIC_64
	case magic[0] == 0xCF && magic[1] == 0xFA && magic[2] == 0xED && magic[3] == 0xFE: // MH_CIGAM_64
	case magic[0] == 0xFE && magic[1] == 0xED && magic[2] == 0xFA && magic[3] == 0xCE: // MH_MAGIC
	case magic[0] == 0xCE && magic[1] == 0xFA && magic[2] == 0xED && magic[3] == 0xFE: // MH_CIGAM
	case magic[0] == 0xCA && magic[1] == 0xFE && magic[2] == 0xBA && magic[3] == 0xBE: // FAT_MAGIC (universal)
	case magic[0] == 0xBE && magic[1] == 0xBA && magic[2] == 0xFE && magic[3] == 0xCA: // FAT_CIGAM
	default:
		return errors.New("staged app main executable is not a Mach-O binary")
	}
	return nil
}

// mountDMG mounts a DMG read-only at mountPoint with hdiutil and returns the
// mount point. The caller must detach it afterwards.
func mountDMG(dmgPath, mountPoint string) (string, error) {
	if err := os.MkdirAll(mountPoint, 0o755); err != nil {
		return "", err
	}
	cmd := exec.Command("hdiutil", "attach", "-nobrowse", "-readonly", "-mountpoint", mountPoint, dmgPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("hdiutil attach: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return mountPoint, nil
}

// detachDMG unmounts a previously mounted DMG.
func detachDMG(mountPoint string) error {
	cmd := exec.Command("hdiutil", "detach", mountPoint)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("hdiutil detach: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// copyDir copies a directory tree with ditto, preserving permissions,
// symlinks, and extended attributes.
func copyDir(src, dst string) error {
	cmd := exec.Command("ditto", src, dst)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ditto: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// removeQuarantineAttr best-effort clears the quarantine attribute on an app
// bundle. Ally-downloaded DMGs carry none, but clearing keeps the replaced
// bundle launchable even if the attribute somehow exists.
func removeQuarantineAttr(appDir string) {
	_ = exec.Command("xattr", "-dr", "com.apple.quarantine", appDir).Run()
}

// DownloadUpdate downloads the platform-matched archive (ZIP on Windows, DMG
// on macOS) for the given tag. Emits update:progress during download and
// update:ready (or update:error) on completion.
func (a *App) DownloadUpdate(tag string) UpdateDownloadResult {
	if !updatePlatformSupported() {
		return UpdateDownloadResult{Error: "automatic update is only supported on windows x64 and macOS"}
	}
	asset, err := a.ListUpdateAsset(tag).okOrError()
	if err != nil {
		a.emit("update:error", map[string]any{"stage": "match", "error": err.Error()})
		return UpdateDownloadResult{Error: err.Error()}
	}
	versionDir := updateVersionDir(asset.Tag)
	stagedDir := updateStagedDir(asset.Tag)
	archivePath := updateAssetPath(asset.Tag)

	// Clean any previous staged state for this version.
	_ = os.RemoveAll(versionDir)
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		msg := fmt.Sprintf("create version dir: %v", err)
		a.emit("update:error", map[string]any{"stage": "prepare", "error": msg})
		return UpdateDownloadResult{Error: msg}
	}

	if err := a.downloadAsset(asset.URL, archivePath, asset.Tag, asset.Size); err != nil {
		msg := fmt.Sprintf("download: %v", err)
		a.emit("update:error", map[string]any{"stage": "download", "error": msg})
		_ = os.RemoveAll(versionDir)
		return UpdateDownloadResult{Error: msg}
	}

	if goruntime.GOOS == "darwin" {
		// macOS: no extraction. Validate the DMG and report ready; the actual
		// mount and bundle validation happen at apply time.
		if err := validateStagedDMG(archivePath); err != nil {
			msg := fmt.Sprintf("invalid staged dmg: %v", err)
			a.emit("update:error", map[string]any{"stage": "verify", "error": msg})
			_ = os.RemoveAll(versionDir)
			return UpdateDownloadResult{Error: msg}
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

	a.emit("update:progress", map[string]any{
		"stage":   "extract",
		"version": asset.Tag,
		"percent": 0,
	})
	if err := extractZip(archivePath, stagedDir); err != nil {
		msg := fmt.Sprintf("extract: %v", err)
		a.emit("update:error", map[string]any{"stage": "extract", "error": msg})
		_ = os.RemoveAll(versionDir)
		return UpdateDownloadResult{Error: msg}
	}
	a.emit("update:progress", map[string]any{
		"stage":   "extract",
		"version": asset.Tag,
		"percent": 100,
	})

	// Verify the staged Ally.exe before declaring ready.
	stagedExe := filepath.Join(stagedDir, "Ally.exe")
	if err := validateStagedExecutable(stagedExe); err != nil {
		msg := fmt.Sprintf("invalid staged executable: %v", err)
		a.emit("update:error", map[string]any{"stage": "verify", "error": msg})
		_ = os.RemoveAll(versionDir)
		return UpdateDownloadResult{Error: msg}
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

// copyFile copies src to dst with a temp-file + rename so the destination
// is replaced atomically (same volume only).
func copyFileAtomic(src, dst string) error {
	srcF, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcF.Close()
	info, err := srcF.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := io.Copy(tmp, srcF); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, info.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tmpName, dst)
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
		rel, err := filepath.Rel(stagedDir, path)
		if err != nil {
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
		_ = os.RemoveAll(filepath.Join(rootDir, tag))
	}
}

// ApplyUpdate stops all runs and services, then replaces the current
// installation from the staged archive (Windows EXE/replace flow or macOS
// bundle replace flow). On any failure it rolls back so the previous
// installation keeps running.
func (a *App) ApplyUpdate(tag string) UpdateApplyResult {
	if !updatePlatformSupported() {
		return UpdateApplyResult{Error: "automatic update is only supported on windows x64 and macOS"}
	}
	if err := validateUpdateTag(tag); err != nil {
		return UpdateApplyResult{Error: err.Error()}
	}
	if goruntime.GOOS == "darwin" {
		return a.applyMacUpdate(tag)
	}
	return a.applyWindowsUpdate(tag)
}

// applyWindowsUpdate stops all runs, services, and MCP servers, then replaces
// the Ally executable and resource files from the staged directory. On any
// failure it rolls back the EXE rename so the previous binary keeps running.
func (a *App) applyWindowsUpdate(tag string) UpdateApplyResult {
	stagedDir := updateStagedDir(tag)
	if _, err := os.Stat(stagedDir); err != nil {
		return UpdateApplyResult{Error: fmt.Sprintf("staged dir not found: %v", err)}
	}
	stagedExe := filepath.Join(stagedDir, "Ally.exe")
	if err := validateStagedExecutable(stagedExe); err != nil {
		return UpdateApplyResult{Error: fmt.Sprintf("invalid staged executable: %v", err)}
	}
	exeDir, err := allyExecutableDir()
	if err != nil {
		return UpdateApplyResult{Error: fmt.Sprintf("locate executable: %v", err)}
	}
	if err := isDirWritable(exeDir); err != nil {
		return UpdateApplyResult{Error: fmt.Sprintf("install dir not writable: %v", err)}
	}

	a.emit("update:progress", map[string]any{"stage": "apply", "version": tag, "percent": 0})

	// Stop everything that could hold a handle on Ally.exe.
	// MCP servers are independent subprocesses and do not hold the Ally.exe
	// file handle; they are shut down later by the ctx.Done() path triggered
	// by QuitForUpdate's wruntime.Quit. Keeping them alive here means a
	// rolled-back update leaves MCP fully functional.
	if err := a.stopAllRuns(); err != nil {
		a.emit("update:error", map[string]any{"stage": "apply", "error": err.Error()})
		return UpdateApplyResult{Error: err.Error()}
	}
	a.stopAllServices()

	exeName := "Ally.exe"
	currentExe := filepath.Join(exeDir, exeName)
	backupExe := currentExe + exeBackupSuffix

	// Remove any stale backup from a previous failed attempt.
	_ = os.Remove(backupExe)

	// Rename the running EXE to .bak. On Windows this is allowed even while
	// the process is running; the file handle stays valid until exit.
	if err := os.Rename(currentExe, backupExe); err != nil {
		msg := fmt.Sprintf("backup current exe: %v", err)
		a.emit("update:error", map[string]any{"stage": "apply", "error": msg})
		return UpdateApplyResult{Error: msg}
	}

	// Copy the new EXE into place.
	if err := copyFileAtomic(stagedExe, currentExe); err != nil {
		// Roll back: restore the backup.
		if rbErr := os.Rename(backupExe, currentExe); rbErr != nil {
			// Last-resort fallback: try to copy the staged EXE directly so
			// the install is not left without an Ally.exe. If this also
			// fails, the user must manually restore from the staged dir.
			if cpErr := copyFileAtomic(stagedExe, currentExe); cpErr == nil {
				_ = os.Remove(backupExe)
				msg := fmt.Sprintf("copy new exe failed (%v) and rollback rename also failed (%v); recovered by copying staged exe", err, rbErr)
				a.emit("update:error", map[string]any{"stage": "rollback", "error": msg})
				return UpdateApplyResult{Error: msg}
			}
			a.emit("update:error", map[string]any{
				"stage": "rollback",
				"error": fmt.Sprintf("copy exe failed: %v; rollback rename failed: %v; manual recovery required from %s", err, rbErr, stagedDir),
			})
			return UpdateApplyResult{Error: fmt.Sprintf("copy exe failed: %v; rollback rename failed: %v; manual recovery required from %s", err, rbErr, stagedDir)}
		}
		msg := fmt.Sprintf("copy new exe: %v (rolled back)", err)
		a.emit("update:error", map[string]any{"stage": "apply", "error": msg})
		return UpdateApplyResult{Error: msg}
	}

	// Replace supporting resource files. On any failure, roll back the EXE
	// so the previous binary is restored and the .bak is not orphaned for
	// the next startup to blindly delete.
	files, err := stagedFileSet(stagedDir)
	if err != nil {
		msg := fmt.Sprintf("list staged resources: %v", err)
		_ = os.Rename(backupExe, currentExe)
		a.emit("update:error", map[string]any{"stage": "apply", "error": msg})
		return UpdateApplyResult{Error: msg}
	}
	for _, rel := range files {
		if rel == exeName {
			continue // already handled
		}
		src := filepath.Join(stagedDir, rel)
		dst := filepath.Join(exeDir, rel)
		if err := copyFileAtomic(src, dst); err != nil {
			msg := fmt.Sprintf("replace resource %s: %v", rel, err)
			if rbErr := os.Rename(backupExe, currentExe); rbErr != nil {
				msg = fmt.Sprintf("%s; rollback exe also failed: %v; manual recovery required from %s", msg, rbErr, stagedDir)
			}
			a.emit("update:error", map[string]any{"stage": "apply", "error": msg})
			return UpdateApplyResult{Error: msg}
		}
	}

	cleanAppliedUpdateDirs(updateBaseDir(), tag)
	a.emit("update:progress", map[string]any{"stage": "apply", "version": tag, "percent": 100})
	a.emit("update:applied", map[string]any{"version": tag})
	return UpdateApplyResult{OK: true}
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

	a.emit("update:progress", map[string]any{"stage": "apply", "version": tag, "percent": 0})

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
	if err := validateMacAppBundle(stagedApp); err != nil {
		msg := fmt.Sprintf("invalid staged app: %v", err)
		a.emit("update:error", map[string]any{"stage": "apply", "error": msg})
		return UpdateApplyResult{Error: msg}
	}

	backupApp := appDir + appBackupSuffix
	_ = os.RemoveAll(backupApp)
	if err := os.Rename(appDir, backupApp); err != nil {
		msg := fmt.Sprintf("backup current app: %v", err)
		a.emit("update:error", map[string]any{"stage": "apply", "error": msg})
		return UpdateApplyResult{Error: msg}
	}

	// Copy the new bundle into place with ditto, which preserves permissions
	// and symlinks. The destination no longer exists after the rename above.
	if err := copyDir(stagedApp, appDir); err != nil {
		_ = os.RemoveAll(appDir)
		if rbErr := os.Rename(backupApp, appDir); rbErr != nil {
			msg := fmt.Sprintf("copy new app failed (%v) and rollback rename also failed (%v); manual recovery required from %s", err, rbErr, backupApp)
			a.emit("update:error", map[string]any{"stage": "rollback", "error": msg})
			return UpdateApplyResult{Error: msg}
		}
		msg := fmt.Sprintf("copy new app: %v (rolled back)", err)
		a.emit("update:error", map[string]any{"stage": "apply", "error": msg})
		return UpdateApplyResult{Error: msg}
	}

	removeQuarantineAttr(appDir)

	cleanAppliedUpdateDirs(updateBaseDir(), tag)
	a.emit("update:progress", map[string]any{"stage": "apply", "version": tag, "percent": 100})
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
		return errors.New("automatic update is only supported on windows x64 and macOS")
	}
	if a.ctx == nil {
		return errors.New("app context not initialized")
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
		wruntime.Quit(a.ctx)
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
			cleanupUpdateBackupIfUnchanged(backupPath, original)
		}
	}()
}

func cleanupUpdateBackupIfUnchanged(path string, original os.FileInfo) {
	info, err := os.Stat(path)
	if err != nil || !os.SameFile(original, info) {
		return
	}
	_ = os.RemoveAll(path)
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
		_ = os.RemoveAll(appDir + appBackupSuffix)
		return
	}
	exeDir, err := allyExecutableDir()
	if err != nil {
		return
	}
	backup := filepath.Join(exeDir, "Ally.exe"+exeBackupSuffix)
	_ = os.Remove(backup)
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

// GetStagedUpdate returns info about the most recent staged update on disk,
// if any. Used by the frontend to decide whether to show "restart to apply"
// without re-downloading.
func (a *App) GetStagedUpdate() StagedUpdateInfo {
	tag := a.findStagedUpdate()
	if tag == "" {
		return StagedUpdateInfo{Error: "no staged update found"}
	}
	var stagedDir string
	var probe string
	if goruntime.GOOS == "darwin" {
		stagedDir = updateVersionDir(tag)
		probe = updateAssetPath(tag)
	} else {
		stagedDir = updateStagedDir(tag)
		probe = filepath.Join(stagedDir, "Ally.exe")
	}
	if _, err := os.Stat(probe); err != nil {
		return StagedUpdateInfo{Error: fmt.Sprintf("staged update missing: %v", err)}
	}
	return StagedUpdateInfo{
		OK:        true,
		Version:   tag,
		StagedDir: stagedDir,
	}
}

// SkipUpdate marks a release tag as skipped. Future automatic download checks
// will ignore this tag until the user clears the skip list. Any staged files
// for the tag are removed so disk space is not held by a skipped version.
func (a *App) SkipUpdate(version string) SkipUpdateResult {
	version = strings.TrimSpace(version)
	if version == "" {
		return SkipUpdateResult{Error: "version is required"}
	}
	a.mu.Lock()
	cfg := a.config
	already := false
	for _, s := range cfg.SkippedUpdates {
		if s == version {
			already = true
			break
		}
	}
	if !already {
		cfg.SkippedUpdates = append(cfg.SkippedUpdates, version)
		a.config = cfg
	}
	configPath := a.configPath
	a.mu.Unlock()

	if !already {
		data, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return SkipUpdateResult{Error: fmt.Sprintf("marshal config: %v", err)}
		}
		if err := os.WriteFile(configPath, data, 0o600); err != nil {
			return SkipUpdateResult{Error: fmt.Sprintf("persist skip list: %v", err)}
		}
	}
	// Remove staged files for the skipped version so disk space is released.
	_ = os.RemoveAll(updateVersionDir(version))
	return SkipUpdateResult{OK: true, Version: version}
}

// ClearSkippedUpdates empties the skipped-update list so automatic downloads
// resume for previously skipped tags. Returns the number of cleared entries.
func (a *App) ClearSkippedUpdates() int {
	a.mu.Lock()
	cfg := a.config
	n := len(cfg.SkippedUpdates)
	if n == 0 {
		a.mu.Unlock()
		return 0
	}
	cfg.SkippedUpdates = nil
	a.config = cfg
	configPath := a.configPath
	a.mu.Unlock()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return 0
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		return 0
	}
	return n
}
