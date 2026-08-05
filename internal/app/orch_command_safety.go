package app

// Section 2: Command safety (was command_safety.go)
// App-owned boundary that binds internal/tools/command semantic analysis to
// workspace roots, path existence, and codedToolError. Pure command parsing
// lives in internal/tools/command.

import (
	"fmt"
	"path/filepath"

	"ally-dev/internal/tools/command"
)

// checkCommandSafety inspects commands for high-risk patterns and routes
// explicit deletion through delete_path, where workspace and OS guards apply.
// roots[0] 是主工作区（命令的默认 cwd），其余为会话级附加根目录。
func checkCommandSafety(req CommandRequest, roots []string) error {
	cmd := req.Command
	if command.ContainsExplicitDeleteCommand(cmd) && !command.IsAllowedDeleteContext(cmd) {
		return codedToolError("E_COMMAND_BLOCKED", fmt.Errorf("安全围栏已拦截：run_command 不允许直接执行文件删除命令。\n原因：shell 删除命令可能绕过工作区边界、系统目录和 .git 保护。\n处理方式：请改用 delete_path 工具，由专用工具检查目标路径和递归范围。\n被拦截的命令：%s", cmd))
	}
	if risk := firstExistingOutsideMutationTarget(cmd, roots); risk != nil {
		return codedToolError("E_PATH_OUTSIDE", fmt.Errorf("安全围栏已拦截：命令可能修改工作区外的受保护目标。\n原因：%s。\n检测到的目标：%s\n允许的操作：读取工作区外路径、写入 /dev/null 等空设备、创建不存在的新路径。\n禁止的操作：覆盖、追加、移动、改权限或以其他方式修改已经存在的工作区外文件或目录。\n允许写入的根目录：\n%s\n被拦截的命令：%s", risk.Reason, risk.Path, formatAllowedRoots(roots), cmd))
	}
	if risk := command.MatchRiskPattern(cmd); risk != nil {
		return codedToolError("E_COMMAND_BLOCKED", fmt.Errorf("高危命令拒绝: 检测到%s - 命令已被安全围栏拦截。\n如需执行此操作，请手动在终端中执行。\n被拦截的命令: %s", risk.Reason, cmd))
	}
	return nil
}

// outsideMutationRisk describes an outside-write risk that the UI can explain
// directly. Creating a new literal outside path is allowed, while changing an
// existing path or using an unresolved redirection target remains blocked.
// Harmless sinks such as /dev/null are ignored.
type outsideMutationRisk struct {
	Path   string
	Reason string
}

func firstExistingOutsideMutationTarget(commandLine string, roots []string) *outsideMutationRisk {
	if len(roots) == 0 {
		return nil
	}
	primaryRoot := roots[0]
	for _, target := range command.ShellRedirectionTargets(commandLine) {
		if command.IsShellNullDevice(target) {
			continue
		}
		path, ok := command.ResolveCommandLiteralPath(target, primaryRoot)
		if !ok {
			// 动态目标（变量/通配符/命令替换/heredoc 内容）无法静态解析：
			// 宽松策略下放行，避免对合法复杂命令误判；字面外部已存在目标仍拦截。
			continue
		}
		if insideAnyRoot(roots, path) || insideAllyAgentDir(path) {
			continue
		}
		if command.PathExists(path) {
			return &outsideMutationRisk{
				Path:   filepath.ToSlash(path),
				Reason: "重定向目标已经存在，继续执行可能覆盖或追加其内容",
			}
		}
	}

	for _, target := range command.MutationPathTargets(commandLine) {
		if command.IsShellNullDevice(target) {
			continue
		}
		path, ok := command.ResolveCommandLiteralPath(target, primaryRoot)
		if !ok {
			// 动态目标（变量/通配符/命令替换/heredoc 内容）无法静态解析：
			// 宽松策略下放行，避免对合法复杂命令误判；字面外部已存在目标仍拦截。
			continue
		}
		if insideAnyRoot(roots, path) || insideAllyAgentDir(path) {
			continue
		}
		if command.PathExists(path) {
			return &outsideMutationRisk{
				Path:   filepath.ToSlash(path),
				Reason: "命令的写入目标已经存在于工作区外，继续执行可能修改其内容或元数据",
			}
		}
	}
	return nil
}

func validateRemoteCommandSafety(cmd string) error {
	if risk := command.MatchRiskPattern(cmd); risk != nil {
		return codedToolError("E_COMMAND_BLOCKED", fmt.Errorf("高危命令拒绝: 检测到%s - 命令已被安全围栏拦截。\n如需执行此操作，请手动在终端中执行。\n被拦截的命令: %s", risk.Reason, cmd))
	}
	return nil
}

func firstAbsolutePathOutsideWorkspace(commandLine string, workspaceRoot string) string {
	root := filepath.Clean(workspaceRoot)
	for _, candidate := range command.AbsolutePathCandidates(commandLine) {
		if candidate == "" {
			continue
		}
		clean := filepath.Clean(candidate)
		if !insideRoot(root, clean) && !insideAllyAgentDir(clean) {
			return filepath.ToSlash(clean)
		}
	}
	return ""
}
