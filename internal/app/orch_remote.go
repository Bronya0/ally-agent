// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"bytes"
	"compress/zlib"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"ally-dev/internal/tools/command"
	"ally-dev/internal/tools/edit"
	"ally-dev/internal/tools/pathutil"
	"ally-dev/internal/tools/read"
	"ally-dev/internal/tools/sshclient"
	"ally-dev/internal/tools/streamarchive"
)

const remotePythonMarker = "ALLY_REMOTE_RESULT_JSON:"

// maxRemoteReadBatchBytes 是 read_batch 单会话的总字节预算：装不下的剩余
// 路径排入下一轮会话。文件总量在预算内时 20 个文件一轮读完；文件很大时
// 每轮投递更少，最坏每轮一个文件（与旧的逐文件实现持平，不多于旧的每文
// 件一次完整握手）。单文件体积上限 maxReadFileBytes 为 32MB，因此极端
// 情况下单文件本身即可吃满本预算。
const maxRemoteReadBatchBytes = 16 * 1024 * 1024

// remotePythonScript 是远端 helper：整段脚本经 ssh stdin 发到远端执行。远端
// 远端只支持 Linux；解释器可能是 python3，也可能是老系统上唯一的 python
// （Python 2.7），所以脚本必须同时满足两套语义，改动时守这三条：
//   - 文本走边界：payload 文本在入口统一转成 OS 认的形态（py2 是 UTF-8 字节串、
//     py3 是 str），见 to_os_text / payload_os_text；py2 下禁止对 payload 文本用
//     裸 str()——py2 的 str(unicode) 按 ASCII 编码，命令/路径含非 ASCII 直接抛错。
//   - 能力探测：os.* / subprocess / signal 的可选 API（fchmod、replace、DEVNULL、
//     信号表、setsid…）一律先探测再降级，不假设平台与版本。
//   - 语法底线 Python 2.6 / 3.0：不用 f-string、海象运算符等 2.6 解析不了的写法，
//     也不用 3.5+ 才有的标准库入口（subprocess.run 等）。
//
//go:embed remote_helper.py
var remotePythonScript string

var remoteScriptTemplateOnce struct {
	sync.Once
	script string
	err    error
}

type remotePythonResponse struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

func parseRemoteTarget(raw string) (remoteTarget, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return remoteTarget{}, errors.New("target is required")
	}
	if strings.HasPrefix(raw, "ssh://") {
		u, err := url.Parse(raw)
		if err != nil {
			return remoteTarget{}, fmt.Errorf("invalid ssh target: %w", err)
		}
		if u.Hostname() == "" || u.Path == "" {
			return remoteTarget{}, errors.New("ssh target must include host and absolute workspace path")
		}
		host := u.Hostname()
		if u.User != nil {
			host = u.User.String() + "@" + host
		}
		root := path.Clean(u.Path)
		if root == "." || !strings.HasPrefix(root, "/") || root == "/" {
			return remoteTarget{}, errors.New("remote workspaceRoot must be an absolute non-root path")
		}
		return remoteTarget{Raw: raw, Host: host, Port: u.Port(), WorkspaceRoot: root}, nil
	}
	idx := strings.LastIndex(raw, ":")
	if idx <= 0 || idx == len(raw)-1 {
		return remoteTarget{}, errors.New("target must be formatted as host:/absolute/workspace")
	}
	host := strings.TrimSpace(raw[:idx])
	root := strings.TrimSpace(raw[idx+1:])
	if host == "" {
		return remoteTarget{}, errors.New("target host is required")
	}
	root = path.Clean(filepath.ToSlash(root))
	if root == "." || !strings.HasPrefix(root, "/") || root == "/" {
		return remoteTarget{}, errors.New("remote workspaceRoot must be an absolute non-root path")
	}
	return remoteTarget{Raw: raw, Host: host, WorkspaceRoot: root}, nil
}

// resolveAndAuthorizeRemoteTarget parses raw and resolves any cluster alias.
// In interactive agent sessions (sessionID != ""), it enforces Tab-level isolation
// without per-operation confirmation prompts.
func (a *App) resolveAndAuthorizeRemoteTarget(ctx context.Context, raw string) (remoteTarget, error) {
	rt, err := parseRemoteTarget(raw)
	if err != nil {
		return remoteTarget{}, err
	}

	// 已登记节点有两种写法：别名，或真实端点（user@host[:port]）——用户给模型的
	// 多半是后者。两者是同一个身份：认到节点就照它的人名/端口/凭据走，并沿用它的
	// 工作区白名单；只认别名会让已授权、已存密码的节点换个写法又变成陌生主机。
	serverKey := strings.ToLower(strings.TrimSpace(rt.Host))
	serverNode, isRegistered := a.GetSSHServer(serverKey)
	if !isRegistered {
		serverNode, isRegistered = a.findSSHServerByEndpoint(rt.Host, rt.Port)
		if isRegistered {
			serverKey = strings.ToLower(strings.TrimSpace(serverNode.Alias))
		}
	}

	// 会话上下文决定工作区隔离；原始端点直接连接，不做逐次审批。
	sessionID := toolSessionID(ctx)
	var workspace string
	if sessionID != "" {
		a.mu.Lock()
		workspace = a.sessionWorkspaces[sessionID]
		if workspace == "" {
			workspace = a.config.Workspace
		}
		a.mu.Unlock()
	}

	if isRegistered {
		rt.Host = nodeSSHHost(serverNode)
		if serverNode.Port > 0 && serverNode.Port != 22 {
			rt.Port = strconv.Itoa(serverNode.Port)
		}
	}

	// In interactive sessions, enforce workspace authorization for registered nodes.
	if sessionID != "" {
		// 1. Tab-level authorization (prevent lateral movement / diffusion)
		if isRegistered && !a.IsServerAuthorizedForWorkspace(workspace, serverNode.ID) {
			return remoteTarget{}, codedToolError("E_SERVER_UNAUTHORIZED",
				fmt.Errorf("server %q is not authorized for current workspace tab; please authorize it in the tab footer or request access via ssh_cluster", serverKey))
		}

	}

	// 工作区授权通过后才缓存节点凭据。
	// 槽位由解析后的端点决定，invokeRemotePython 按同一端点读取凭据。
	if isRegistered && (serverNode.Password != "" || serverNode.KeyPath != "") {
		a.sshCredentials.store(sshCredentialKey(rt.Host, rt.Port), normalizeSSHAuthType(serverNode), serverNode.Password, serverNode.KeyPath)
	}

	return rt, nil
}

// validateRemoteWorkspacePath validates a path (or cwd) against the remote
// workspace root. Relative paths pass through as before. Absolute paths are
// rebased when they point inside the root: the root itself maps to "." and
// deeper paths to their relative remainder — a caller that already knows the
// absolute location (e.g. cwd="/srv/app" with target root "/srv/app") is not
// punished for spelling it absolutely. Absolute paths outside the root are
// rejected with an error that names the boundary, mirroring the local
// E_PATH_OUTSIDE contract.
func validateRemoteWorkspacePath(p, root string, allowRoot bool) (string, error) {
	p = strings.TrimSpace(filepath.ToSlash(p))
	if strings.HasPrefix(p, "/") {
		root = path.Clean(filepath.ToSlash(root))
		if p == root {
			if !allowRoot {
				return "", errors.New("path is required")
			}
			return ".", nil
		}
		if strings.HasPrefix(p, root+"/") {
			p = strings.TrimPrefix(p, root+"/")
		} else {
			return "", fmt.Errorf("absolute path %s is outside the remote workspaceRoot %s; use a path relative to the root (empty means the root itself)", p, root)
		}
	}
	return validateRemoteRelativePath(p, allowRoot)
}

// validateRemoteRelativePath validates a path already known to be relative
// against the remote workspace root: no NUL bytes, no leading "/", no ".."
// traversal, and path.Clean'd. Empty or "." resolves to the root itself when
// allowRoot is set (used for remote_run_command cwd).
func validateRemoteRelativePath(p string, allowRoot bool) (string, error) {
	p = strings.TrimSpace(filepath.ToSlash(p))
	if p == "" || p == "." {
		if allowRoot {
			return ".", nil
		}
		return "", errors.New("path is required")
	}
	if strings.ContainsRune(p, 0) {
		return "", errors.New("path contains NUL byte")
	}
	if strings.HasPrefix(p, "/") {
		return "", errors.New("path must be relative to remote workspaceRoot")
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return "", errors.New("path must not contain '..'")
		}
	}
	clean := path.Clean(p)
	if clean == "." {
		if allowRoot {
			return ".", nil
		}
		return "", errors.New("path is required")
	}
	return clean, nil
}

func remotePayload(rt remoteTarget, op string, extra map[string]any) map[string]any {
	payload := map[string]any{
		"op":            op,
		"workspaceRoot": rt.WorkspaceRoot,
	}
	for k, v := range extra {
		payload[k] = v
	}
	return payload
}

// buildRemoteScript 把敏感保护清单与 payload（base64url）注入 Python 脚本
// 占位符，整个脚本经 ssh stdin 发送。先替换清单、最后替换 payload，
// 避免编码后的 payload 恰好包含清单占位符文本时被二次替换。
func buildRemoteScript(payload map[string]any) (string, error) {
	// Protection targets are process-wide constants; inject them once, while
	// keeping every request's payload separate and every remote path check live.
	remoteScriptTemplateOnce.Do(func() {
		var targets []byte
		targets, remoteScriptTemplateOnce.err = json.Marshal(removableProtectedTargets())
		if remoteScriptTemplateOnce.err == nil {
			remoteScriptTemplateOnce.script = strings.Replace(remotePythonScript, "__DELETE_SENSITIVE_TARGETS__", string(targets), 1)
		}
	})
	if remoteScriptTemplateOnce.err != nil {
		return "", remoteScriptTemplateOnce.err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	return strings.Replace(remoteScriptTemplateOnce.script, "__PAYLOAD_B64__", encoded, 1), nil
}

// remoteHelperError 把 Python helper 的错误映射为 Go 侧错误。E_PATH_OUTSIDE
// 前缀错误转成结构化错误码，供模型侧恢复提示使用。
func remoteHelperError(resp remotePythonResponse) error {
	if resp.Error == "" {
		resp.Error = "remote helper failed"
	}
	// 每个前缀都要转成结构化错误码：模型侧靠错误码认「这是边界拒绝」，纯文本
	// 会被当成操作自己报的错而重试。清单按远端 helper 实际会带的码收（本地同类
	// 拒绝用的是同一批码），helper 里新增一处带码的拒绝就在这里补一行。
	for _, code := range []string{"E_PATH_OUTSIDE", "E_PROTECTED_PATH", "E_DELETE_BLOCKED"} {
		if strings.HasPrefix(resp.Error, code+":") {
			msg := strings.TrimSpace(strings.TrimPrefix(resp.Error, code+":"))
			return codedToolError(code, errors.New(msg))
		}
	}
	return errors.New(resp.Error)
}

// remoteWriteTargets 提取命令中可静态解析的字面写入目标（与本地
// command 共用 command.LiteralWriteTargets 同一份提取逻辑），
// 交给远端 Python helper 做文件系统事实检查。
func remoteWriteTargets(commandLine string) []string {
	all := command.LiteralWriteTargets(commandLine)
	targets := make([]string, 0, len(all))
	for _, t := range all {
		targets = append(targets, t.Path)
	}
	return targets
}

// remoteVCSMetadataWrite 拒绝把版本控制元数据当写目标的远端路径。规则与本地写
// 路径、删除路径、命令目标同源（pathutil.VCSMetadataReason），新增一个远端写入口
// 只要过这一关就不会漏。这一层只能判字面路径：真实路径要在 SSH 另一头解析，工作
// 区内的符号链接能把干净的字面路径接到 .git 上（`ln -s .git gh` 之后写
// `gh/hooks/pre-commit`），那一步由 helper 用远端事实复判（op_write 的
// contains_vcs）。
func remoteVCSMetadataWrite(cleanPath string) error {
	if blocked, reason := pathutil.VCSMetadataReason(cleanPath); blocked {
		return codedToolError("E_PROTECTED_PATH", errors.New(reason))
	}
	return nil
}

// remoteCommandVCSMetadataTarget 报出远端命令行里第一个落在版本控制元数据内的字面
// 写入目标，workingDir 用来解析相对目标（远端命令的 cwd）。与本地
// firstVCSMetadataMutationTarget 的差别只有一处：本地还会用 evalExistingPrefix 把
// 符号链接解析后复判，远端解析不了，那一步留给 helper 的 check_write_targets。
func remoteCommandVCSMetadataTarget(commandLine, workingDir string) string {
	for _, target := range command.LiteralWriteTargets(commandLine) {
		p, ok := command.ResolveCommandLiteralPath(target.Path, workingDir)
		if !ok {
			continue
		}
		if blocked, _ := pathutil.VCSMetadataReason(p); blocked {
			return filepath.ToSlash(p)
		}
	}
	return ""
}

// toolSessionID 取出工具调用所属的会话 ID；后台任务与子代理调用可能没有会话。
func toolSessionID(ctx context.Context) string {
	if meta, ok := ctx.Value(toolExecutionMetaContextKey{}).(toolExecutionMeta); ok {
		return meta.sessionID
	}
	return ""
}

// errRemoteScriptTimeout 标记一次远端脚本调用在超时预算内没能返回，由调用方渲染
// 成给模型看的提示。
var errRemoteScriptTimeout = errors.New("remote ssh timed out")

// runRemoteScriptOnce 执行一次远端 Python helper 调用。超时预算按单次调用计算：
// 指纹记录被替换后的重试要重新拿到完整预算，不能共用上一次已经耗掉的额度。
func runRemoteScriptOnce(ctx context.Context, sshConfig sshclient.Config, script string, timeout time.Duration) (sshclient.Result, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	const remotePythonCommand = "sh -c 'command -v python3 >/dev/null 2>&1 && exec python3 - ; command -v python2 >/dev/null 2>&1 && exec python2 - ; exec python -'"
	result, err := sshclient.Run(runCtx, sshConfig, remotePythonCommand, strings.NewReader(script))
	if runCtx.Err() == context.DeadlineExceeded {
		return result, fmt.Errorf("%w after %s", errRemoteScriptTimeout, timeout)
	}
	return result, err
}

// refreshHostKeyAndRetry 执行一次远端调用；若记录中的主机指纹与服务器实际提供的
// 不一致，直接换成本次收到的公钥并重试一次。策略口径是「一律不询问」：主代理、
// 子代理、后台任务、本地 API 走同一条路，等价于 OpenSSH 的
// StrictHostKeyChecking=no（替换前会把原记录备份为 <known_hosts>.old 供人工核对）。
// 只重试一次：第二次仍对不上说明这不是一次正常的服务器换钥，照实上报而不是死循环。
func refreshHostKeyAndRetry[T any](attempt func() (T, error)) (T, error) {
	out, err := attempt()
	var mismatch *sshclient.HostKeyMismatchError
	if !errors.As(err, &mismatch) {
		return out, err
	}
	if replaceErr := sshclient.ReplaceHostKey(mismatch.KnownHostsPath, mismatch.Address, mismatch.NewKey); replaceErr != nil {
		return out, fmt.Errorf("could not update the saved host key for %s: %w", mismatch.Address, replaceErr)
	}
	return attempt()
}

func (a *App) invokeRemotePython(ctx context.Context, rt remoteTarget, payload map[string]any, timeout time.Duration, out any) error {
	script, err := buildRemoteScript(payload)
	if err != nil {
		return err
	}
	entry, _ := a.sshCredentials.lookup(sshCredentialKey(rt.Host, rt.Port))
	sshConfig := a.remoteSSHConfig(rt, entry)
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	// 指纹不一致由 refreshHostKeyAndRetry 直接换记录并重连，这里拿到的要么是成功结果，
	// 要么是分类上报的失败。
	result, err := refreshHostKeyAndRetry(func() (sshclient.Result, error) {
		return runRemoteScriptOnce(ctx, sshConfig, script, timeout)
	})
	stdout := result.Stdout
	stderr := result.Stderr
	if errors.Is(err, errRemoteScriptTimeout) {
		return err
	}
	var mismatch *sshclient.HostKeyMismatchError
	if errors.As(err, &mismatch) {
		// 只有在替换记录成功后重连仍然对不上时才会走到这里（记录文件被并发改写、
		// 服务器换了另一把钥匙等）：原样上报新旧指纹，交由人工判断。
		return fmt.Errorf("ssh %s failed: %w", rt.Host, mismatch)
	}
	if errors.Is(err, sshclient.ErrOutputTooLarge) {
		return fmt.Errorf("ssh %s failed: remote helper output exceeded the capture limit: %w", rt.Host, err)
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	if errors.Is(err, sshclient.ErrAuthentication) {
		return fmt.Errorf("ssh %s failed: authentication failed. Check the SSH cluster username, password, private key, key passphrase, and server authorized_keys: %w", rt.Host, err)
	}
	if err != nil {
		msg := strings.TrimSpace(string(stderr))
		if msg == "" {
			msg = strings.TrimSpace(string(stdout))
		}
		if msg == "" {
			msg = err.Error()
		}
		// 远端缺解释器时给出可操作诊断：常见形态是 bash 的 "python:
		// command not found" 或 ash/dash 的 "python: not found"，透传原始
		// ssh 错误对模型不可辨因。
		if strings.Contains(msg, "python3: command not found") || strings.Contains(msg, "python3: not found") ||
			strings.Contains(msg, "python2: command not found") || strings.Contains(msg, "python2: not found") ||
			strings.Contains(msg, "python: command not found") || strings.Contains(msg, "python: not found") {
			return fmt.Errorf("remote host %s has no python3/python2/python in PATH; remote_* tools need a python interpreter on the remote side (install python3, python2 or python, or add it to PATH): %s", rt.Host, msg)
		}
		return fmt.Errorf("ssh %s failed: %s", rt.Host, msg)
	}
	// 远程命令的输出会被原样嵌在 ok() 的 JSON output 字段里，如果命令
	// 恰好输出了 marker 字符串，marker 会同时出现在 JSON 内部。因此不能
	// 简单用 LastIndex 取最后一个匹配（会命中 JSON 内部），需要从后往前
	// 逐个尝试：真正 ok() 的 JSON 必然合法，误命中的候选必然解析失败。
	markerBytes := []byte(remotePythonMarker)
	stdoutBytes := stdout
	var resp remotePythonResponse
	decoded := false
	searchFrom := len(stdoutBytes)
	for {
		idx := bytes.LastIndex(stdoutBytes[:searchFrom], markerBytes)
		if idx < 0 {
			break
		}
		candidate := strings.TrimRight(string(stdoutBytes[idx+len(markerBytes):]), "\r\n")
		if err := json.Unmarshal([]byte(candidate), &resp); err == nil {
			decoded = true
			break
		}
		searchFrom = idx
	}
	if !decoded {
		return fmt.Errorf("remote helper returned no JSON result; stderr: %s", strings.TrimSpace(string(stderr)))
	}
	if !resp.OK {
		return remoteHelperError(resp)
	}
	if out != nil {
		if err := json.Unmarshal(resp.Data, out); err != nil {
			return fmt.Errorf("decode remote helper data: %w", err)
		}
	}
	return nil
}

func decodeRemoteRawFile(data struct {
	Path       string `json:"path"`
	DataBase64 string `json:"dataBase64"`
	Size       int64  `json:"size"`
	Mode       int    `json:"mode"`
	ModTime    string `json:"modTime"`
}) (remoteRawFile, error) {
	raw, err := base64.StdEncoding.DecodeString(data.DataBase64)
	if err != nil {
		return remoteRawFile{}, err
	}
	// The same transcode boundary as the local read pipeline: UTF-16 LE/BE
	// (with or without a BOM) is accepted and converted to UTF-8, while real
	// binary and malformed/non-text UTF-16 still fail. Version tokens and
	// edit write-back are therefore based on the same bytes as local files.
	decoded, err := read.DecodeTextBytes(raw)
	if err != nil {
		return remoteRawFile{}, err
	}
	_, ending, _ := normalizeText(decoded)
	return remoteRawFile{Path: data.Path, Data: decoded, Size: data.Size, Mode: data.Mode, ModTime: data.ModTime, LineEnding: ending}, nil
}

func (a *App) remoteReadRaw(ctx context.Context, target, relPath string) (remoteTarget, remoteRawFile, error) {
	rt, err := a.resolveAndAuthorizeRemoteTarget(ctx, target)
	if err != nil {
		return remoteTarget{}, remoteRawFile{}, err
	}
	return a.remoteReadRawOne(ctx, rt, relPath)
}

// remoteReadBatchItem 是 read_batch 结果槽的传输形状（纯字节搬运；转码/
// 版本/预览语义全部在 Go 侧复用本地 read 管线，不在这里实现）。
type remoteReadBatchItem struct {
	Path string `json:"path"`
	OK   bool   `json:"ok"`
	Data struct {
		Path       string `json:"path"`
		DataBase64 string `json:"dataBase64"`
		Size       int64  `json:"size"`
		Mode       int    `json:"mode"`
		ModTime    string `json:"modTime"`
	} `json:"data"`
	Error string `json:"error"`
}

// remoteReadRawBatch 在单个 ssh 会话里读多个文件（read_batch op）：每个结
// 果槽独立 try，单文件失败只写入自己的 error 字段，与本地批量 read 的隔
// 离契约一致；连接级失败由调用方把同一错误填进所有未填充槽。返回的
// remaining 是总字节预算装不下而未读的路径尾部，供下一轮会话继续。
func (a *App) remoteReadRawBatch(ctx context.Context, rt remoteTarget, paths []string) ([]remoteReadBatchItem, []string, error) {
	var resp struct {
		Files     []remoteReadBatchItem `json:"files"`
		Remaining []string              `json:"remaining"`
	}
	err := a.invokeRemotePython(ctx, rt, remotePayload(rt, "read_batch", map[string]any{
		"paths":      paths,
		"maxBytes":   maxReadFileBytes,
		"totalBytes": maxRemoteReadBatchBytes,
	}), 300*time.Second, &resp)
	if err != nil {
		return nil, nil, err
	}
	return resp.Files, resp.Remaining, nil
}

// remoteReadRawOne 是单文件读的会话实现（edit 的读-改-写与单文件批量读
// 共用）；路径校验失败时直接返回，不起 ssh 会话，保持原错误语义。
func (a *App) remoteReadRawOne(ctx context.Context, rt remoteTarget, relPath string) (remoteTarget, remoteRawFile, error) {
	cleanPath, err := validateRemoteWorkspacePath(relPath, rt.WorkspaceRoot, false)
	if err != nil {
		return remoteTarget{}, remoteRawFile{}, err
	}
	var rawResp struct {
		Path       string `json:"path"`
		DataBase64 string `json:"dataBase64"`
		Size       int64  `json:"size"`
		Mode       int    `json:"mode"`
		ModTime    string `json:"modTime"`
	}
	err = a.invokeRemotePython(ctx, rt, remotePayload(rt, "read", map[string]any{"path": cleanPath, "maxBytes": maxReadFileBytes}), 60*time.Second, &rawResp)
	if err != nil {
		return remoteTarget{}, remoteRawFile{}, err
	}
	file, err := decodeRemoteRawFile(rawResp)
	if err != nil {
		return remoteTarget{}, remoteRawFile{}, err
	}
	return rt, file, nil
}

func (a *App) remoteWriteRaw(ctx context.Context, rt remoteTarget, relPath string, data []byte, overwrite, mkdirs bool) ([]string, error) {
	cleanPath, err := validateRemoteWorkspacePath(relPath, rt.WorkspaceRoot, false)
	if err != nil {
		return nil, err
	}
	// 远端写入（create / edit）与本地同一条围栏：.git/hooks 里的文件会在下次 git
	// 命令时执行，覆盖 .git/index 或 ref 会损坏仓库。
	if err := remoteVCSMetadataWrite(cleanPath); err != nil {
		return nil, err
	}
	var outcome struct {
		CreatedDirs []string `json:"createdDirs"`
	}
	err = a.invokeRemotePython(ctx, rt, remotePayload(rt, "write", map[string]any{
		"path":       cleanPath,
		"dataBase64": base64.StdEncoding.EncodeToString(data),
		"overwrite":  overwrite,
		"mkdirs":     mkdirs,
	}), 60*time.Second, &outcome)
	if err != nil {
		return nil, err
	}
	return outcome.CreatedDirs, nil
}

func (a *App) remoteReadFile(ctx context.Context, req RemoteReadFileRequest) (BatchReadResult, error) {
	if strings.TrimSpace(req.Target) == "" {
		return BatchReadResult{}, errors.New("target is required")
	}
	fileRequests := req.Files
	if len(fileRequests) == 0 && strings.TrimSpace(req.Path) != "" {
		fileRequests = []BatchReadFileRequest{
			{
				Path:      req.Path,
				StartLine: req.StartLine,
				EndLine:   req.EndLine,
			},
		}
	}
	if len(fileRequests) == 0 {
		return BatchReadResult{}, errors.New("remote_read requires at least one file in files")
	}
	if len(fileRequests) > 20 {
		return BatchReadResult{}, errors.New("too many files; max 20 per batch")
	}
	rt, err := a.resolveAndAuthorizeRemoteTarget(ctx, req.Target)
	if err != nil {
		return BatchReadResult{}, err
	}
	// 单文件请求走 remoteReadRawOne（remoteEdit 读-改-写与 remoteReadRaw
	// 共用该实现），保持原错误语义；多文件改用 read_batch 单会话批量读，
	// 预算装不下的尾部路径自动排入下一轮会话。文件不大时 20 个文件只需
	// 一轮 ssh 握手（旧的逐文件串行是 20 次完整握手）；文件很大时轮次随
	// 预算上升，最坏每轮一个文件、与旧实现持平。
	if len(fileRequests) == 1 {
		_, rawFile, err := a.remoteReadRawOne(ctx, rt, fileRequests[0].Path)
		if err != nil {
			return BatchReadResult{Files: []BatchReadResultItem{{Path: fileRequests[0].Path, Error: err.Error()}}}, nil
		}
		item, itemErr := buildRemoteReadResultItem(rawFile, fileRequests[0])
		if itemErr != nil {
			return BatchReadResult{Files: []BatchReadResultItem{{Path: fileRequests[0].Path, Error: itemErr.Error()}}}, nil
		}
		return BatchReadResult{Files: []BatchReadResultItem{item}}, nil
	}
	results := make([]BatchReadResultItem, len(fileRequests))
	// 先逐槽位做与单文件路径相同的清洗校验（root 内绝对路径 rebase 成相
	// 对拼写），非法槽位直接落错误，不进会话；再按清洗后路径去重排序，
	// 同一物理文件只传一次。重复路径的每个槽位各自渲染自己的行区间，
	// 与旧逐文件实现语义一致。
	slotsOf := make(map[string][]int, len(fileRequests))
	pendingSet := make(map[string]bool, len(fileRequests))
	pending := make([]string, 0, len(fileRequests))
	for i, f := range fileRequests {
		clean, err := validateRemoteWorkspacePath(f.Path, rt.WorkspaceRoot, false)
		if err != nil {
			results[i] = BatchReadResultItem{Path: f.Path, Error: err.Error()}
			continue
		}
		if !pendingSet[clean] {
			pendingSet[clean] = true
			pending = append(pending, clean)
		}
		slotsOf[clean] = append(slotsOf[clean], i)
	}
	for len(pending) > 0 {
		var items []remoteReadBatchItem
		var remaining []string
		var batchErr error
		if a.remoteReadBatchFn != nil {
			items, remaining, batchErr = a.remoteReadBatchFn(ctx, rt, pending)
		} else {
			items, remaining, batchErr = a.remoteReadRawBatch(ctx, rt, pending)
		}
		if batchErr != nil {
			// 连接级失败：同一错误填进所有未填充槽，错误隔离契约与旧
			// 逐文件实现一致。
			for _, p := range pending {
				for _, slot := range slotsOf[p] {
					results[slot] = BatchReadResultItem{Path: fileRequests[slot].Path, Error: batchErr.Error()}
				}
			}
			break
		}
		for _, it := range items {
			slots, ok := slotsOf[it.Path]
			if !ok {
				continue
			}
			if !it.OK {
				for _, slot := range slots {
					results[slot] = BatchReadResultItem{Path: fileRequests[slot].Path, Error: it.Error}
				}
				continue
			}
			rawFile, decErr := decodeRemoteRawFile(it.Data)
			for _, slot := range slots {
				if decErr != nil {
					results[slot] = BatchReadResultItem{Path: fileRequests[slot].Path, Error: decErr.Error()}
					continue
				}
				item, itemErr := buildRemoteReadResultItem(rawFile, fileRequests[slot])
				if itemErr != nil {
					results[slot] = BatchReadResultItem{Path: fileRequests[slot].Path, Error: itemErr.Error()}
					continue
				}
				results[slot] = item
			}
		}
		if len(remaining) == 0 {
			break
		}
		pending = remaining
	}
	return BatchReadResult{Files: results}, nil
}

// remoteReadFileCached 给 remote_read 接上会话级读缓存：同一会话里第二次读同一
// 台机器上同一范围且内容未变的文件时，正文不再重复投递，只回版本令牌与 reused
// 说明。模型侧的渲染本来就与本地 read 共用一条管线（见 infra_result.go 的
// case "read", "remote_read"），这里补的是数据层：命中判定、被清空的字段与
// Reused 标记全部复用 sessionReadCache.dedupeResult，与本地 read 是同一份实现。
func (a *App) remoteReadFileCached(ctx context.Context, req RemoteReadFileRequest) (BatchReadResult, error) {
	result, err := a.remoteReadFile(ctx, req)
	if err != nil {
		return result, err
	}
	if cache, ok := ctx.Value(sessionReadCacheContextKey{}).(*sessionReadCache); ok {
		// 远端条目的路径已是目标机工作区内清洗过的相对路径（绝对写法会被
		// rebase），键里不再带 target：两台机器上的同名文件只有内容逐字节相
		// 同时才会互相命中，而那时省略的正是模型已经收到过的内容。
		cache.dedupeResult(&result, func(item *BatchReadResultItem) string { return item.Path })
	}
	return result, nil
}

// buildRemoteReadResultItem 把远端原始字节转换成与本地 read 同形状的预览
// 结果：转码/版本令牌/行号预览/截断标记全部复用本地 read 的同一套管
// 线，远程与本地语义不分离。
func buildRemoteReadResultItem(rawFile remoteRawFile, f BatchReadFileRequest) (BatchReadResultItem, error) {
	text, ending, _ := normalizeText(rawFile.Data)
	sha256Hex, version := hashBytesAndVersion(rawFile.Data)
	_ = sha256Hex
	previewStart, rangeErr := resolveReadStartLine(f.StartLine, f.EndLine, f.TailLines)
	if rangeErr != nil {
		return BatchReadResultItem{}, rangeErr
	}
	preview, previewErr := formatLineNumberReadPreviewRangeWithBudget(text, readRangeRequest{
		StartLine: previewStart,
		EndLine:   f.EndLine,
	}, maxReadPreviewBytes)
	if previewErr != nil {
		return BatchReadResultItem{}, previewErr
	}
	return BatchReadResultItem{
		// Req 提供读缓存的键里的行区间（见 sessionReadCache.dedupeResult）：
		// 缺了它，同一文件的不同区间会共用一把键互相覆盖，去重直接失效。形状
		// 与本地 read 的条目一致（previewStart 已折叠 tailLines）。
		Req: ReadFileRequest{
			Path:      f.Path,
			StartLine: previewStart,
			EndLine:   f.EndLine,
		},
		Path:                  rawFile.Path,
		Content:               preview.Content,
		Kind:                  "text",
		ContentFormat:         "line_numbers",
		Editable:              true,
		StartLine:             preview.StartLine,
		EndLine:               preview.EndLine,
		NextStartLine:         preview.NextStartLine,
		TotalLines:            preview.TotalLines,
		Version:               version,
		Size:                  rawFile.Size,
		LineEnding:            ending,
		Truncated:             preview.Truncated,
		TruncatedLines:        preview.TruncatedLines,
		TruncatedLinesOmitted: preview.TruncatedLinesOmitted,
		RangeStatus:           preview.RangeStatus,
		EmptyRange:            preview.EmptyRange,
	}, nil
}

// remoteEdit applies the flat single-file remote edit contract. The result is
// the same MultiEditResult shape local edit returns, with exactly one file.
// A single-file call needs no backup/rollback: remoteEditOne validates the
// version, applies the whole batch in memory, and writes once, so the file is
// either fully updated or untouched.
func (a *App) remoteEdit(ctx context.Context, req RemoteEditRequest) (MultiEditResult, error) {
	if strings.TrimSpace(req.Target) == "" {
		return MultiEditResult{}, errors.New("target is required")
	}
	file := req.file()
	if err := validateModelEditToolRequest([]FileTextEdits{file}); err != nil {
		return MultiEditResult{}, err
	}
	rt, original, err := a.remoteReadRaw(ctx, req.Target, file.Path)
	if err != nil {
		return MultiEditResult{}, err
	}
	edited, err := a.remoteEditOne(ctx, rt, file, original)
	if err != nil {
		return MultiEditResult{}, err
	}
	result := MultiEditResult{Files: []EditResult{edited}, FileCount: 1}
	result.Replacements = edited.Replacements
	result.AddedLines = edited.AddedLines
	result.RemovedLines = edited.RemovedLines
	for _, warning := range edited.Warnings {
		result.Warnings = append(result.Warnings, edited.Path+": "+warning)
	}
	result.Summary = fmt.Sprintf("updated %d file(s) with %d replacement(s)", result.FileCount, result.Replacements)
	if result.Replacements == 0 {
		result.Summary = fmt.Sprintf("no content changes needed in %d file(s)", result.FileCount)
	}
	if edited.Diff != "" {
		result.Diff = "### " + edited.Path + "\n" + edited.Diff
	}
	return result, nil
}

func (a *App) remoteEditOne(ctx context.Context, rt remoteTarget, req FileTextEdits, file remoteRawFile) (EditResult, error) {
	beforeHash, beforeVersion := hashBytesAndVersion(file.Data)
	if !strings.EqualFold(req.Version, beforeVersion) {
		return EditResult{}, codedToolError("E_VERSION_MISMATCH", fmt.Errorf("remote file changed: expected version %s, current %s. Re-read before editing", req.Version, beforeVersion))
	}
	text, ending, hadBOM := normalizeText(file.Data)
	result, replacements, err := edit.ApplyBatchTextChanges(text, toEditChanges(req.Changes))
	if err != nil {
		return EditResult{}, err
	}
	after := encodeText(result.Content, ending, hadBOM)
	afterHash, afterVersion := hashBytesAndVersion(after)
	// An all-no-op batch (every change ignored as identical) succeeds without
	// writing, mirroring the local edit contract instead of failing E_NOOP.
	noop := bytes.Equal(file.Data, after)
	if !noop {
		if _, err := a.remoteWriteRaw(ctx, rt, req.Path, after, true, true); err != nil {
			return EditResult{}, err
		}
	}
	diff, removed, added := edit.GenerateEditDiffPreviewWithCounts(text, result.Content, maxToolOutput)
	classification := "edit"
	if noop {
		classification = "noop"
	} else if len(after) > len(file.Data) {
		classification = "addition"
	} else if len(after) < len(file.Data) {
		classification = "deletion"
	}
	return EditResult{
		Path:              file.Path,
		BeforeSHA256:      beforeHash,
		AfterSHA256:       afterHash,
		BeforeVersion:     beforeVersion,
		Version:           afterVersion,
		BeforeBytes:       len(file.Data),
		AfterBytes:        len(after),
		Replacements:      replacements,
		AddedLines:        added,
		RemovedLines:      removed,
		LineEnding:        ending,
		Summary:           fmt.Sprintf("%s updated on %s: %d -> %d bytes", file.Path, rt.Host, len(file.Data), len(after)),
		Diff:              diff,
		FirstChanged:      result.FirstChangedLine,
		LastChanged:       result.LastChangedLine,
		Warnings:          result.Warnings,
		Classification:    classification,
		ChangedLinesBlock: edit.BuildLineNumberContextBlockBounded(result.Content, result.FirstChangedLine, result.LastChangedLine),
	}, nil
}

// remoteStat is the metadata-only view of a remote path (the helper's stat op).
// Existence cannot be inferred from a read: read_raw_file refuses files above
// maxReadFileBytes and directories outright, so a failed read says nothing about
// whether the target is there.
type remoteStat struct {
	Exists bool
	IsDir  bool
	Size   int64
}

func (a *App) remoteStatPath(ctx context.Context, rt remoteTarget, relPath string) (remoteStat, error) {
	var resp struct {
		Exists bool  `json:"exists"`
		IsDir  bool  `json:"isDir"`
		Size   int64 `json:"size"`
	}
	err := a.invokeRemotePython(ctx, rt, remotePayload(rt, "stat", map[string]any{"path": relPath}), 60*time.Second, &resp)
	if err != nil {
		return remoteStat{}, err
	}
	return remoteStat{Exists: resp.Exists, IsDir: resp.IsDir, Size: resp.Size}, nil
}

func (a *App) remoteCreateFile(ctx context.Context, req RemoteCreateFileRequest) (EditResult, error) {
	rt, err := a.resolveAndAuthorizeRemoteTarget(ctx, req.Target)
	if err != nil {
		return EditResult{}, err
	}
	cleanPath, err := validateRemoteWorkspacePath(req.Path, rt.WorkspaceRoot, false)
	if err != nil {
		return EditResult{}, err
	}
	// Existence comes from the helper's stat op, not from the pre-read:
	// read_raw_file refuses files above maxReadFileBytes and directories outright,
	// so a failed read must not report an overwritten file as created.
	stat, statErr := a.remoteStatPath(ctx, rt, cleanPath)
	if statErr != nil {
		return EditResult{}, statErr
	}
	if stat.IsDir {
		return EditResult{}, codedToolError("E_BAD_PATH", fmt.Errorf("%s is a directory on %s; remote_create_file writes files only", cleanPath, rt.Host))
	}
	before := []byte{}
	beforeHash := ""
	beforeVersion := ""
	// Best-effort read supplies the diff and version; stat determines existence.
	if stat.Exists {
		if _, existing, readErr := a.remoteReadRaw(ctx, req.Target, cleanPath); readErr == nil {
			before = existing.Data
			beforeHash, beforeVersion = hashBytesAndVersion(existing.Data)
		}
	}
	content, ending, hadBOM := normalizeText([]byte(req.Content))
	encoded := encodeText(content, ending, hadBOM)

	createdDirs, err := a.remoteWriteRaw(ctx, rt, cleanPath, encoded, req.Overwrite, true)
	if err != nil {
		return EditResult{}, err
	}
	result := makeEditResult(cleanPath, beforeHash, beforeVersion, before, encoded, ending, 1, string(before), content)
	created := !stat.Exists
	result.Created = &created
	if len(createdDirs) > 0 {
		result.CreatedDirs = createdDirs
	}
	if created {
		result.Summary = fmt.Sprintf("%s created: %d bytes", filepath.ToSlash(req.Path), len(encoded))
	}
	return result, nil
}

// remoteDeletePath 删除远端一次调用里的全部路径，与本地 delete 同一套语义：
// Go 侧先判路径形态（相对性、越界、工作区根、VCS 元数据），并把这一串路径归一成
// 「去重 + 深的先删」的执行计划（planDeleteExecution）；只有远端才知道的事实
// （是否目录、是否系统敏感目标）由
// helper 在动手之前逐条判完——任一条不通过，整批一条都不删。
// 执行阶段仍是逐条隔离：单条失败只写自己的结果槽；本来就不存在的路径报 absent
// 槽（既不成功也不失败），同批其它路径照删。
func (a *App) remoteDeletePath(ctx context.Context, req RemoteDeletePathRequest) (DeletePathsResult, error) {
	rt, err := a.resolveAndAuthorizeRemoteTarget(ctx, req.Target)
	if err != nil {
		return DeletePathsResult{}, err
	}
	candidates, err := resolveDeletePathList(req.Paths)
	if err != nil {
		return DeletePathsResult{}, err
	}
	clean := make([]string, 0, len(candidates))
	targets := make([]fileMutationTarget, 0, len(candidates))
	for _, candidate := range candidates {
		// allowRoot=true maps the workspace root to "." so the dedicated refusal
		// below reports the root, instead of the generic "path is required" that
		// allowRoot=false raises first (which made that branch dead code).
		cleanPath, err := validateRemoteWorkspacePath(candidate, rt.WorkspaceRoot, true)
		if err != nil {
			return DeletePathsResult{}, err
		}
		if cleanPath == "." {
			return DeletePathsResult{}, codedToolError("E_DELETE_BLOCKED", errors.New("refusing to delete remote workspace root"))
		}
		for _, part := range strings.Split(cleanPath, "/") {
			if part == ".git" || part == ".svn" || part == ".hg" {
				return DeletePathsResult{}, codedToolError("E_DELETE_BLOCKED", fmt.Errorf("refusing to delete VCS metadata (%s in %s)", part, cleanPath))
			}
		}
		clean = append(clean, cleanPath)
		targets = append(targets, fileMutationTarget{remoteMutationKey(req.Target, cleanPath), cleanPath})
	}
	// 重复与包含不再整批拒：只把代表项发给 helper（同一身份删一次），并按计划顺序送——
	// 深的先删，父目录与它里面的路径同时列出时两条都能真删掉，而不是让结果取决于顺序。
	plan := planDeleteExecution(targets)
	execPaths := make([]string, 0, len(plan.order))
	for _, rep := range plan.order {
		execPaths = append(execPaths, clean[rep])
	}

	var resp struct {
		Paths []struct {
			Path         string `json:"path"`
			OK           bool   `json:"ok"`
			Absent       bool   `json:"absent"`
			Error        string `json:"error"`
			ResolvedPath string `json:"resolvedPath"`
		} `json:"paths"`
	}
	// 一批里可能包含递归删除的大目录（构建产物、依赖目录），单路径时代的 60s
	// 预算会误报超时；按条数放宽并封顶，一次调用不会无限期挂着。
	timeout := time.Duration(60*len(execPaths)) * time.Second
	if timeout > 5*time.Minute {
		timeout = 5 * time.Minute
	}
	if err := a.invokeRemotePython(ctx, rt, remotePayload(rt, "delete_batch", map[string]any{"paths": execPaths, "recursive": req.Recursive}), timeout, &resp); err != nil {
		return DeletePathsResult{}, err
	}
	// 槽位与发出去的路径一一对应：数目不对就是 helper 契约破了，宁可报错也不要拿零值
	// 槽位当「删失败」糊弄过去。
	if len(resp.Paths) != len(execPaths) {
		return DeletePathsResult{}, fmt.Errorf("remote delete helper returned %d result slots for %d paths", len(resp.Paths), len(execPaths))
	}
	executed := make([]DeleteResult, len(clean))
	for i, item := range resp.Paths {
		slot := DeleteResult{
			Deleted:      item.Path,
			Path:         item.Path,
			ResolvedPath: item.ResolvedPath,
			OK:           item.OK,
			Absent:       item.Absent,
			Recursive:    req.Recursive,
		}
		if !item.OK {
			slot.Error = item.Error
		}
		executed[plan.order[i]] = slot
	}
	result := DeletePathsResult{}
	applyDeletePlanSlots(&result, plan, clean, executed)
	return result, nil
}

func (a *App) remoteRunCommand(ctx context.Context, req RemoteRunCommandRequest) (CommandResult, error) {
	if strings.TrimSpace(req.Command) == "" {
		return CommandResult{}, errors.New("command is required")
	}
	if command.ContainsExplicitDeleteCommand(req.Command) && !command.IsAllowedDeleteContext(req.Command) {
		return CommandResult{}, codedToolError("E_COMMAND_BLOCKED", fmt.Errorf("remote_run_command refuses explicit deletion commands. Use remote_delete_path for deletion.\n被拦截的命令: %s", req.Command))
	}
	if err := validateRemoteCommandSafety(req.Command); err != nil {
		return CommandResult{}, err
	}
	rt, err := a.resolveAndAuthorizeRemoteTarget(ctx, req.Target)
	if err != nil {
		return CommandResult{}, err
	}
	// cwd 先校验并归一；算出来的
	// 远端绝对路径同时是下面版本控制元数据判定的解析基准。
	cwd, err := validateRemoteWorkspacePath(req.Cwd, rt.WorkspaceRoot, true)
	if err != nil {
		return CommandResult{}, err
	}
	// 版本控制元数据：本地那条判定即便在内核接管、围栏 stand down 时也保留，远端
	// 没有内核兜底，更必须有等价的一份，否则远端命令能写 .git/hooks（下次 git 即
	// 执行代码）。字面目标里出现 .git 段即拒；解析后的复判在 helper 侧。
	if risk := remoteCommandVCSMetadataTarget(req.Command, path.Join(rt.WorkspaceRoot, cwd)); risk != "" {
		return CommandResult{}, codedToolError("E_PROTECTED_PATH", fmt.Errorf("安全围栏已拦截：远端命令的写入目标是版本控制元数据内的路径。\n原因：创建或覆盖 .git/.svn/.hg 的元数据会损坏仓库，写入 hooks 更会在下次 git 命令时执行代码。\n检测到的目标：%s\n处理方式：版本控制状态请手动在服务器上变更。\n被拦截的命令：%s", risk, req.Command))
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = defaultShellLimit
	}
	if timeout > 600 {
		timeout = 600
	}
	shell := strings.TrimSpace(req.Shell)
	if shell != "" {
		allowed := false
		for _, s := range []string{"/bin/bash", "/bin/sh", "/usr/bin/bash", "/usr/bin/sh", "/usr/bin/zsh", "/bin/zsh"} {
			if shell == s {
				allowed = true
				break
			}
		}
		if !allowed {
			return CommandResult{}, codedToolError("E_BAD_SHELL", fmt.Errorf("unsupported shell %q: only bash, sh, zsh are allowed", shell))
		}
	}
	// 镜像本地 command 的 E_PATH_OUTSIDE 检查：只把"可静态解析的字面写
	// 目标"传给远端做事实检查；动态目标（变量/glob/命令替换）与 null 设备
	// 在本地同样放行，因此不传。
	writeTargets := remoteWriteTargets(req.Command)
	var result CommandResult
	err = a.invokeRemotePython(ctx, rt, remotePayload(rt, "run", map[string]any{
		"command":        req.Command,
		"cwd":            cwd,
		"timeoutSeconds": timeout,
		"shell":          shell,
		"maxOutput":      maxToolOutput,
		"targets":        writeTargets,
	}), time.Duration(timeout+20)*time.Second, &result)
	return result, err
}

const remoteTransferMarker = "ALLY_REMOTE_TRANSFER_JSON:"

// Transfer mode uses the same helper, with tar on stdin/stdout and JSON on stderr.
var remoteTransferPythonScript = strings.Replace(strings.Replace(remotePythonScript, "TRANSFER_MODE = False", "TRANSFER_MODE = True", 1), "__DELETE_SENSITIVE_TARGETS__", "[]", 1)

type RemoteTransferRequest struct {
	Target     string `json:"target"`
	Action     string `json:"action"` // "upload" or "download"
	LocalPath  string `json:"localPath"`
	RemotePath string `json:"remotePath"`
	Overwrite  bool   `json:"overwrite,omitempty"`
}

type RemoteTransferResult struct {
	OK         bool   `json:"ok"`
	Action     string `json:"action"`
	Target     string `json:"target"`
	LocalPath  string `json:"localPath"`
	RemotePath string `json:"remotePath"`
	TotalBytes int64  `json:"totalBytes"`
	FileCount  int    `json:"fileCount"`
	DirCount   int    `json:"dirCount,omitempty"`
	Summary    string `json:"summary"`
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
	rest := string(stderr[idx+len(markerBytes):])
	line := rest
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		line = rest[:nl]
	}
	candidate := strings.TrimSpace(line)
	var resp struct {
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data,omitempty"`
		Error string          `json:"error,omitempty"`
	}
	if err := json.Unmarshal([]byte(candidate), &resp); err != nil {
		return fmt.Errorf("parse remote transfer response: %w", err)
	}
	if !resp.OK {
		if strings.HasPrefix(resp.Error, "E_PROTECTED_PATH:") {
			return codedToolError("E_PROTECTED_PATH", errors.New(strings.TrimSpace(strings.TrimPrefix(resp.Error, "E_PROTECTED_PATH:"))))
		}
		if strings.HasPrefix(resp.Error, "Tar-Slip violation:") {
			return codedToolError("E_PATH_OUTSIDE", errors.New(resp.Error))
		}
		return errors.New(resp.Error)
	}
	var stats struct {
		TotalBytes *int64 `json:"totalBytes"`
		FileCount  *int   `json:"fileCount"`
		DirCount   *int   `json:"dirCount"`
	}
	if err := json.Unmarshal(resp.Data, &stats); err != nil {
		return fmt.Errorf("invalid remote transfer statistics: %w", err)
	}
	if stats.TotalBytes == nil || stats.FileCount == nil || stats.DirCount == nil || *stats.TotalBytes < 0 || *stats.FileCount < 0 || *stats.DirCount < 0 {
		return errors.New("remote transfer response has missing or negative statistics")
	}
	if out != nil {
		return json.Unmarshal(resp.Data, out)
	}
	return nil
}

func parseRemoteTransferError(host string, stderr []byte, err error) error {
	msg := strings.TrimSpace(string(stderr))
	if strings.Contains(msg, remoteTransferMarker) {
		if markerErr := decodeRemoteTransferMarker(stderr, nil); markerErr != nil {
			return markerErr
		}
		return fmt.Errorf("ssh transfer %s failed: %w", host, err)
	}
	if strings.Contains(msg, "python3: command not found") || strings.Contains(msg, "python3: not found") ||
		strings.Contains(msg, "python2: command not found") || strings.Contains(msg, "python2: not found") ||
		strings.Contains(msg, "python: command not found") || strings.Contains(msg, "python: not found") {
		return fmt.Errorf("remote host %s has no python interpreter; remote_transfer needs python (install python3 or python, or add to PATH)", host)
	}
	if msg != "" {
		return fmt.Errorf("ssh transfer %s failed: %s", host, msg)
	}
	return fmt.Errorf("ssh transfer %s failed: %w", host, err)
}

func (a *App) executeRemoteTransfer(ctx context.Context, req RemoteTransferRequest) (RemoteTransferResult, error) {
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action != "upload" && action != "download" {
		return RemoteTransferResult{}, codedToolError("E_BAD_ARGS", fmt.Errorf("action must be 'upload' or 'download', got %q", req.Action))
	}
	if strings.TrimSpace(req.Target) == "" {
		return RemoteTransferResult{}, codedToolError("E_BAD_ARGS", errors.New("target is required"))
	}
	if strings.TrimSpace(req.LocalPath) == "" {
		return RemoteTransferResult{}, codedToolError("E_BAD_ARGS", errors.New("localPath is required"))
	}
	if strings.TrimSpace(req.RemotePath) == "" {
		return RemoteTransferResult{}, codedToolError("E_BAD_ARGS", errors.New("remotePath is required"))
	}

	// 1. Resolve target, workspace authorization and credentials
	rt, err := a.resolveAndAuthorizeRemoteTarget(ctx, req.Target)
	if err != nil {
		return RemoteTransferResult{}, err
	}

	// 2. Validate remote path against workspace root
	cleanRemotePath, err := validateRemoteWorkspacePath(req.RemotePath, rt.WorkspaceRoot, false)
	if err != nil {
		return RemoteTransferResult{}, err
	}
	if cleanRemotePath == "." {
		return RemoteTransferResult{}, codedToolError("E_PATH_OUTSIDE", errors.New("refusing transfer directly targeting remote workspace root without subpath"))
	}
	// VCS metadata check for remote path
	if err := remoteVCSMetadataWrite(cleanRemotePath); err != nil {
		return RemoteTransferResult{}, err
	}

	// 3. Resolve local workspace and validate local path
	sessionID := toolSessionID(ctx)
	var workspace string
	if sessionID != "" {
		a.mu.Lock()
		workspace = a.sessionWorkspaces[sessionID]
		if workspace == "" {
			workspace = a.config.Workspace
		}
		a.mu.Unlock()
	} else {
		a.mu.Lock()
		workspace = a.config.Workspace
		a.mu.Unlock()
	}
	if workspace == "" {
		return RemoteTransferResult{}, errors.New("no workspace configured for local transfer")
	}

	localAbs, err := pathutil.SafeJoin(pathRuntime, []string{workspace}, req.LocalPath)
	if err != nil {
		return RemoteTransferResult{}, codedToolError("E_PATH_OUTSIDE", fmt.Errorf("localPath %q is outside workspace: %w", req.LocalPath, err))
	}
	relToWs, relErr := filepath.Rel(workspace, localAbs)
	if relErr != nil || strings.HasPrefix(relToWs, "..") || relToWs == ".." {
		return RemoteTransferResult{}, codedToolError("E_PATH_OUTSIDE", fmt.Errorf("localPath %q escapes workspace: %s", req.LocalPath, workspace))
	}
	localClean := filepath.ToSlash(relToWs)
	if localClean == "." {
		return RemoteTransferResult{}, codedToolError("E_PATH_OUTSIDE", errors.New("refusing transfer directly targeting local workspace root without subpath"))
	}
	if blocked, reason := pathutil.VCSMetadataReason(localAbs); blocked {
		return RemoteTransferResult{}, codedToolError("E_PROTECTED_PATH", errors.New(reason))
	}

	// 4. Action-specific validation
	if action == "upload" {
		// Check local source existence
		if _, err := os.Lstat(localAbs); err != nil {
			if os.IsNotExist(err) {
				return RemoteTransferResult{}, fmt.Errorf("local source does not exist: %s", localClean)
			}
			return RemoteTransferResult{}, fmt.Errorf("access local source: %w", err)
		}
		// Sensitive file read fence check
		if blocked, reason := blockedSensitiveRead(localAbs); blocked {
			return RemoteTransferResult{}, codedToolError("E_PROTECTED_PATH", fmt.Errorf("upload rejected: %s", reason))
		}

		// Stat remote destination to check existence
		stat, statErr := a.remoteStatPath(ctx, rt, cleanRemotePath)
		if statErr != nil {
			return RemoteTransferResult{}, fmt.Errorf("check remote destination: %w", statErr)
		}
		if stat.Exists && !req.Overwrite {
			return RemoteTransferResult{}, codedToolError("E_EXISTS", fmt.Errorf("remote destination %s already exists on %s; set overwrite=true to replace it", cleanRemotePath, rt.Host))
		}
	} else { // download
		// Stat remote source to ensure it exists
		stat, statErr := a.remoteStatPath(ctx, rt, cleanRemotePath)
		if statErr != nil {
			return RemoteTransferResult{}, fmt.Errorf("check remote source: %w", statErr)
		}
		if !stat.Exists {
			return RemoteTransferResult{}, fmt.Errorf("remote source does not exist on %s: %s", rt.Host, cleanRemotePath)
		}

		// Check local destination existence
		if _, destErr := os.Lstat(localAbs); destErr == nil && !req.Overwrite {
			return RemoteTransferResult{}, codedToolError("E_EXISTS", fmt.Errorf("local destination %s already exists; set overwrite=true to replace it", localClean))
		}
	}

	// 5. Execute raw stream transfer via Python Bridge
	result, err := a.runRemoteTransferStream(ctx, rt, action, localAbs, cleanRemotePath, req.Overwrite)
	if err != nil {
		return RemoteTransferResult{}, err
	}

	result.Action = action
	result.Target = req.Target
	result.LocalPath = localClean
	result.RemotePath = cleanRemotePath
	result.OK = true
	if action == "upload" {
		result.Summary = fmt.Sprintf("successfully uploaded %s to %s:%s (%d bytes, %d files)", localClean, rt.Host, cleanRemotePath, result.TotalBytes, result.FileCount)
	} else {
		result.Summary = fmt.Sprintf("successfully downloaded %s:%s to %s (%d bytes, %d files)", rt.Host, cleanRemotePath, localClean, result.TotalBytes, result.FileCount)
	}
	return result, nil
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

func (a *App) runRemoteTransferStream(ctx context.Context, rt remoteTarget, action, localAbs, cleanRemotePath string, overwrite bool) (RemoteTransferResult, error) {
	arcRootName := path.Base(cleanRemotePath)
	if action == "download" {
		arcRootName = filepath.Base(localAbs)
	}
	payload := map[string]any{
		"action":        action,
		"workspaceRoot": rt.WorkspaceRoot,
		"remotePath":    cleanRemotePath,
		"arcRootName":   arcRootName,
		"overwrite":     overwrite,
	}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return RemoteTransferResult{}, err
	}
	b64Payload := base64.RawURLEncoding.EncodeToString(rawPayload)
	b64Script := getRemoteTransferCompressedScript()

	remoteCmd := fmt.Sprintf(`sh -c 'boot="import base64, sys, zlib; code = zlib.decompress(base64.b64decode(sys.argv.pop(1))); exec(compile(code, \"<ally-transfer>\", \"exec\"))"; command -v python3 >/dev/null 2>&1 && exec python3 -u -c "$boot" "$@" ; command -v python2 >/dev/null 2>&1 && exec python2 -u -c "$boot" "$@" ; exec python -u -c "$boot" "$@"' -- %s %s`, b64Script, b64Payload)

	entry, _ := a.sshCredentials.lookup(sshCredentialKey(rt.Host, rt.Port))
	sshConfig := a.remoteSSHConfig(rt, entry)

	var outcome struct {
		TotalBytes int64 `json:"totalBytes"`
		FileCount  int   `json:"fileCount"`
		DirCount   int   `json:"dirCount"`
	}

	if action == "upload" {
		var packed streamarchive.Stats
		stderr, runErr := refreshHostKeyAndRetry(func() ([]byte, error) {
			pr, pw := io.Pipe()
			packErrCh := make(chan error, 1)
			go func() {
				var pErr error
				defer func() {
					if pErr != nil {
						_ = pw.CloseWithError(pErr)
					} else {
						_ = pw.Close()
					}
					packErrCh <- pErr
				}()
				packed, pErr = streamarchive.PackWithFilter(localAbs, arcRootName, pw, func(current string, isDir bool) error {
					if blocked, reason := blockedSensitiveRead(current); blocked {
						return codedToolError("E_PROTECTED_PATH", fmt.Errorf("upload rejected: %s", reason))
					}
					return nil
				})
			}()

			stderr, err := sshclient.RunStream(ctx, sshConfig, remoteCmd, pr, nil)
			if err != nil {
				_ = pr.CloseWithError(err)
			} else {
				_ = pr.Close()
			}
			packErr := <-packErrCh

			if packErr != nil && !errors.Is(packErr, io.ErrClosedPipe) {
				return stderr, fmt.Errorf("local archive packing error: %w", packErr)
			}
			if err != nil {
				return stderr, err
			}
			return stderr, nil
		})

		if runErr != nil {
			return RemoteTransferResult{}, parseRemoteTransferError(rt.Host, stderr, runErr)
		}
		if err := decodeRemoteTransferMarker(stderr, &outcome); err != nil {
			return RemoteTransferResult{}, err
		}
		if outcome.TotalBytes != packed.TotalBytes || outcome.FileCount != packed.FileCount || outcome.DirCount != packed.DirCount {
			return RemoteTransferResult{}, errors.New("remote transfer statistics do not match packed archive")
		}
	} else { // download
		type downloadAttemptResult struct {
			stderr []byte
			stats  streamarchive.Stats
		}

		res, runErr := refreshHostKeyAndRetry(func() (downloadAttemptResult, error) {
			pr, pw := io.Pipe()
			type unpackResult struct {
				stats streamarchive.Stats
				err   error
			}
			unpackCh := make(chan unpackResult, 1)

			go func() {
				var uRes unpackResult
				defer func() {
					if uRes.err != nil {
						_ = pr.CloseWithError(uRes.err)
					} else {
						_ = pr.Close()
					}
					unpackCh <- uRes
				}()
				uRes.stats, uRes.err = streamarchive.UnpackTarget(pr, localAbs, overwrite)
				if uRes.err == nil {
					_, _ = io.Copy(io.Discard, pr)
				}
			}()

			stderr, err := sshclient.RunStream(ctx, sshConfig, remoteCmd, nil, pw)
			if err != nil {
				_ = pw.CloseWithError(err)
			} else {
				_ = pw.Close()
			}
			unpackRes := <-unpackCh

			if unpackRes.err != nil && !errors.Is(unpackRes.err, io.ErrClosedPipe) {
				return downloadAttemptResult{stderr: stderr, stats: unpackRes.stats}, fmt.Errorf("local archive unpacking error: %w", unpackRes.err)
			}
			if err != nil {
				// If local unpack was completely successful and remote transfer marker indicates success,
				// ignore pipe close errors caused by EOF tear-down
				if unpackRes.err == nil && (errors.Is(err, io.ErrClosedPipe) || strings.Contains(err.Error(), "closed pipe")) {
					if markerErr := decodeRemoteTransferMarker(stderr, nil); markerErr == nil {
						return downloadAttemptResult{stderr: stderr, stats: unpackRes.stats}, nil
					}
				}
				return downloadAttemptResult{stderr: stderr, stats: unpackRes.stats}, err
			}
			return downloadAttemptResult{stderr: stderr, stats: unpackRes.stats}, nil
		})

		if runErr != nil {
			return RemoteTransferResult{}, parseRemoteTransferError(rt.Host, res.stderr, runErr)
		}
		if err := decodeRemoteTransferMarker(res.stderr, &outcome); err != nil {
			return RemoteTransferResult{}, err
		}
		if outcome.TotalBytes != res.stats.TotalBytes || outcome.FileCount != res.stats.FileCount || outcome.DirCount != res.stats.DirCount {
			return RemoteTransferResult{}, errors.New("remote transfer statistics do not match extracted archive")
		}
	}

	return RemoteTransferResult{
		TotalBytes: outcome.TotalBytes,
		FileCount:  outcome.FileCount,
		DirCount:   outcome.DirCount,
	}, nil
}
