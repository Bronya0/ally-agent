package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"ally-dev/internal/tools/command"
	"ally-dev/internal/tools/edit"
)

const remotePythonMarker = "ALLY_REMOTE_RESULT_JSON:"

const remotePythonScript = `
import base64, json, os, pathlib, re, selectors, shutil, signal, stat as stat_mod, subprocess, sys, tempfile, time
from datetime import datetime, timezone

MARKER = "ALLY_REMOTE_RESULT_JSON:"

def fail(msg):
    print(MARKER + json.dumps({"ok": False, "error": str(msg)}, separators=(",", ":")))
    sys.exit(0)

def ok(data):
    print(MARKER + json.dumps({"ok": True, "data": data}, separators=(",", ":")))
    sys.exit(0)

def decode_payload(arg):
    padding = "=" * (-len(arg) % 4)
    return json.loads(base64.urlsafe_b64decode((arg + padding).encode("ascii")).decode("utf-8"))

def as_posix_rel(root, path):
    return pathlib.Path(path).relative_to(root).as_posix()

def safe_join(root, rel):
    rel = "" if rel is None else str(rel)
    if "\x00" in rel:
        raise ValueError("path contains NUL byte")
    if rel == "" or rel == ".":
        return root
    rel_path = pathlib.PurePosixPath(rel.replace("\\", "/"))
    if rel_path.is_absolute():
        raise ValueError("remote path must be relative to workspaceRoot")
    if any(part == ".." for part in rel_path.parts):
        raise ValueError("remote path must not contain '..'")
    target = (root / pathlib.Path(*rel_path.parts)).resolve(strict=False)
    if os.path.commonpath([str(root), str(target)]) != str(root):
        raise ValueError("remote path is outside workspaceRoot")
    return target

def is_heavy_dir(name):
    return name.lower() in {".git", "node_modules", "dist", "build", "target", ".next", ".nuxt", ".svelte-kit", "vendor", "__pycache__"}

def contains_vcs(path):
    return any(part in {".git", ".svn", ".hg"} for part in path.parts)

def is_protected_delete_path(path):
    p = str(path)
    exact_only = ["/", "/home", "/Users", "/Volumes"]
    for item in exact_only:
        if p == item:
            return True
    protected_trees = ["/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/dev", "/proc", "/sys", "/root", "/System", "/Library", "/Applications"]
    for item in protected_trees:
        if p == item or p.startswith(item.rstrip("/") + "/"):
            return True
    return False

def iso_mtime(st):
    return datetime.fromtimestamp(st.st_mtime, timezone.utc).isoformat()

def op_list(root, payload):
    start = safe_join(root, payload.get("path", ""))
    if not start.exists():
        raise FileNotFoundError(str(start))
    if not start.is_dir():
        raise ValueError("path is not a directory")
    max_depth = int(payload.get("maxDepth") or 3)
    if max_depth < 0:
        max_depth = 0
    if max_depth > 20:
        max_depth = 20
    limit = int(payload.get("limit") or 200)
    if limit < 1:
        limit = 1
    if limit > 1000:
        limit = 1000
    include_hidden = bool(payload.get("includeHidden"))
    entries = []
    truncated = False
    root_str = str(root)
    start_str = str(start)
    for current, dirs, files in os.walk(start):
        # depth = 当前目录相对 start 的深度（用路径分隔符计数，避免 pathlib 开销）
        if current == start_str:
            depth = 0
        else:
            depth = current[len(start_str):].count(os.sep)
        if depth >= max_depth:
            dirs[:] = []
        dirs[:] = sorted([d for d in dirs if include_hidden or (not d.startswith(".") and not is_heavy_dir(d))])
        names = [(d, True) for d in dirs] + [(f, False) for f in sorted(files) if include_hidden or not f.startswith(".")]
        for name, is_dir in names:
            full = os.path.join(current, name)
            try:
                st = os.stat(full)
            except OSError:
                continue
            # 相对 root 的 posix 路径（字符串处理，避免 pathlib.Path 构造）
            rel = os.path.relpath(full, root_str)
            rel_posix = rel.replace(os.sep, "/")
            entries.append({
                "path": rel_posix,
                "name": name,
                "dir": is_dir,
                "size": 0 if is_dir else st.st_size,
                "modTime": iso_mtime(st),
            })
            if len(entries) >= limit:
                truncated = True
                return {"entries": entries, "count": len(entries), "truncated": truncated}
    return {"entries": entries, "count": len(entries), "truncated": truncated}

def op_read(root, payload):
    path = safe_join(root, payload.get("path", ""))
    max_bytes = int(payload.get("maxBytes") or 2097152)
    st = path.stat()
    if stat_mod.S_ISDIR(st.st_mode):
        raise ValueError("path is a directory")
    if st.st_size > max_bytes:
        raise ValueError("file is too large: %d bytes" % st.st_size)
    with open(str(path), "rb") as f:
        data = f.read()
    return {"path": as_posix_rel(root, path), "dataBase64": base64.b64encode(data).decode("ascii"), "size": len(data), "mode": st.st_mode & 0o777, "modTime": iso_mtime(st)}

def op_write(root, payload):
    path = safe_join(root, payload.get("path", ""))
    mkdirs = bool(payload.get("mkdirs"))
    overwrite = bool(payload.get("overwrite"))
    original_mode = None
    try:
        existing_st = path.stat()
    except FileNotFoundError:
        existing_st = None
    if existing_st is not None:
        if stat_mod.S_ISDIR(existing_st.st_mode):
            raise ValueError("path is a directory")
        if not overwrite:
            raise FileExistsError("file already exists: " + payload.get("path", ""))
        original_mode = existing_st.st_mode & 0o7777
    parent = path.parent
    if mkdirs:
        parent.mkdir(parents=True, exist_ok=True)
    elif not parent.exists():
        raise FileNotFoundError(str(parent))
    data = base64.b64decode(payload.get("dataBase64", ""))
    fd, tmp = tempfile.mkstemp(prefix=".ally-write-", dir=str(parent))
    try:
        if original_mode is not None:
            os.fchmod(fd, original_mode)
        with os.fdopen(fd, "wb") as f:
            fd = -1
            f.write(data)
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, path)
    finally:
        if fd >= 0:
            try:
                os.close(fd)
            except OSError:
                pass
        try:
            if os.path.exists(tmp):
                os.unlink(tmp)
        except OSError:
            pass
    st = path.stat()
    return {"path": as_posix_rel(root, path), "size": st.st_size, "mode": st.st_mode & 0o7777, "modTime": iso_mtime(st)}

def op_delete(root, payload):
    path = safe_join(root, payload.get("path", ""))
    if path == root:
        raise ValueError("refusing to delete remote workspace root")
    if contains_vcs(path):
        raise ValueError("refusing to delete path containing VCS metadata")
    if is_protected_delete_path(path):
        raise ValueError("refusing to delete OS-sensitive path")
    if path.is_dir():
        if not payload.get("recursive"):
            raise ValueError("path is a directory; set recursive=true")
        shutil.rmtree(path)
    else:
        path.unlink()
    return {"deleted": payload.get("path", "")}

DELETE_RE = re.compile(r"(?i)(^|[\s;&|()])(?:rm|unlink|rmdir|del|erase|rd|remove-item|ri)\b")

def op_run(root, payload):
    command = str(payload.get("command") or "")
    if not command.strip():
        raise ValueError("command is required")
    if DELETE_RE.search(command):
        raise ValueError("remote_run_command refuses explicit deletion commands; use remote_delete_path")
    cwd = safe_join(root, payload.get("cwd", ""))
    if not cwd.is_dir():
        raise ValueError("cwd is not a directory")
    timeout = int(payload.get("timeoutSeconds") or 120)
    if timeout < 1:
        timeout = 1
    if timeout > 600:
        timeout = 600
    shell = str(payload.get("shell") or "")
    if not shell:
        shell = "/bin/bash" if os.path.exists("/bin/bash") else "/bin/sh"
    max_output = int(payload.get("maxOutput") or 131072)
    start = time.time()
    preexec = os.setsid if hasattr(os, "setsid") else None
    proc = subprocess.Popen(command, shell=True, cwd=str(cwd), executable=shell, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL, preexec_fn=preexec)
    out = bytearray()
    truncated = False
    timed_out = False
    sel = selectors.DefaultSelector()
    sel.register(proc.stdout, selectors.EVENT_READ)
    deadline = start + timeout
    eof = False
    while True:
        if time.time() > deadline:
            timed_out = True
            try:
                if preexec:
                    os.killpg(proc.pid, signal.SIGKILL)
                else:
                    proc.kill()
            except Exception:
                pass
            break
        if eof:
            # stdout 已关闭但进程可能仍在运行（如守护进程关闭了 std fd）。
            # 不能在此 break，否则 poll() 返回 None 会被 JSON 编码为 null，
            # Go 侧 int 字段反序列化 null 得到 0，误报为成功退出。
            # 用 proc.wait 阻塞等待进程退出（OS 级通知，比 sleep 轮询高效），
            # 至多等到 deadline；超时则回到循环顶部走 timed_out 分支。
            remain = deadline - time.time()
            if remain <= 0:
                continue
            try:
                proc.wait(timeout=min(remain, 1.0))
            except subprocess.TimeoutExpired:
                pass
        else:
            events = sel.select(timeout=0.1)
            for key, _ in events:
                chunk = key.fileobj.read1(8192) if hasattr(key.fileobj, "read1") else key.fileobj.read(8192)
                if not chunk:
                    eof = True
                    break
                remain = max_output - len(out)
                if remain > 0:
                    out.extend(chunk[:remain])
                if len(chunk) > remain:
                    truncated = True
        if proc.poll() is not None:
            # 进程已退出；排空管道内剩余缓冲数据（最多等 0.5s，避免后台
            # 子进程持有 stdout fd 时无限阻塞）。
            drain_deadline = time.time() + 0.5
            while not eof and time.time() < drain_deadline:
                events = sel.select(timeout=0.1)
                if not events:
                    break
                for key, _ in events:
                    chunk = key.fileobj.read1(8192) if hasattr(key.fileobj, "read1") else key.fileobj.read(8192)
                    if not chunk:
                        eof = True
                        break
                    remain = max_output - len(out)
                    if remain > 0:
                        out.extend(chunk[:remain])
                    if len(chunk) > remain:
                        truncated = True
            break
    sel.close()
    try:
        proc.wait(timeout=5)
    except Exception:
        pass
    try:
        proc.stdout.close()
    except Exception:
        pass
    exit_code = proc.poll()
    if exit_code is None:
        # 兜底：极端情况下进程仍未退出（如 D 状态不可中断），报告 -1 而非 0
        exit_code = -1
    if timed_out:
        exit_code = -1
    duration = int((time.time() - start) * 1000)
    output = out.decode("utf-8", errors="replace")
    return {"command": command, "cwd": str(cwd), "shell": shell, "shellPath": shell, "output": output, "exitCode": exit_code, "timedOut": timed_out, "durationMs": duration, "truncated": truncated}

try:
    payload = decode_payload(sys.argv[1])
    root = pathlib.Path(payload["workspaceRoot"]).expanduser().resolve(strict=True)
    if str(root) == "/":
        raise ValueError("workspaceRoot must not be filesystem root")
    op = payload.get("op")
    if op == "list":
        ok(op_list(root, payload))
    elif op == "read":
        ok(op_read(root, payload))
    elif op == "write":
        ok(op_write(root, payload))
    elif op == "delete":
        ok(op_delete(root, payload))
    elif op == "run":
        ok(op_run(root, payload))
    else:
        raise ValueError("unknown op: %s" % op)
except Exception as exc:
    fail(str(exc))
`

type remotePythonResponse struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

func parseRemoteTarget(raw string) (remoteTarget, error) {
	return parseRemoteTargetWithOptions(raw, false)
}

func parseRemoteTargetWithOptions(raw string, allowRoot bool) (remoteTarget, error) {
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
		if root == "." || !strings.HasPrefix(root, "/") || (!allowRoot && root == "/") {
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
	if root == "." || !strings.HasPrefix(root, "/") || (!allowRoot && root == "/") {
		return remoteTarget{}, errors.New("remote workspaceRoot must be an absolute non-root path")
	}
	return remoteTarget{Raw: raw, Host: host, WorkspaceRoot: root}, nil
}

func normalizeRemoteListTarget(rawTarget, rawPath string) (remoteTarget, string, error) {
	rt, err := parseRemoteTargetWithOptions(rawTarget, true)
	if err != nil {
		return remoteTarget{}, "", err
	}
	if rt.WorkspaceRoot != "/" {
		cleanPath, err := validateRemoteRelativePath(rawPath, true)
		return rt, cleanPath, err
	}
	p := strings.TrimSpace(filepath.ToSlash(rawPath))
	if p == "" || p == "." || p == "/" {
		return remoteTarget{}, "", errors.New("remote workspaceRoot '/' is not allowed for broad listing; use target like host:/home or host:/srv/app")
	}
	if strings.ContainsRune(p, 0) {
		return remoteTarget{}, "", errors.New("path contains NUL byte")
	}
	p = strings.TrimPrefix(p, "/")
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return remoteTarget{}, "", errors.New("path must not contain '..'")
		}
	}
	rt.WorkspaceRoot = "/" + path.Clean(p)
	return rt, ".", nil
}

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

func (a *App) invokeRemotePython(ctx context.Context, rt remoteTarget, payload map[string]any, timeout time.Duration, out any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3"}
	if rt.Port != "" {
		args = append(args, "-p", rt.Port)
	}
	args = append(args, rt.Host, "python3", "-", encoded)
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "ssh", args...)
	cmd.Stdin = strings.NewReader(remotePythonScript)
	var stdout bytes.Buffer
	var stderr limitedBuffer
	stderr.limit = 64 * 1024
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	hideCommandWindow(cmd)
	err = cmd.Run()
	if runCtx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("remote ssh timed out after %s", timeout)
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("ssh %s failed: %s", rt.Host, msg)
	}
	// 远程命令的输出会被原样嵌在 ok() 的 JSON output 字段里，如果命令
	// 恰好输出了 marker 字符串，marker 会同时出现在 JSON 内部。因此不能
	// 简单用 LastIndex 取最后一个匹配（会命中 JSON 内部），需要从后往前
	// 逐个尝试：真正 ok() 的 JSON 必然合法，误命中的候选必然解析失败。
	markerBytes := []byte(remotePythonMarker)
	stdoutBytes := stdout.Bytes()
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
		return fmt.Errorf("remote helper returned no JSON result; stderr: %s", strings.TrimSpace(stderr.String()))
	}
	if !resp.OK {
		if resp.Error == "" {
			resp.Error = "remote helper failed"
		}
		return errors.New(resp.Error)
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
	if bytes.Contains(raw, []byte{0}) {
		return remoteRawFile{}, errors.New("binary file is not supported")
	}
	if !utf8.Valid(raw) {
		return remoteRawFile{}, errors.New("file is not valid UTF-8")
	}
	_, ending, _ := normalizeText(raw)
	return remoteRawFile{Path: data.Path, Data: raw, Size: data.Size, Mode: data.Mode, ModTime: data.ModTime, LineEnding: ending}, nil
}

func (a *App) remoteReadRaw(ctx context.Context, target, relPath string) (remoteTarget, remoteRawFile, error) {
	rt, err := parseRemoteTarget(target)
	if err != nil {
		return remoteTarget{}, remoteRawFile{}, err
	}
	cleanPath, err := validateRemoteRelativePath(relPath, false)
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

func (a *App) remoteWriteRaw(ctx context.Context, rt remoteTarget, relPath string, data []byte, overwrite, mkdirs bool) error {
	cleanPath, err := validateRemoteRelativePath(relPath, false)
	if err != nil {
		return err
	}
	return a.invokeRemotePython(ctx, rt, remotePayload(rt, "write", map[string]any{
		"path":       cleanPath,
		"dataBase64": base64.StdEncoding.EncodeToString(data),
		"overwrite":  overwrite,
		"mkdirs":     mkdirs,
	}), 60*time.Second, nil)
}

func (a *App) remoteListFiles(ctx context.Context, req RemoteListFilesRequest) (ListFilesResult, error) {
	rt, cleanPath, err := normalizeRemoteListTarget(req.Target, req.Path)
	if err != nil {
		return ListFilesResult{}, err
	}
	maxDepth := req.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 3
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 200
	}
	var result ListFilesResult
	err = a.invokeRemotePython(ctx, rt, remotePayload(rt, "list", map[string]any{
		"path":          cleanPath,
		"maxDepth":      maxDepth,
		"limit":         limit,
		"includeHidden": req.IncludeHidden,
	}), 60*time.Second, &result)
	return result, err
}

func (a *App) remoteReadFile(ctx context.Context, req RemoteReadFileRequest) (ReadFileResult, error) {
	_, file, err := a.remoteReadRaw(ctx, req.Target, req.Path)
	if err != nil {
		return ReadFileResult{}, err
	}
	text, ending, _ := normalizeText(file.Data)
	sha256Hex, version := hashBytesAndVersion(file.Data)
	preview, err := formatLineNumberReadPreviewRangeWithBudget(text, readRangeRequest{
		StartLine: req.StartLine,
		EndLine:   req.EndLine,
	}, maxToolOutput)
	if err != nil {
		return ReadFileResult{}, err
	}
	return ReadFileResult{
		Path:                  file.Path,
		Content:               preview.Content,
		RawContent:            preview.RawContent,
		Kind:                  "text",
		ContentFormat:         "line_numbers",
		Editable:              true,
		StartLine:             preview.StartLine,
		EndLine:               preview.EndLine,
		NextStartLine:         preview.NextStartLine,
		TotalLines:            preview.TotalLines,
		SHA256:                sha256Hex,
		Version:               version,
		Size:                  file.Size,
		LineEnding:            ending,
		Truncated:             preview.Truncated,
		TruncatedLines:        preview.TruncatedLines,
		TruncatedLinesOmitted: preview.TruncatedLinesOmitted,
		RangeStatus:           preview.RangeStatus,
		EmptyRange:            preview.EmptyRange,
	}, nil
}

func (a *App) remoteEdit(ctx context.Context, req RemoteEditRequest) (MultiEditResult, error) {
	if strings.TrimSpace(req.Target) == "" {
		return MultiEditResult{}, errors.New("target is required")
	}
	if err := validateModelEditToolRequest(req.Files); err != nil {
		return MultiEditResult{}, err
	}
	result := MultiEditResult{Files: make([]EditResult, 0, len(req.Files))}
	type remoteBackup struct {
		rt   remoteTarget
		path string
		data []byte
	}
	backups := make([]remoteBackup, 0, len(req.Files))
	var rollbackErrors []string
	for _, file := range req.Files {
		rt, original, err := a.remoteReadRaw(ctx, req.Target, file.Path)
		if err != nil {
			for i := len(backups) - 1; i >= 0; i-- {
				if rbErr := a.remoteWriteRaw(ctx, backups[i].rt, backups[i].path, backups[i].data, true, true); rbErr != nil {
					rollbackErrors = append(rollbackErrors, fmt.Sprintf("%s: %v", backups[i].path, rbErr))
				}
			}
			if len(rollbackErrors) > 0 {
				err = fmt.Errorf("%w (rollback failures: %s)", err, strings.Join(rollbackErrors, "; "))
			}
			return MultiEditResult{}, err
		}
		edited, err := a.remoteEditOne(ctx, rt, file, original)
		if err != nil {
			for i := len(backups) - 1; i >= 0; i-- {
				if rbErr := a.remoteWriteRaw(ctx, backups[i].rt, backups[i].path, backups[i].data, true, true); rbErr != nil {
					rollbackErrors = append(rollbackErrors, fmt.Sprintf("%s: %v", backups[i].path, rbErr))
				}
			}
			if len(rollbackErrors) > 0 {
				err = fmt.Errorf("%w (rollback failures: %s)", err, strings.Join(rollbackErrors, "; "))
			}
			return MultiEditResult{}, err
		}
		backups = append(backups, remoteBackup{rt: rt, path: file.Path, data: original.Data})
		result.Files = append(result.Files, edited)
		result.Replacements += edited.Replacements
		result.AddedLines += edited.AddedLines
		result.RemovedLines += edited.RemovedLines
	}
	result.FileCount = len(result.Files)
	result.Summary = fmt.Sprintf("Edited %d remote files", result.FileCount)
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
	if bytes.Equal(file.Data, after) {
		return EditResult{}, codedToolError("E_NOOP", errors.New("edit produced no content changes"))
	}
	if err := a.remoteWriteRaw(ctx, rt, req.Path, after, true, true); err != nil {
		return EditResult{}, err
	}
	diff := edit.GenerateEditDiffPreview(text, result.Content, maxToolOutput)
	added, removed := 0, 0
	if diff != "" {
		added, removed = edit.CountEditDiffStats(diff)
	} else {
		added, removed = edit.ApproximateLineDeltaContent(text, result.Content)
	}
	classification := "edit"
	if len(result.Content) > len(text) {
		classification = "addition"
	} else if len(result.Content) < len(text) {
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
		ChangedLinesBlock: edit.BuildLineNumberContextBlock(result.Content, result.FirstChangedLine, result.LastChangedLine, splitLines),
	}, nil
}

func (a *App) remoteCreateFile(ctx context.Context, req RemoteCreateFileRequest) (EditResult, error) {
	rt, err := parseRemoteTarget(req.Target)
	if err != nil {
		return EditResult{}, err
	}
	cleanPath, err := validateRemoteRelativePath(req.Path, false)
	if err != nil {
		return EditResult{}, err
	}
	before := []byte{}
	beforeHash := ""
	beforeVersion := ""
	if _, existing, readErr := a.remoteReadRaw(ctx, req.Target, cleanPath); readErr == nil {
		before = existing.Data
		beforeHash, beforeVersion = hashBytesAndVersion(existing.Data)
	}
	content, ending, hadBOM := normalizeText([]byte(req.Content))
	encoded := encodeText(content, ending, hadBOM)
	if err := a.remoteWriteRaw(ctx, rt, cleanPath, encoded, req.Overwrite, true); err != nil {
		return EditResult{}, err
	}
	return makeEditResult(cleanPath, beforeHash, beforeVersion, before, encoded, ending, 1, string(before), content), nil
}

func (a *App) remoteDeletePath(ctx context.Context, req RemoteDeletePathRequest) (map[string]any, error) {
	rt, err := parseRemoteTarget(req.Target)
	if err != nil {
		return nil, err
	}
	cleanPath, err := validateRemoteRelativePath(req.Path, false)
	if err != nil {
		return nil, err
	}
	if cleanPath == "." {
		return nil, codedToolError("E_DELETE_BLOCKED", errors.New("refusing to delete remote workspace root"))
	}
	for _, part := range strings.Split(cleanPath, "/") {
		if part == ".git" || part == ".svn" || part == ".hg" {
			return nil, codedToolError("E_DELETE_BLOCKED", errors.New("refusing to delete VCS metadata"))
		}
	}
	var result map[string]any
	err = a.invokeRemotePython(ctx, rt, remotePayload(rt, "delete", map[string]any{"path": cleanPath, "recursive": req.Recursive}), 60*time.Second, &result)
	return result, err
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
	rt, err := parseRemoteTarget(req.Target)
	if err != nil {
		return CommandResult{}, err
	}
	cwd, err := validateRemoteRelativePath(req.Cwd, true)
	if err != nil {
		return CommandResult{}, err
	}
	timeout := req.TimeoutSeconds
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
	var result CommandResult
	err = a.invokeRemotePython(ctx, rt, remotePayload(rt, "run", map[string]any{
		"command":        req.Command,
		"cwd":            cwd,
		"timeoutSeconds": timeout,
		"shell":          req.Shell,
		"maxOutput":      maxToolOutput,
	}), time.Duration(timeout+20)*time.Second, &result)
	return result, err
}
