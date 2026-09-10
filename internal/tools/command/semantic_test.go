// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package command

import (
	"slices"
	"strings"
	"testing"
)

func TestDeleteDetectionUsesCommandPositions(t *testing.T) {
	allowedData := []string{
		`grep -R "rm" docs`,
		`echo rm`,
		`printf 'remove-item\n'`,
		`git log --grep=shutdown`,
	}
	for _, commandLine := range allowedData {
		if ContainsExplicitDeleteCommand(commandLine) {
			t.Fatalf("data must not be treated as a deletion command: %q", commandLine)
		}
	}

	blocked := []string{
		`rm generated.txt`,
		`sudo rm generated.txt`,
		`bash -c "rm generated.txt"`,
		`cmd.exe /c "del generated.txt"`,
		`echo generated.txt | xargs rm`,
		`eval "rm generated.txt"`,
		`echo $(rm generated.txt)`,
		"echo `rm generated.txt`",
		`find . -delete`,
		`rsync --archive --delete source/ destination/`,
	}
	for _, commandLine := range blocked {
		if !ContainsExplicitDeleteCommand(commandLine) {
			t.Fatalf("expected deletion command to be detected: %q", commandLine)
		}
	}
}

func TestAllowedDeleteContextRequiresEveryDeletionToBeManaged(t *testing.T) {
	allowed := []string{
		`git rm --cached tracked.txt`,
		`docker container rm old-container`,
		`kubectl delete pod old-pod`,
		`npm remove old-package`,
	}
	for _, commandLine := range allowed {
		if !ContainsExplicitDeleteCommand(commandLine) || !IsAllowedDeleteContext(commandLine) {
			t.Fatalf("expected managed deletion to be allowed: %q", commandLine)
		}
	}

	mixed := `git rm --cached tracked.txt; rm generated.txt`
	if !ContainsExplicitDeleteCommand(mixed) || IsAllowedDeleteContext(mixed) {
		t.Fatalf("mixed managed and raw deletion must not be allowed: %q", mixed)
	}
}

func TestDeleteDetectionUnwrapsCommonWrappers(t *testing.T) {
	blocked := []string{
		`timeout 5 rm generated.txt`,
		`timeout -s KILL 5 rm -rf /tmp/x`,
		`timeout -v 5 rm generated.txt`,
		`timeout --verbose 5 rm generated.txt`,
		`stdbuf -oL rm generated.txt`,
		`stdbuf --output=L rm generated.txt`,
		`setsid rm generated.txt`,
		`ionice -c3 rm generated.txt`,
	}
	for _, commandLine := range blocked {
		if !ContainsExplicitDeleteCommand(commandLine) {
			t.Fatalf("expected wrapped deletion to be detected: %q", commandLine)
		}
	}
	allowed := []string{
		`timeout 5 echo rm`,
		`timeout -v 5 echo rm`,
		`stdbuf -oL echo rm`,
		`setsid echo rm`,
		`ionice -c3 echo rm`,
		`timeout 5 git status`,
	}
	for _, commandLine := range allowed {
		if ContainsExplicitDeleteCommand(commandLine) {
			t.Fatalf("data must not be treated as a deletion command: %q", commandLine)
		}
	}
}

func TestRiskDetectionIgnoresArgumentsButBlocksActualCommands(t *testing.T) {
	allowed := []string{
		`git log --grep=shutdown`,
		`echo "reboot"`,
		`rg mkfs docs`,
		`dd if=input.bin bs=1 count=16`,
		`chmod 064 file.txt`,
		`chmod 044 file.txt`,
	}
	for _, commandLine := range allowed {
		if risk := MatchRiskPattern(commandLine); risk != nil {
			t.Fatalf("normal command %q was classified as %s", commandLine, risk.Reason)
		}
	}

	blocked := []string{
		`shutdown -h now`,
		`sudo reboot`,
		`mkfs.ext4 /dev/sda1`,
		`dd if=image.iso of=/dev/sda`,
		`chmod 000 secrets.txt`,
		`printf x > /dev/sda`,
		`bash -c "poweroff"`,
	}
	for _, commandLine := range blocked {
		if risk := MatchRiskPattern(commandLine); risk == nil {
			t.Fatalf("expected high-risk command to be detected: %q", commandLine)
		}
	}
}

func TestMutationPathTargetsDistinguishInputsFromOutputs(t *testing.T) {
	tests := []struct {
		commandLine string
		want        []string
	}{
		{`cp /tmp/source.txt ./source.txt`, []string{"./source.txt"}},
		{`mv /tmp/source.txt ./source.txt`, []string{"./source.txt"}},
		{`python -c "print(open('/etc/hosts').read())"`, nil},
		{`node -e "console.log(require('fs').readFileSync('/etc/hosts'))"`, nil},
		{`unzip -l /tmp/archive.zip`, nil},
		{`unzip /tmp/archive.zip -d .`, []string{"."}},
		{`tar -xf /tmp/archive.tar -C .`, []string{"."}},
		{`touch -r /tmp/reference.txt ./target.txt`, []string{"./target.txt"}},
		{`sed -i 's/a/b/' /tmp/file.txt`, []string{"/tmp/file.txt"}},
		{`dd if=input.bin of=output.bin`, []string{"output.bin"}},
	}
	for _, test := range tests {
		if got := MutationPathTargets(test.commandLine); !slices.Equal(got, test.want) {
			t.Fatalf("MutationPathTargets(%q) = %#v, want %#v", test.commandLine, got, test.want)
		}
	}
}

func TestLiteralWriteTargets(t *testing.T) {
	tests := []struct {
		commandLine string
		want        []WriteTarget
	}{
		{`echo hi > out.txt`, []WriteTarget{{"out.txt", WriteTargetRedirection}}},
		{`cp /tmp/source.txt ./dest.txt`, []WriteTarget{{"./dest.txt", WriteTargetMutation}}},
		{`echo hi > /dev/null`, nil},
		{`echo hi > $VAR`, nil},
		{`echo hi > /tmp/out.txt`, []WriteTarget{{"/tmp/out.txt", WriteTargetRedirection}}},
		{`echo hi >> app.log 2>&1`, []WriteTarget{{"app.log", WriteTargetRedirection}}},
	}
	for _, test := range tests {
		if got := LiteralWriteTargets(test.commandLine); !slices.Equal(got, test.want) {
			t.Fatalf("LiteralWriteTargets(%q) = %#v, want %#v", test.commandLine, got, test.want)
		}
	}
}

func TestShellASTDecodesQuotedWindowsPathsAndSkipsFileDescriptorRedirection(t *testing.T) {
	invocations := Invocations(`cp "C:\Users\me\source.txt" "C:\Users\me\dest.txt"`)
	if len(invocations) != 1 {
		t.Fatalf("Invocations() returned %d calls, want 1: %#v", len(invocations), invocations)
	}
	wantArgs := []string{`C:\Users\me\source.txt`, `C:\Users\me\dest.txt`}
	if !slices.Equal(invocations[0].Args, wantArgs) {
		t.Fatalf("Invocations() args = %#v, want %#v", invocations[0].Args, wantArgs)
	}

	targets := ShellRedirectionTargets(`printf x > "C:\Temp\out.txt" 2>&1`)
	wantTargets := []string{`C:\Temp\out.txt`}
	if !slices.Equal(targets, wantTargets) {
		t.Fatalf("ShellRedirectionTargets() = %#v, want %#v", targets, wantTargets)
	}
}

func TestLegacyScannerFlushesWordBeforeHeredocBody(t *testing.T) {
	// A fragment the bash parser rejects (PowerShell-style braces) falls back to
	// the legacy scanner. The heredoc body is data: the pending word must be
	// flushed before skipping to the terminator, otherwise the first token after
	// the body is glued onto `cat` and every later invocation disappears from
	// risk analysis — which would let a deletion command through the fence.
	commandLine := "{ cat <<EOF\nbody\nEOF\nRemove-Item C:\\important }"
	calls := invocations(commandLine, 0)
	found := false
	for _, call := range calls {
		if strings.EqualFold(call.Name, "remove-item") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("legacy scanner lost the invocation after the heredoc body: %#v", calls)
	}
	if !ContainsExplicitDeleteCommand(commandLine) {
		t.Fatalf("deletion after a heredoc body must be detected: %q", commandLine)
	}
}

func TestResolveCommandLiteralPathRefusesTildeExpansion(t *testing.T) {
	root := t.TempDir()
	// Real shells expand `~` to the home directory, so a tilde target cannot be
	// judged as an in-workspace literal path; it must refuse static resolution.
	for _, value := range []string{"~/.bashrc", "~/notes.txt", "~/"} {
		if got, ok := ResolveCommandLiteralPath(value, root); ok {
			t.Fatalf("tilde target %q must not resolve (got %q)", value, got)
		}
	}
	// Ordinary relative and absolute targets still resolve unchanged.
	got, ok := ResolveCommandLiteralPath("sub/file.txt", root)
	if !ok || !strings.HasPrefix(got, root) {
		t.Fatalf("plain relative target must resolve inside the workspace, got %q ok=%v", got, ok)
	}
}
