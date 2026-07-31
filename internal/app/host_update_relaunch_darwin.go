//go:build darwin

package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	updateRelaunchHelperArg = "--ally-update-relaunch-helper"
	updateRelaunchTimeout   = 60 * time.Second
	updateParentPipeFD      = 3
)

var (
	updateRelaunchMu        sync.Mutex
	updateRelaunchKeepalive *os.File
)

func startUpdateRelaunchHelper(appDir string) error {
	appDir = filepath.Clean(strings.TrimSpace(appDir))
	if filepath.Ext(appDir) != ".app" {
		return fmt.Errorf("invalid app bundle path %q", appDir)
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}

	updateRelaunchMu.Lock()
	defer updateRelaunchMu.Unlock()
	if updateRelaunchKeepalive != nil {
		reader.Close()
		writer.Close()
		return errors.New("update relaunch helper already started")
	}

	logPath := filepath.Join(appDataDir(), "update-relaunch.log")
	logFile, _ := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	cmd := exec.Command(executable, updateRelaunchHelperArg, appDir)
	cmd.Stdin = nil
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	cmd.ExtraFiles = []*os.File{reader}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		reader.Close()
		writer.Close()
		if logFile != nil {
			logFile.Close()
		}
		return err
	}
	reader.Close()
	if logFile != nil {
		logFile.Close()
	}
	if err := cmd.Process.Release(); err != nil {
		writer.Close()
		return err
	}
	// Keep the write end alive until process teardown. The detached helper owns
	// only the read end and receives EOF for this exact parent, avoiding PID
	// reuse races without interpolating paths into a shell command.
	updateRelaunchKeepalive = writer
	return nil
}

// RunUpdateRelaunchHelper handles the detached macOS relaunch mode before
// Wails starts. Paths are passed as argv entries, never interpolated into a
// shell command, so installation-directory characters cannot execute code.
func RunUpdateRelaunchHelper(args []string) (bool, error) {
	if len(args) == 0 || args[0] != updateRelaunchHelperArg {
		return false, nil
	}
	if len(args) != 2 {
		return true, errors.New("invalid update relaunch helper arguments")
	}
	appDir := filepath.Clean(strings.TrimSpace(args[1]))
	if filepath.Ext(appDir) != ".app" {
		return true, fmt.Errorf("invalid app bundle path %q", appDir)
	}
	parentPipe := os.NewFile(uintptr(updateParentPipeFD), "ally-update-parent")
	if parentPipe == nil {
		return true, errors.New("update parent pipe is unavailable")
	}
	defer parentPipe.Close()
	if err := waitForParentPipe(parentPipe, updateRelaunchTimeout); err != nil {
		return true, err
	}
	cmd := exec.Command("open", appDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return true, fmt.Errorf("open updated app: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return true, nil
}

func waitForParentPipe(parentPipe *os.File, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() {
		var one [1]byte
		_, err := parentPipe.Read(one[:])
		if errors.Is(err, io.EOF) {
			err = nil
		}
		done <- err
	}()
	if timeout <= 0 {
		timeout = updateRelaunchTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("wait for parent process: %w", err)
		}
		return nil
	case <-timer.C:
		return errors.New("timed out waiting for parent process to exit")
	}
}
