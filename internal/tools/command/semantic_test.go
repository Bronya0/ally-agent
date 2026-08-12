package command

import (
	"slices"
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
