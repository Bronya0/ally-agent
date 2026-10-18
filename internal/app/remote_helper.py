# -*- coding: utf-8 -*-
from __future__ import print_function
import base64, errno, json, os, stat as stat_mod, sys, tempfile, time

TRANSFER_MODE = False

MARKER = "ALLY_REMOTE_RESULT_JSON:"

# 文本类型集合：Python 2 下 unicode 与 str（字节串）都算文本。
if sys.version_info[0] < 3:
    TEXT_TYPES = (str, unicode)
else:
    TEXT_TYPES = (str,)

# 受保护删除清单占位符：Go 侧在构建脚本时替换为 JSON 数组字面量。
# 包含跨平台统一的系统敏感目录与高危文件黑名单。
DELETE_SENSITIVE_TARGETS = __DELETE_SENSITIVE_TARGETS__

# payload 占位符：Go 侧每次调用时替换为真实 base64url 编码的 payload，
# 整个脚本经 ssh stdin 发送，避免大 payload 塞进命令行参数。
PAYLOAD_B64 = "__PAYLOAD_B64__"

def to_unicode(s):
    if sys.version_info[0] < 3:
        if isinstance(s, BaseException):
            # 不能写 unicode(exc)：Python 2 会把异常消息按 ASCII 解码，消息里
            # 嵌了非 ASCII 字节（远端路径）时反而抛 UnicodeDecodeError。优先
            # 取 str(exc) 的原始字节（随后按 UTF-8 容错解码），异常自身格式化
            # 不出来时才退到逐个 args 解码。
            try:
                s = str(s)
            except Exception:
                s = " ".join(to_unicode(arg) for arg in s.args) if s.args else s.__class__.__name__
        if isinstance(s, str):
            return s.decode("utf-8", "replace")
        if isinstance(s, unicode):
            return s
        return unicode(s)
    return str(s)

def to_os_text(s):
    # 交给 OS（os.* 与 subprocess）的文本形态：Python 2 下必须是 UTF-8 字节串，
    # Python 3 下是 str。Python 2 的 str(unicode) 一律按 ASCII 编码——命令或
    # 路径里只要有一个非 ASCII 字符就抛 UnicodeEncodeError（消息里的 position
    # 是该字符在串中的下标）；远端 locale 非 UTF-8 时 os.* / Popen 同样无法
    # 编码 unicode 路径。
    if sys.version_info[0] < 3:
        if isinstance(s, unicode):
            return s.encode("utf-8")
        if isinstance(s, str):
            return s
        return str(s)
    if isinstance(s, str):
        return s
    if isinstance(s, bytes):
        return s.decode("utf-8", "replace")
    return str(s)

def payload_os_text(obj):
    # Python 2 下把 payload 里的文本一次性转成 UTF-8 字节串：之后命令与路径
    # 全程按字节处理，os.path / os.* 不混类型，也不依赖远端 locale 是否为
    # UTF-8。Python 3 下不做转换（str 本身就是 OS 认的文本）。
    if isinstance(obj, dict):
        return dict((key, payload_os_text(value)) for key, value in obj.items())
    if isinstance(obj, list):
        return [payload_os_text(item) for item in obj]
    if isinstance(obj, TEXT_TYPES):
        return to_os_text(obj)
    return obj

def emit_result(response):
    line = MARKER + json.dumps(response, separators=(",", ":")) + "\n"
    if TRANSFER_MODE:
        sys.stderr.write(line)
        sys.stderr.flush()
    else:
        sys.stdout.write(line)
        sys.stdout.flush()

def fail(msg):
    emit_result({"ok": False, "error": to_unicode(msg)})
    sys.exit(1 if TRANSFER_MODE else 0)

def ok(data):
    emit_result({"ok": True, "data": data})
    sys.exit(0)

def decode_payload():
    encoded = PAYLOAD_B64
    if TRANSFER_MODE:
        if len(sys.argv) < 2:
            raise ValueError("missing payload argument")
        encoded = sys.argv[1]
    padding = "=" * (-len(encoded) % 4)
    raw_b64 = (encoded + padding).encode("ascii")
    decoded = base64.urlsafe_b64decode(raw_b64)
    if hasattr(decoded, "decode"):
        decoded = decoded.decode("utf-8")
    payload = json.loads(decoded)
    if sys.version_info[0] < 3:
        payload = payload_os_text(payload)
    return payload

def is_subpath(child, parent):
    try:
        rel = os.path.relpath(child, parent)
        return rel != ".." and not rel.startswith(".." + os.sep) and not (os.altsep and rel.startswith(".." + os.altsep))
    except (ValueError, OSError):
        return False

def as_posix_rel(root, path):
    return os.path.relpath(path, root).replace("\\", "/")

def safe_join_paths(root, rel):
    # 与 safe_join 同一套判定，但把两个形态都交出来：lexical 是工作区内的字面
    # 路径（删除/写入实际作用的那个），resolved 是符号链接解析后的真实路径
    # （用于越界与保护判定）。两者在这里一次算出，调用方不会各自解析一遍。
    rel = "" if rel is None else to_os_text(rel)
    if "\x00" in rel:
        raise ValueError("path contains NUL byte")
    if rel == "" or rel == ".":
        return root, root
    p = rel.replace("\\", "/")
    if p.startswith("/"):
        raise ValueError("remote path must be relative to workspaceRoot (got '%s')" % to_unicode(rel))
    parts = [part for part in p.split("/") if part and part != "."]
    if any(part == ".." for part in p.split("/")):
        raise ValueError("remote path must not contain '..' (got '%s')" % to_unicode(rel))
    lexical = os.path.normpath(os.path.join(root, *parts)) if parts else root
    if not is_subpath(lexical, root):
        # 与下一条分开报：这条是词法拼接之后就落到根外（根给错了，或路径拼错），
        # 下一条是词法在根内、解析符号链接后才发现逃了出去，两者的下一步不同。
        raise ValueError("remote path is outside workspaceRoot after normalization: '%s' (workspaceRoot '%s')" % (to_unicode(rel), to_unicode(root)))
    resolved = os.path.realpath(lexical)
    if not is_subpath(resolved, root):
        raise ValueError("remote path escapes workspaceRoot via symlink: '%s' (resolved to '%s', workspaceRoot '%s')" % (to_unicode(rel), to_unicode(resolved), to_unicode(root)))
    return lexical, resolved

def safe_join(root, rel):
    return safe_join_paths(root, rel)[1]

def contains_vcs(path):
    parts = path.replace("\\", "/").split("/")
    return any(part in (".git", ".svn", ".hg") for part in parts)

def is_protected_delete_path(path):
    # 与本地 isDangerousDeletePath 统一保持两套规则：
    # 机制 1：系统全盘视角下，禁止删除根目录、第 1 级和第 2 级骨干目录；
    # 机制 2：跨平台敏感文件与深层系统目录黑名单拦截。
    # 返回 (是否保护, 拒绝原因)：原因必须落到真正触发的那条机制上。合成一句会让
    # 模型错误归因（浅根下的第 2 级目录曾被报成「系统敏感路径」，模型以为判定名单
    # 有问题，换个路径反复重试，而真正拦它的是层级规则）。
    p = to_os_text(path).replace(os.sep, "/")
    norm = p.lower()
    if len(norm) >= 2 and norm[1] == ":":
        norm = norm[2:]
    norm = norm.rstrip("/")
    if not norm or norm == "/":
        return True, "refusing to delete the filesystem root"

    # 镜像本地 isDangerousDeletePath 的临时目录豁免：测试与构建工作区
    # 常位于 OS 临时目录下（Linux/macOS 为 /tmp 或 /var/tmp，macOS 为 /var/folders/...）。
    # 临时目录内部的子项放行，只拦临时根目录本身。
    for tmp_candidate in (tempfile.gettempdir(), "/tmp", "/var/tmp"):
        try:
            for t_str in (tmp_candidate, os.path.realpath(tmp_candidate)):
                t_posix = to_os_text(t_str).replace(os.sep, "/").lower()
                if len(t_posix) >= 2 and t_posix[1] == ":":
                    t_posix = t_posix[2:]
                t_posix = t_posix.rstrip("/")
                if t_posix and (norm == t_posix or norm.startswith(t_posix + "/")):
                    if norm == t_posix:
                        return True, "refusing to delete the temporary directory root %s" % t_posix
                    return False, ""
        except Exception:
            pass

    # 机制一：全盘视角 1 级、2 级目录绝对禁止删除
    parts = [part for part in norm.split("/") if part]
    depth = len(parts)

    is_dir = False
    try:
        is_dir = os.path.isdir(path)
    except Exception:
        pass
    if not is_dir:
        try:
            if not os.path.exists(path):
                _, ext = os.path.splitext(norm)
                is_dir = (ext == "")
        except Exception:
            pass

    if depth == 1:
        return True, "refusing to delete top-level filesystem directory"
    if depth == 2 and is_dir:
        return True, "refusing to delete second-level filesystem directory"

    # 机制二：敏感文件与深层目录黑名单
    for target in DELETE_SENSITIVE_TARGETS:
        if norm == target or norm.startswith(target + "/"):
            return True, "refusing to delete protected system target (matched %s)" % target

    return False, ""

def iso_mtime(st):
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(st.st_mtime))

def read_raw_file(root, rel, max_bytes, remaining_bytes=None):
    path = safe_join(root, rel)
    st = os.stat(path)
    if stat_mod.S_ISDIR(st.st_mode):
        raise ValueError("path is a directory, not a file: '%s'" % to_unicode(rel))
    if st.st_size > max_bytes:
        raise ValueError("file is too large: '%s' is %d bytes (limit %d); read a line range instead of the whole file" % (to_unicode(rel), st.st_size, max_bytes))
    # Batch deferral happens after path/type/size checks. Do not read and base64
    # encode a file that cannot fit this response and will be retried anyway.
    if remaining_bytes is not None and st.st_size > remaining_bytes:
        return None
    with open(path, "rb") as f:
        # stat 与 read 之间文件可能被追加，按实际读取字节数兜底判定，
        # 防止增长中的文件绕过尺寸预算。
        data = f.read(max_bytes + 1)
    if len(data) > max_bytes:
        raise ValueError("file is too large: '%s' grew past the limit while it was being read (%d bytes, limit %d); read a line range instead" % (to_unicode(rel), len(data), max_bytes))
    if remaining_bytes is not None and len(data) > remaining_bytes:
        return None
    b64 = base64.b64encode(data)
    if hasattr(b64, "decode"):
        b64 = b64.decode("ascii")
    return {
        "path": as_posix_rel(root, path),
        "dataBase64": to_os_text(b64),
        "size": len(data),
        "mode": st.st_mode & 0o777,
        "modTime": iso_mtime(st),
    }

def op_read(root, payload):
    return read_raw_file(root, payload.get("path", ""), int(payload.get("maxBytes") or 2097152))

def op_stat(root, payload):
    # 只回元数据：read_raw_file 对超限文件与目录直接报错，拿不到“是否存在”，
    # 而覆盖审批与 created 报告都必须建立在真实的存在性上。
    path = safe_join(root, payload.get("path", ""))
    try:
        st = os.stat(path)
    except (OSError, IOError) as exc:
        if getattr(exc, "errno", None) not in (errno.ENOENT, errno.ENOTDIR):
            raise
        return {"path": as_posix_rel(root, path), "exists": False, "isDir": False, "size": 0}
    return {
        "path": as_posix_rel(root, path),
        "exists": True,
        "isDir": bool(stat_mod.S_ISDIR(st.st_mode)),
        "size": st.st_size,
        "mode": st.st_mode & 0o7777,
        "modTime": iso_mtime(st),
    }

def op_read_batch(root, payload):
    # 单会话批量读：逐文件独立 try（单文件失败只污染自己的结果槽，与
    # 本地批量 read 的隔离契约一致）；总字节预算装不下时把余下路径排进
    # remaining，交给 Go 侧再起一轮会话。首文件必收（files 为空时无条
    # 件接受），不会死循环。
    max_bytes = int(payload.get("maxBytes") or 2097152)
    total_bytes = int(payload.get("totalBytes") or 16777216)
    files = []
    remaining = []
    used = 0
    for rel in payload.get("paths") or []:
        item = {"path": rel, "ok": False}
        try:
            data = read_raw_file(root, rel, max_bytes, total_bytes - used if files else None)
        except Exception as exc:
            item["error"] = to_unicode(exc)
            files.append(item)
            continue
        if data is None:
            remaining.append(rel)
            continue
        used += int(data["size"])
        item["ok"] = True
        item["data"] = data
        files.append(item)
    return {"files": files, "remaining": remaining}

def op_write(root, payload):
    lexical, resolved = safe_join_paths(root, payload.get("path", ""))
    # 版本控制元数据在远端同样是禁区：Go 侧已按字面路径判过一遍，但工作区内的
    # 符号链接能把干净的字面路径接到 .git 上（把 .git 链成 gh 之后写
    # gh/hooks/pre-commit），只有远端的事实解析才看得见，所以两个形态各判一次。
    for candidate in (lexical, resolved):
        if contains_vcs(candidate):
            raise ValueError("E_PROTECTED_PATH: refusing to write version-control metadata: %s" % to_unicode(payload.get("path", "")))
    path = resolved
    mkdirs = bool(payload.get("mkdirs"))
    overwrite = bool(payload.get("overwrite"))
    original_mode = None
    if overwrite:
        try:
            existing_st = os.stat(path)
            if stat_mod.S_ISDIR(existing_st.st_mode):
                raise ValueError("cannot replace a directory with a file: '%s'" % to_unicode(payload.get("path", "")))
            original_mode = existing_st.st_mode & 0o7777
        except (OSError, IOError) as exc:
            if getattr(exc, "errno", None) != errno.ENOENT:
                raise
    parent = os.path.dirname(path)
    created_dirs = []
    if mkdirs:
        # 统计实际缺失的父目录链（外层→内层，相对 root），供调用方展示；
        # 必须在 mkdir 之前探测，mkdir 失败时整个 op 报错、列表无意义。
        cur = parent
        while cur and not os.path.exists(cur):
            created_dirs.append(as_posix_rel(root, cur))
            next_cur = os.path.dirname(cur)
            if next_cur == cur:
                break
            cur = next_cur
        created_dirs.reverse()
        if not os.path.exists(parent):
            os.makedirs(parent)
    elif not os.path.exists(parent):
        err = OSError(errno.ENOENT, "No such file or directory: " + parent)
        err.filename = parent
        raise err
    probe_created = False
    if not overwrite:
        # O_EXCL 原子探测（须在 mkdirs 之后）：目标已存在时立即失败，
        # 消除 stat→replace 窗口内的 TOCTOU 竞态；探测创建的空文件
        # 随后被 os.replace 原子覆盖，替换失败时由 finally 清理。
        try:
            probe_fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        except (OSError, IOError) as exc:
            # POSIX 对已存在目标抛 EEXIST、对目录抛 EISDIR；Windows 对目录抛 EACCES。
            try:
                if os.path.isdir(path):
                    raise ValueError("target path is a directory, not a file: " + to_os_text(payload.get("path", "")))
            except (OSError, IOError):
                pass
            raise ValueError("file already exists: " + to_os_text(payload.get("path", "")))
        probe_created = True
        os.close(probe_fd)
    data = None
    fd = -1
    tmp = None
    replaced = False
    try:
        raw_b64 = to_os_text(payload.get("dataBase64", ""))
        data = base64.b64decode(raw_b64)
        fd, tmp = tempfile.mkstemp(prefix=".ally-write-", dir=parent)
        # 覆盖时保留原文件权限位；新建文件对齐本地 SafeWriteFile 的 0644
        # 默认值——mkstemp 的 0600 会让远程新建文件对其他账号不可读。
        # Windows 直到 Python 3.13 才提供 os.fchmod（chmod 接受 fd 也是同一版本
        # 才支持），缺失时退回按路径授权。
        target_mode = original_mode if original_mode is not None else 0o644
        if hasattr(os, "fchmod"):
            os.fchmod(fd, target_mode)
        else:
            os.chmod(tmp, target_mode)
        with os.fdopen(fd, "wb") as f:
            fd = -1
            f.write(data)
            f.flush()
            os.fsync(f.fileno())
        if hasattr(os, "replace"):
            os.replace(tmp, path)
        else:
            if os.name == "nt":
                try:
                    os.rename(tmp, path)
                except OSError:
                    os.remove(path)
                    os.rename(tmp, path)
            else:
                os.rename(tmp, path)
        replaced = True
    finally:
        if fd >= 0:
            try:
                os.close(fd)
            except (OSError, IOError):
                pass
        if tmp is not None:
            try:
                if os.path.exists(tmp):
                    os.unlink(tmp)
            except (OSError, IOError):
                pass
        if probe_created and not replaced and os.path.exists(path):
            # 替换失败时移除探针占位文件，避免残留空文件。
            # 仅本次调用创建的探针且 replace 尚未覆盖它时才可能走到这里；
            # 并发写同一路径不受支持（overwrite=false 语义本身排他）。
            try:
                os.unlink(path)
            except (OSError, IOError):
                pass
    st = os.stat(path)
    return {
        "path": as_posix_rel(root, path),
        "size": st.st_size,
        "mode": st.st_mode & 0o7777,
        "modTime": iso_mtime(st),
        "createdDirs": created_dirs,
    }

def check_delete_path(root, rel, recursive):
    # 单条路径删除的全部判定，不执行任何东西。单路径与批量共用这一份：批量在
    # 动手之前逐条跑完，任一条不通过就整批不删。Go 侧已判过路径形态（相对性、
    # 越界、工作区根、VCS 元数据名），这里判只有远端才知道的事实：是否目录、
    # 是否系统敏感目标；存在性只记录不拒绝（见下）。
    #
    # 两套路径各管一头：保护判定用 resolved（符号链接指向 .git 或系统目录也
    # 要拦住），实际删除用 lexical —— 删符号链接删的是链接本身，不是它指向的
    # 东西，与本地 delete 一致（旧实现按解析后的路径删，实测会把链接指向的
    # 文件/目录抹掉）。
    # 错误文案带上路径：批量里不点名的话，模型不知道是哪一条被拒。
    lexical, resolved = safe_join_paths(root, rel)
    if lexical == root:
        raise ValueError("E_DELETE_BLOCKED: refusing to delete remote workspace root")
    if contains_vcs(resolved):
        raise ValueError("E_DELETE_BLOCKED: refusing to delete path containing VCS metadata (requested '%s', resolved to '%s')" % (to_unicode(rel), to_unicode(resolved)))
    protected, reason = is_protected_delete_path(resolved)
    if protected:
        # 一条拒绝要说清三件事：撞的是哪条规则、调用方请求的是哪个路径、它实际解析到
        # 了哪里。合成一句话报出去会让模型只能看到笼统的「敏感路径」。
        raise ValueError("E_DELETE_BLOCKED: %s (requested '%s', resolved to '%s'). This deletion was blocked by the safety fence; run it manually on the server if it is genuinely intended." % (reason, to_unicode(rel), to_unicode(resolved)))
    if not os.path.lexists(lexical):
        # 不存在不是错误：删除的目的已经达成。标出来交给 op_delete_batch 报成
        # absent 槽（本地 delete 是同一套语义），同批其它路径照删。
        return {"missing": True, "lexical": lexical, "resolved": resolved, "isDir": False}
    # os.path.isdir 会跟随符号链接，而删除只作用于链接本身：链接一律按文件处理。
    is_dir = os.path.isdir(lexical) and not os.path.islink(lexical)
    if is_dir and not recursive:
        raise ValueError("path is a directory; set recursive=true: %s" % to_unicode(rel))
    return {"lexical": lexical, "resolved": resolved, "isDir": is_dir}

def execute_delete_path(prepared):
    import shutil
    if prepared["isDir"]:
        shutil.rmtree(prepared["lexical"])
    else:
        os.unlink(prepared["lexical"])
    return {"resolvedPath": prepared["resolved"]}

def op_delete_batch(root, payload):
    # 两遍：先全部判定（任一条不通过则整批一条都不删），再逐条执行（单条失败
    # 只污染自己的结果槽，与批量读的隔离契约一致）。判定阶段顺手把解析好的
    # 路径带下去，执行阶段不再重新解析，删除目标不会被第二个进程换掉。
    # 判定阶段答出来的「本来就不存在」不是失败：执行阶段跳过它，结果槽报
    # absent（与本地 delete 同一套语义），同批其它路径照删。
    recursive = bool(payload.get("recursive"))
    prepared = []
    for rel in payload.get("paths") or []:
        prepared.append((rel, check_delete_path(root, rel, recursive)))
    results = []
    for rel, item in prepared:
        if item.get("missing"):
            results.append({"path": rel, "ok": True, "absent": True})
            continue
        try:
            slot = execute_delete_path(item)
            slot["path"] = rel
            slot["ok"] = True
            results.append(slot)
        except Exception as exc:
            results.append({"path": rel, "ok": False, "error": to_unicode(exc)})
    return {"paths": results}

def check_write_targets(root, cwd, targets):
    # 镜像本地 command 的 E_PATH_OUTSIDE 策略：Go 侧已把字面写入目标
    # （重定向 + 变更命令）解析出来，这里只做远端文件系统事实检查。
    # 动态目标（变量/glob/命令替换）在 Go 侧已被过滤，不会到达这里。
    if not targets:
        return
    root_str = to_os_text(root)
    cwd_str = to_os_text(cwd)
    for t in targets:
        if not t or t.startswith("&"):
            continue
        p = to_os_text(t)
        if not os.path.isabs(p):
            p = os.path.join(cwd_str, p)
        p = os.path.normpath(p)
        if p == os.devnull:
            continue
        lexical = p
        resolved = os.path.realpath(p)
        try:
            lex_inside = is_subpath(lexical, root_str)
            res_inside = is_subpath(resolved, root_str)
        except (ValueError, OSError):
            lex_inside = False
            res_inside = False
        if lex_inside and not res_inside:
            # 词法在工作区内但解析后逃到工作区外（symlink 父目录）：
            # 与本地 inspectCommandMutationTarget 一致，无条件拦截，
            # 无论目标文件是否已存在。
            raise ValueError("E_PATH_OUTSIDE: remote command write target escapes workspaceRoot via symlink: %s" % p)
        # 解析后落在版本控制元数据里同样拒绝：.git 被链成 gh 之后的
        # gh/hooks/pre-commit 字面干净（Go 侧按字面判过一遍），解析后才露出 .git
        # ——与本地 command 用解析后路径再判一次是同一个手法。
        for candidate in (lexical, resolved):
            if contains_vcs(candidate):
                raise ValueError("E_PROTECTED_PATH: remote command write target is inside version-control metadata: %s" % p)
        if res_inside:
            continue
        if os.path.lexists(p):
            raise ValueError("E_PATH_OUTSIDE: remote command write target is outside workspaceRoot: %s" % p)

def append_command_output(out, chunk, limit):
    # Keep the real beginning and end within the existing capture budget.
    out.extend(chunk)
    if len(out) <= limit:
        return False
    head = limit // 3
    del out[head:len(out) - (limit - head)]
    return True

def op_run(root, payload):
    import select, signal, subprocess
    command = to_os_text(payload.get("command") or "")
    if not command.strip():
        raise ValueError("command is required")
    cwd = safe_join(root, payload.get("cwd", ""))
    check_write_targets(root, cwd, payload.get("targets") or [])
    if not os.path.isdir(cwd):
        raise ValueError("cwd is not a directory on the remote host: %s (cwd is resolved inside the target workspace path)" % to_unicode(cwd))
    timeout = int(payload.get("timeoutSeconds") or 120)
    if timeout < 1:
        timeout = 1
    if timeout > 600:
        timeout = 600
    shell = to_os_text(payload.get("shell") or "")
    if not shell:
        # 默认 shell：posix 用 bash/sh；Windows 没有 /bin/sh，退回 cmd.exe
        # （COMSPEC），否则 Popen 直接报找不到解释器。
        if os.name == "nt":
            shell = os.environ.get("COMSPEC") or "cmd.exe"
        else:
            shell = "/bin/bash" if os.path.exists("/bin/bash") else "/bin/sh"
    max_output = int(payload.get("maxOutput") or 131072)
    start = time.time()
    # start_new_session 与 preexec_fn=os.setsid 等价，但不会触发
    # Python 3.12+ 对 preexec_fn 的弃用警告；远端只可能是 posix。
    new_session = hasattr(os, "setsid")
    popen_kwargs = {}
    if new_session:
        if sys.version_info >= (3, 2):
            popen_kwargs["start_new_session"] = True
        else:
            popen_kwargs["preexec_fn"] = os.setsid
    devnull = getattr(subprocess, "DEVNULL", None)
    devnull_f = None
    if devnull is None:
        try:
            devnull_f = open(os.devnull, "r+b")
            devnull = devnull_f
        except Exception:
            devnull = subprocess.PIPE
    proc = subprocess.Popen(
        command,
        shell=True,
        cwd=to_os_text(cwd),
        executable=shell,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        stdin=devnull,
        **popen_kwargs
    )
    # 取消运行时 Go 侧杀掉本地 ssh 客户端，sshd 只回收直接子进程（本脚
    # 本）；start_new_session 起的命令进程组收不到信号会变成远端孤儿进
    # 程。这里捕获终止信号，先击杀整个命令进程组再退出，保证会话中断
    # 等价于远端进程同步终止。
    def _terminate_command_group(signum, frame):
        try:
            if new_session and proc.poll() is None:
                os.killpg(proc.pid, signal.SIGKILL)
        except Exception:
            pass
        sys.exit(0)
    # 信号名不是所有平台都有（Windows 没有 SIGHUP）：按名字取、缺哪个跳过
    # 哪个，不能让“取信号”这一步把 op_run 打挂。
    for _sig_name in ("SIGTERM", "SIGHUP", "SIGINT"):
        _sig = getattr(signal, _sig_name, None)
        if _sig is None:
            continue
        try:
            signal.signal(_sig, _terminate_command_group)
        except (ValueError, OSError):
            pass
    out = bytearray()
    truncated = False
    timed_out = False
    deadline = start + timeout
    eof = False
    while True:
        if time.time() > deadline:
            timed_out = True
            try:
                if new_session:
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
            if hasattr(proc, "wait") and sys.version_info >= (3, 3):
                try:
                    proc.wait(timeout=min(remain, 0.5))
                except Exception:
                    pass
            else:
                time.sleep(min(remain, 0.05))
        else:
            if os.name == "nt":
                line = proc.stdout.readline()
                if not line:
                    eof = True
                else:
                    if append_command_output(out, line, max_output):
                        truncated = True
            else:
                rlist, _, _ = select.select([proc.stdout], [], [], 0.1)
                if proc.stdout in rlist:
                    if hasattr(proc.stdout, "read1"):
                        chunk = proc.stdout.read1(8192)
                    else:
                        chunk = os.read(proc.stdout.fileno(), 8192)
                    if not chunk:
                        eof = True
                    else:
                        if append_command_output(out, chunk, max_output):
                            truncated = True
        if proc.poll() is not None:
            # 进程已退出；排空管道内剩余缓冲数据（最多等 0.5s，避免后台
            # 子进程持有 stdout fd 时无限阻塞）。
            if os.name != "nt":
                drain_deadline = time.time() + 0.5
                while not eof and time.time() < drain_deadline:
                    rlist, _, _ = select.select([proc.stdout], [], [], 0.1)
                    if not rlist or proc.stdout not in rlist:
                        break
                    if hasattr(proc.stdout, "read1"):
                        chunk = proc.stdout.read1(8192)
                    else:
                        chunk = os.read(proc.stdout.fileno(), 8192)
                    if not chunk:
                        eof = True
                        break
                    if append_command_output(out, chunk, max_output):
                        truncated = True
            break
    try:
        if hasattr(proc, "wait") and sys.version_info >= (3, 3):
            proc.wait(timeout=5)
        else:
            wait_deadline = time.time() + 5
            while time.time() < wait_deadline and proc.poll() is None:
                time.sleep(0.05)
    except Exception:
        pass
    try:
        proc.stdout.close()
    except Exception:
        pass
    if devnull_f is not None:
        try:
            devnull_f.close()
        except Exception:
            pass
    exit_code = proc.poll()
    if exit_code is None:
        # 兜底：极端情况下进程仍未退出（如 D 状态不可中断），报告 -1 而非 0
        exit_code = -1
    if timed_out:
        exit_code = -1
    duration = int((time.time() - start) * 1000)
    if truncated:
        # Drop only a split UTF-8 sequence at each cut, preserving real U+FFFD.
        import codecs
        head = max_output // 3
        head_text = codecs.getincrementaldecoder("utf-8")("replace").decode(bytes(out[:head]), final=False)
        tail = bytes(out[head:])
        for _ in range(3):
            if tail[:1] and b"\x80" <= tail[:1] <= b"\xbf":
                tail = tail[1:]
            else:
                break
        output = (head_text +
                  "\n\n[... remote output omitted ...]\n\n" +
                  tail.decode("utf-8", "replace"))
    else:
        output = bytes(out).decode("utf-8", "replace")
    return {
        "command": command,
        "cwd": to_os_text(cwd),
        "shell": shell,
        "shellPath": shell,
        "output": output,
        "exitCode": exit_code,
        "timedOut": timed_out,
        "durationMs": duration,
        "truncated": truncated,
    }

def get_raw_stdin():
    if hasattr(sys.stdin, "buffer"):
        return sys.stdin.buffer
    if os.name == "nt":
        import msvcrt
        msvcrt.setmode(sys.stdin.fileno(), os.O_BINARY)
    return sys.stdin

def get_raw_stdout():
    if hasattr(sys.stdout, "buffer"):
        return sys.stdout.buffer
    if os.name == "nt":
        import msvcrt
        msvcrt.setmode(sys.stdout.fileno(), os.O_BINARY)
    return sys.stdout

def do_upload(root, payload):
    import tarfile
    rel = payload.get("remotePath", "")
    lexical, resolved = safe_join_paths(root, rel)
    if lexical == root:
        raise ValueError("refusing to upload directly to workspace root without subpath")
    if contains_vcs(resolved) or contains_vcs(lexical):
        raise ValueError("E_PROTECTED_PATH: refusing to write VCS metadata: " + to_unicode(rel))

    overwrite = bool(payload.get("overwrite"))
    if not overwrite and os.path.lexists(resolved):
        raise ValueError("remote destination already exists: " + to_unicode(rel))

    dest_parent = os.path.dirname(resolved)
    if not os.path.exists(dest_parent):
        os.makedirs(dest_parent)

    raw_in = get_raw_stdin()
    try:
        tar = tarfile.open(mode="r|", fileobj=raw_in)
    except Exception as exc:
        raise ValueError("invalid tar stream: " + to_unicode(exc))

    total_bytes = 0
    file_count = 0
    dir_count = 0
    dir_modtimes = []

    for member in tar:
        # tarfile otherwise retains every header even in streaming mode (Python 2/3).
        tar.members[:] = []
        m_name = member.name.replace("\\", "/")
        if not m_name or m_name.startswith("/") or ".." in m_name.split("/"):
            raise ValueError("Tar-Slip violation: illegal entry: " + to_unicode(m_name))
        target_path = os.path.normpath(os.path.join(dest_parent, m_name))
        if not is_subpath(target_path, resolved):
            raise ValueError("Tar-Slip violation: entry escapes transfer destination: " + to_unicode(m_name))
        if contains_vcs(m_name):
            raise ValueError("E_PROTECTED_PATH: refusing VCS metadata: " + to_unicode(m_name))

        # Reject parent symlinks, including ones created by earlier archive entries.
        parent = os.path.dirname(target_path)
        while parent != root and is_subpath(parent, root):
            if os.path.islink(parent):
                raise ValueError("Tar-Slip violation: symlink parent: " + to_unicode(parent))
            parent = os.path.dirname(parent)
        if not is_subpath(os.path.realpath(os.path.dirname(target_path)), root):
            raise ValueError("Tar-Slip violation: parent escapes workspaceRoot: " + to_unicode(m_name))
        if not overwrite and os.path.lexists(target_path) and not (member.isdir() and os.path.isdir(target_path) and not os.path.islink(target_path)):
            raise ValueError("remote destination already exists: " + to_unicode(m_name))

        if member.isdir():
            if os.path.lexists(target_path) and (os.path.islink(target_path) or not os.path.isdir(target_path)):
                os.unlink(target_path)
            if not os.path.exists(target_path):
                os.makedirs(target_path, (member.mode & 0o777 & ~0o022) or 0o755)
            dir_modtimes.append((target_path, member.mtime))
            dir_count += 1
        elif member.isreg():
            p_dir = os.path.dirname(target_path)
            if not os.path.exists(p_dir):
                os.makedirs(p_dir)
            f_in = tar.extractfile(member)
            if f_in is None:
                raise ValueError("missing file data: " + to_unicode(m_name))
            fd, tmp_path = tempfile.mkstemp(prefix=".ally-tmp-", dir=p_dir)
            try:
                with os.fdopen(fd, "wb") as f_out:
                    while True:
                        chunk = f_in.read(65536)
                        if not chunk:
                            break
                        f_out.write(chunk)
                        total_bytes += len(chunk)
                # Never import setuid/setgid/sticky permission bits from an archive.
                mode = (member.mode & 0o777) & ~0o022
                os.chmod(tmp_path, mode or 0o644)
                if not overwrite:
                    # Atomic no-clobber publication; existence checks alone race.
                    os.link(tmp_path, target_path)
                    os.unlink(tmp_path)
                else:
                    if os.path.isdir(target_path) and not os.path.islink(target_path):
                        import shutil
                        shutil.rmtree(target_path)
                    if hasattr(os, "replace"):
                        os.replace(tmp_path, target_path)
                    else:
                        if os.name == "nt" and os.path.lexists(target_path):
                            os.remove(target_path)
                        os.rename(tmp_path, target_path)
                if member.mtime > 0:
                    os.utime(target_path, (member.mtime, member.mtime))
                file_count += 1
            finally:
                f_in.close()
                if os.path.exists(tmp_path):
                    os.unlink(tmp_path)
        elif member.issym():
            link_target = member.linkname.replace("\\", "/")
            full_link = os.path.realpath(os.path.join(os.path.dirname(target_path), link_target))
            if link_target.startswith("/") or not is_subpath(full_link, root):
                raise ValueError("Tar-Slip violation: symlink escapes workspaceRoot: " + to_unicode(m_name))
            if contains_vcs(link_target) or contains_vcs(full_link):
                raise ValueError("E_PROTECTED_PATH: refusing symlink to VCS metadata: " + to_unicode(m_name))
            if not os.path.exists(os.path.dirname(target_path)):
                os.makedirs(os.path.dirname(target_path))
            if os.path.lexists(target_path):
                os.unlink(target_path)
            os.symlink(member.linkname, target_path)
        else:
            raise ValueError("unsupported tar entry type: " + to_unicode(m_name))

    tar.close()
    for d_path, d_mtime in reversed(dir_modtimes):
        if d_mtime > 0 and os.path.isdir(d_path):
            try:
                os.utime(d_path, (d_mtime, d_mtime))
            except Exception:
                pass

    ok({"totalBytes": total_bytes, "fileCount": file_count, "dirCount": dir_count})

def do_download(root, payload):
    import tarfile
    rel = payload.get("remotePath", "")
    lexical, resolved = safe_join_paths(root, rel)
    if not os.path.lexists(resolved):
        raise ValueError("remote path does not exist: " + to_unicode(rel))
    if contains_vcs(resolved) or contains_vcs(lexical):
        raise ValueError("E_PROTECTED_PATH: refusing to read VCS metadata: " + to_unicode(rel))

    arc_root_name = payload.get("arcRootName") or os.path.basename(resolved)
    raw_out = get_raw_stdout()
    tar = tarfile.open(mode="w|", fileobj=raw_out)

    total_bytes = 0
    file_count = 0
    dir_count = 0

    def add_entry(fpath, arcname):
        # Python tarfile deduplicates hardlinks, while the Go receiver expects data
        # for each regular entry. Also avoid retaining headers for the whole tree.
        tar.inodes.clear()
        tar.add(fpath, arcname=arcname, recursive=False)
        tar.members[:] = []
        return os.lstat(fpath)

    def walk_error(exc):
        raise exc

    if os.path.isdir(resolved):
        for dirpath, dirnames, filenames in os.walk(resolved, onerror=walk_error):
            dirnames[:] = [d for d in dirnames if not contains_vcs(d)]
            rel_dir = os.path.relpath(dirpath, resolved).replace("\\", "/")
            arc_dir = arc_root_name if rel_dir == "." else arc_root_name + "/" + rel_dir
            add_entry(dirpath, arc_dir)
            dir_count += 1
            for dname in dirnames[:]:
                dpath = os.path.join(dirpath, dname)
                if os.path.islink(dpath):
                    add_entry(dpath, arc_dir + "/" + dname)
                    dirnames.remove(dname)
            for fname in filenames:
                if contains_vcs(fname):
                    continue
                fpath = os.path.join(dirpath, fname)
                st = add_entry(fpath, arc_dir + "/" + fname)
                if stat_mod.S_ISREG(st.st_mode):
                    total_bytes += st.st_size
                    file_count += 1
    else:
        st = add_entry(resolved, arc_root_name)
        if stat_mod.S_ISREG(st.st_mode):
            total_bytes += st.st_size
            file_count = 1

    tar.close()
    raw_out.flush()
    ok({"totalBytes": total_bytes, "fileCount": file_count, "dirCount": dir_count})

# op 名 → 实现：唯一的分发表，新增一个 op 只在这里加一行（实现本身仍是 op_* 函数）。
# 两个测试专用 op（_check_*）形状与其余不同，留在下面的分支里。
OPS = {
    "read": op_read,
    "read_batch": op_read_batch,
    "stat": op_stat,
    "write": op_write,
    "delete_batch": op_delete_batch,
    "run": op_run,
}

try:
    if TRANSFER_MODE:
        MARKER = "ALLY_REMOTE_TRANSFER_JSON:"
    payload = decode_payload()
    raw_root = os.path.expanduser(to_os_text(payload["workspaceRoot"]))
    # 三种拒绝各说各的，并点明这个值的来源：它来自调用方传进来的 target 路径，
    # 不是服务器状态。不点明来源与下一步，模型会以为远端异常而在同一路径上反复重试。
    if not os.path.exists(raw_root):
        raise ValueError("workspaceRoot does not exist on this host: %s (this path comes from the target argument, not from the server; use a directory that exists on the remote host, or ask the user which one to use)" % raw_root)
    root = os.path.realpath(raw_root)
    if not os.path.isdir(root):
        raise ValueError("workspaceRoot is not a directory on this host: %s (the target path must be a directory; this resolved to a file or a special file)" % root)
    if root == "/":
        raise ValueError("workspaceRoot must not be the filesystem root: give the target an absolute non-root directory such as /srv/app")
    if TRANSFER_MODE:
        action = payload.get("action", "")
        if action == "upload":
            do_upload(root, payload)
        elif action == "download":
            do_download(root, payload)
        else:
            raise ValueError("unknown transfer action: " + to_unicode(action))
    op = payload.get("op")
    handler = OPS.get(op)
    if handler is not None:
        ok(handler(root, payload))
    elif op == "_check_write_targets":
        # 测试专用内部 op：只做写目标越界检查，不执行命令。
        # cwd 同样走 safe_join，保证与 op_run 的相对路径解析一致。
        check_write_targets(root, safe_join(root, payload.get("cwd", "")), payload.get("targets") or [])
        ok({"checked": True})
    elif op == "_check_protected":
        # 测试专用内部 op：直接暴露删除保护判定，便于本地单测。
        protected, reason = is_protected_delete_path(to_os_text(payload["path"]))
        ok({"protected": protected, "reason": reason})
    else:
        raise ValueError("unknown op: %s" % op)
except Exception as exc:
    fail(to_unicode(exc))
