# -*- coding: utf-8 -*-
from __future__ import print_function
import base64, errno, hashlib, json, os, stat as stat_mod, sys, tempfile, time

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

def as_posix_path(path):
    return path.replace("\\", "/")

def remote_path_paths(raw):
    # Absolute remote paths are independent of the local workspace and target.
    # Keep both forms: deletion unlinks the requested link, protection checks
    # also inspect its destination on the remote filesystem.
    raw = to_os_text(raw or "")
    if "\x00" in raw:
        raise ValueError("path contains NUL byte")
    if not raw or not os.path.isabs(raw):
        raise ValueError("remote path must be absolute")
    lexical = os.path.normpath(raw)
    return lexical, os.path.realpath(lexical)

def remote_path(raw):
    return remote_path_paths(raw)[1]

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

def read_raw_file(rel, max_bytes, remaining_bytes=None, tail_lines=0):
    path = remote_path(rel)
    try:
        st = os.stat(path)
    except (OSError, IOError) as exc:
        if getattr(exc, "errno", None) in (errno.ENOENT, errno.ENOTDIR):
            raise ValueError("E_PATH_NOT_FOUND: file does not exist: '%s'" % to_unicode(rel))
        raise
    if stat_mod.S_ISDIR(st.st_mode):
        raise ValueError("E_IS_DIRECTORY: path is a directory, not a file: '%s'" % to_unicode(rel))
    if not stat_mod.S_ISREG(st.st_mode):
        raise ValueError("E_NOT_REGULAR_FILE: path is not a regular file: '%s'" % to_unicode(rel))
    is_tail = st.st_size > max_bytes and tail_lines > 0
    if st.st_size > max_bytes and not is_tail:
        raise ValueError("E_FILE_TOO_LARGE: file is too large: '%s' is %d bytes (limit %d); use tailLines for UTF-8 logs or a read-only command to filter it" % (to_unicode(rel), st.st_size, max_bytes))
    # Batch deferral happens after path/type/size checks. Do not read and base64
    # encode a file that cannot fit this response and will be retried anyway.
    if remaining_bytes is not None and min(st.st_size, 131072 if is_tail else max_bytes) > remaining_bytes:
        return None
    with open(path, "rb") as f:
        # stat 与 read 之间文件可能被追加，按实际读取字节数兜底判定，
        # 防止增长中的文件绕过尺寸预算。
        if is_tail:
            header = f.read(2)
            if header in (b"\xff\xfe", b"\xfe\xff"):
                raise ValueError("E_FILE_TOO_LARGE: large-file tails require UTF-8; convert this UTF-16 log first")
            f.seek(max(0, st.st_size - 131072))
            data = f.read(131072)
            for _ in range(3):
                if data[:1] and b"\x80" <= data[:1] <= b"\xbf": data = data[1:]
                else: break
        else:
            data = f.read(max_bytes + 1)
    if len(data) > max_bytes:
        raise ValueError("E_FILE_TOO_LARGE: file is too large: '%s' grew past the limit while it was being read (%d bytes, limit %d); retry with tailLines for a log" % (to_unicode(rel), len(data), max_bytes))
    if remaining_bytes is not None and len(data) > remaining_bytes:
        return None
    b64 = base64.b64encode(data)
    if hasattr(b64, "decode"):
        b64 = b64.decode("ascii")
    return {
        "path": as_posix_path(path),
        "dataBase64": to_os_text(b64),
        "size": st.st_size if is_tail else len(data),
        "tail": is_tail,
        "readBytes": len(data),
        "mode": st.st_mode & 0o777,
        "modTime": iso_mtime(st),
    }

def op_read(payload):
    return read_raw_file(payload.get("path", ""), int(payload.get("maxBytes") or 2097152), tail_lines=int(payload.get("tailLines") or 0))

def op_stat(payload):
    # 只回元数据：read_raw_file 对超限文件与目录直接报错，拿不到“是否存在”，
    # 而覆盖审批与 created 报告都必须建立在真实的存在性上。
    path = remote_path(payload.get("path", ""))
    try:
        st = os.stat(path)
    except (OSError, IOError) as exc:
        if getattr(exc, "errno", None) not in (errno.ENOENT, errno.ENOTDIR):
            raise
        return {"path": as_posix_path(path), "exists": False, "isDir": False, "size": 0}
    return {
        "path": as_posix_path(path),
        "exists": True,
        "isDir": bool(stat_mod.S_ISDIR(st.st_mode)),
        "size": st.st_size,
        "mode": st.st_mode & 0o7777,
        "modTime": iso_mtime(st),
    }

def op_read_batch(payload):
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
            data = read_raw_file(rel, max_bytes, total_bytes - used if used else None, int((payload.get("tails") or {}).get(rel) or 0))
        except Exception as exc:
            item["error"] = to_unicode(exc)
            files.append(item)
            continue
        if data is None:
            remaining.append(rel)
            continue
        used += int(data["readBytes"])
        item["ok"] = True
        item["data"] = data
        files.append(item)
    return {"files": files, "remaining": remaining}

def op_write(payload):
    lexical, resolved = remote_path_paths(payload.get("path", ""))
    path = resolved
    expected = payload.get("expectedSHA256") or ""
    def check_expected():
        if expected:
            try:
                with open(path, "rb") as source:
                    current = source.read(33554433)
            except (OSError, IOError):
                raise ValueError("E_EDIT_CONFLICT: remote file disappeared before commit; inspect the current contents before retrying")
            if len(current) > 33554432 or hashlib.sha256(current).hexdigest() != expected:
                raise ValueError("E_EDIT_CONFLICT: remote file changed before commit; inspect the current contents before retrying")
    check_expected()
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
        # 统计实际缺失的父目录链（外层→内层，绝对路径），供调用方展示；
        # 必须在 mkdir 之前探测，mkdir 失败时整个 op 报错、列表无意义。
        cur = parent
        while cur and not os.path.exists(cur):
            created_dirs.append(as_posix_path(cur))
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
        if expected and len(data) > 33554432:
            raise ValueError("E_FILE_TOO_LARGE: edited file exceeds the 32 MiB text limit")
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
        check_expected()
        replace_file(tmp, path)
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
        "path": as_posix_path(path),
        "size": st.st_size,
        "mode": st.st_mode & 0o7777,
        "modTime": iso_mtime(st),
        "createdDirs": created_dirs,
    }

def check_delete_path(rel, recursive):
    # 单条路径删除的全部判定，不执行任何东西。单路径与批量共用这一份：批量在
    # 动手之前逐条跑完，任一条不通过就整批不删。Go 侧已判过绝对路径、
    # 文件系统根，这里判只有远端才知道的事实：是否目录、
    # 是否系统敏感目标；存在性只记录不拒绝（见下）。
    #
    # 两套路径各管一头：保护判定用 resolved（符号链接指向系统目录也
    # 要拦住），实际删除用 lexical —— 删符号链接删的是链接本身，不是它指向的
    # 东西，与本地 delete 一致（旧实现按解析后的路径删，实测会把链接指向的
    # 文件/目录抹掉）。
    # 错误文案带上路径：批量里不点名的话，模型不知道是哪一条被拒。
    lexical, resolved = remote_path_paths(rel)
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

def op_delete_batch(payload):
    # 两遍：先全部判定（任一条不通过则整批一条都不删），再逐条执行（单条失败
    # 只污染自己的结果槽，与批量读的隔离契约一致）。判定阶段顺手把解析好的
    # 路径带下去，执行阶段不再重新解析，删除目标不会被第二个进程换掉。
    # 判定阶段答出来的「本来就不存在」不是失败：执行阶段跳过它，结果槽报
    # absent（与本地 delete 同一套语义），同批其它路径照删。
    recursive = bool(payload.get("recursive"))
    prepared = []
    for rel in payload.get("paths") or []:
        prepared.append((rel, check_delete_path(rel, recursive)))
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

def append_command_output(out, chunk, limit):
    # Keep the real beginning and end within the existing capture budget.
    out.extend(chunk)
    if len(out) <= limit:
        return False
    head = limit // 3
    del out[head:len(out) - (limit - head)]
    return True

def open_command_log():
    directory = os.path.join(os.path.expanduser("~"), ".ally_agent", "command-logs")
    if not os.path.exists(directory):
        try: os.makedirs(directory)
        except OSError:
            if not os.path.isdir(directory): raise
    logs = []
    active = 0
    for name in os.listdir(directory):
        if name.startswith("ally-run-") and name.endswith(".part"):
            p = os.path.join(directory, name)
            if time.time() - os.lstat(p).st_mtime > 86400:
                os.unlink(p)
            else:
                active += 1
        if name.startswith("ally-run-") and name.endswith(".log"):
            p = os.path.join(directory, name)
            st = os.lstat(p)
            if stat_mod.S_ISREG(st.st_mode): logs.append((st.st_mtime, p, st.st_size))
    logs.sort()
    used = sum(item[2] for item in logs)
    # Include active writers and the new sink in both budgets before choosing
    # completed files to evict. Active reservations alone may exhaust capacity;
    # in that case deleting completed evidence cannot make this request fit.
    reserved = (active + 1) * 67108864
    if reserved > 536870912 or active + 1 > 128:
        raise ValueError("command log quota reached; active logs retained")
    count = len(logs) + active + 1
    for modified, p, size in logs:
        if used + reserved <= 536870912 and count <= 128 and time.time() - modified < 86400: break
        os.unlink(p)
        used -= size
        count -= 1
    if used + reserved > 536870912 or count > 128:
        raise ValueError("command log quota reached; active logs retained")
    fd, name = tempfile.mkstemp(prefix="ally-run-", suffix=".part", dir=directory)
    return os.fdopen(fd, "wb"), name

def op_run(payload):
    import select, signal, subprocess, threading
    command = to_os_text(payload.get("command") or "")
    if not command.strip():
        raise ValueError("command is required")
    cwd = remote_path(payload.get("cwd") or os.path.expanduser("~"))
    if not os.path.isdir(cwd):
        raise ValueError("cwd is not a directory on the remote host: %s" % to_unicode(cwd))
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
    # Python 3.12+ 对 preexec_fn 的弃用警告；Windows 使用自己的进程树清理。
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
    control = globals().get("ALLY_RUN_STDIN")
    cancelled = threading.Event()
    # stdin EOF 是主动取消通知；信号只设置标志，清理统一走 finally，
    # 不在信号回调中退出，以免跳过进程回收和日志关闭。阻塞读取放在
    # daemon 线程中：Windows select 只能监听 socket，不能监听 SSH 管道。
    def watch_control():
        # 不占用 sys.stdin BufferedReader 的锁，否则正常退出时 daemon
        # 仍在等待 EOF，会触发 Python 的 _enter_buffered_busy 致命错误。
        while os.read(control.fileno(), 1):
            pass
        cancelled.set()
    if control is not None:
        control_thread = threading.Thread(target=watch_control)
        control_thread.daemon = True
        control_thread.start()
        if cancelled.is_set():
            if devnull_f is not None:
                devnull_f.close()
            raise ValueError("E_CANCELLED: remote command cancelled before start")
    def _terminate_command_group(signum, frame):
        cancelled.set()
    saved_signals = {}
    # 信号名不是所有平台都有（Windows 没有 SIGHUP）：按名字取、缺哪个跳过
    # 哪个，不能让“取信号”这一步把 op_run 打挂。
    for _sig_name in ("SIGTERM", "SIGHUP", "SIGINT"):
        _sig = getattr(signal, _sig_name, None)
        if _sig is None:
            continue
        try:
            saved_signals[_sig] = signal.signal(_sig, _terminate_command_group)
        except (ValueError, OSError):
            pass
    try:
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
    except BaseException:
        for sig, previous in saved_signals.items():
            signal.signal(sig, previous)
        if devnull_f is not None:
            devnull_f.close()
        raise
    def command_exited():
        if not new_session:
            return proc.poll() is not None
        # 清理前不 reap shell：即使它先退出，PID/PGID 也仍由本次调用占有，
        # 可安全清理存活子进程，不会误杀复用了相同 ID 的其他任务。
        if hasattr(os, "waitid") and hasattr(os, "WNOWAIT"):
            return os.waitid(os.P_PID, proc.pid, os.WEXITED | os.WNOHANG | os.WNOWAIT) is not None
        # 远端只支持 Linux；旧 Python 没有 waitid，用有界 /proc 状态读取，
        # 不扫描进程表，也不按进程名匹配。
        with open("/proc/%d/stat" % proc.pid, "rb") as status:
            state = status.read(4096).rsplit(b")", 1)[1].strip()[:1]
        return state in (b"Z", b"X")
    def stop_command():
        if os.name == "nt":
            # terminate/kill 只杀 cmd.exe，会留下持有 stdout 的子进程。
            # taskkill /T 清理整个命令树，避免取消后 reader 仍卡在管道上。
            startup = subprocess.STARTUPINFO()
            startup.dwFlags |= subprocess.STARTF_USESHOWWINDOW
            startup.wShowWindow = 0
            subprocess.call(["taskkill", "/PID", str(proc.pid), "/T", "/F"],
                            stdout=devnull, stderr=devnull, startupinfo=startup)
            if proc.poll() is None:
                proc.kill()
            return
        try:
            if new_session:
                os.killpg(proc.pid, signal.SIGTERM)
            else:
                proc.terminate()
        except OSError as exc:
            if exc.errno == errno.ESRCH:
                return
            raise
        # 给 TERM 半秒收尾，再清理剩余进程；不能因 shell 已退出而跳过。
        time.sleep(0.5)
        try:
            if new_session:
                os.killpg(proc.pid, signal.SIGKILL)
            else:
                proc.kill()
        except OSError as exc:
            if exc.errno != errno.ESRCH:
                raise
    out = bytearray()
    log_file = [None]
    log_path = [""]
    log_bytes = [0]
    log_error = [""]
    total_output = [0]
    model_limit = int(payload.get("modelOutputBytes") or 12288)
    def capture(chunk):
        if not log_error[0]:
            try:
                if log_file[0] is None and total_output[0] + len(chunk) > model_limit:
                    log_file[0], log_path[0] = open_command_log()
                    prefix = bytes(out)
                    log_file[0].write(prefix)
                    log_bytes[0] = len(prefix)
                if log_file[0] is not None:
                    room = 67108864 - log_bytes[0]
                    piece = chunk[:max(0, room)]
                    log_file[0].write(piece)
                    log_bytes[0] += len(piece)
                    if len(piece) < len(chunk): raise ValueError("command log reached the 64 MiB limit")
            except Exception as exc:
                log_error[0] = to_unicode(exc)
        total_output[0] += len(chunk)
        return append_command_output(out, chunk, max_output)
    truncated = False
    timed_out = False
    deadline = start + timeout
    eof = False
    if os.name == "nt":
        try:
            import queue
        except ImportError:
            import Queue as queue
        # 有界队列避免高输出命令无界占用内存；主线程不再阻塞 readline，
        # 静默命令和没有换行的输出也能响应取消与超时。
        output_queue = queue.Queue(8)
        def read_output():
            try:
                while True:
                    chunk = os.read(proc.stdout.fileno(), 8192)
                    output_queue.put(chunk)
                    if not chunk:
                        break
            except Exception as exc:
                output_queue.put(exc)
        output_thread = threading.Thread(target=read_output)
        output_thread.daemon = True
        output_thread.start()
    try:
        while True:
            if cancelled.is_set():
                break
            if time.time() > deadline:
                timed_out = True
                break
            if os.name == "nt":
                if eof:
                    time.sleep(0.05)
                else:
                    try:
                        chunk = output_queue.get(timeout=0.1)
                    except queue.Empty:
                        continue
                    if isinstance(chunk, Exception):
                        raise chunk
                    if not chunk:
                        eof = True
                    elif capture(chunk):
                        truncated = True
            else:
                watched = [] if eof else [proc.stdout]
                rlist, _, _ = select.select(watched, [], [], 0.1)
                if cancelled.is_set():
                    break
                if proc.stdout in rlist:
                    if hasattr(proc.stdout, "read1"):
                        chunk = proc.stdout.read1(8192)
                    else:
                        chunk = os.read(proc.stdout.fileno(), 8192)
                    if not chunk:
                        eof = True
                    else:
                        if capture(chunk):
                            truncated = True
            if command_exited() and (os.name != "nt" or eof):
                # 进程已退出；排空剩余输出，仍同时接收取消通知。
                if os.name != "nt":
                    drain_deadline = time.time() + 0.5
                    while not eof and time.time() < drain_deadline:
                        watched = [proc.stdout]
                        rlist, _, _ = select.select(watched, [], [], 0.1)
                        if cancelled.is_set() or proc.stdout not in rlist:
                            break
                        if hasattr(proc.stdout, "read1"):
                            chunk = proc.stdout.read1(8192)
                        else:
                            chunk = os.read(proc.stdout.fileno(), 8192)
                        if not chunk:
                            eof = True
                            break
                        if capture(chunk):
                            truncated = True
                break
    finally:
        try:
            if cancelled.is_set() or timed_out or sys.exc_info()[0] is not None:
                stop_command()
        finally:
            for sig, previous in saved_signals.items():
                signal.signal(sig, previous)
            try:
                if sys.version_info >= (3, 3):
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
            if log_file[0] is not None:
                try:
                    log_file[0].close()
                except Exception as exc:
                    log_error[0] = to_unicode(exc)
    exit_code = proc.poll()
    if exit_code is None:
        # 兜底：极端情况下进程仍未退出（如 D 状态不可中断），报告 -1 而非 0
        exit_code = -1
    if timed_out or cancelled.is_set():
        exit_code = -1
    duration = int((time.time() - start) * 1000)
    if log_file[0] is not None:
        try:
            final_path = log_path[0][:-5] + ".log"
            os.rename(log_path[0], final_path)
            log_path[0] = final_path
        except Exception as exc:
            log_error[0] = to_unicode(exc)
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
        "outputFilePath": as_posix_path(log_path[0]) if log_path[0] else "",
        "outputFileBytes": log_bytes[0],
        "outputFileIncomplete": bool(log_error[0]),
        "outputWarning": ("Full remote output could not be retained: " + log_error[0][:512] + ". Do not repeat side effects to recover logs.") if log_error[0] else "",
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

def replace_file(source, target):
    # Never delete the original before a replacement that might fail.
    if hasattr(os, "replace"):
        os.replace(source, target)
    else:
        os.rename(source, target)

def copy_file_bytes(source, destination, size):
    remaining = size
    while remaining:
        chunk = source.read(min(65536, remaining))
        if not chunk:
            raise ValueError("incomplete file stream")
        destination.write(chunk)
        remaining -= len(chunk)

def transfer_file_stats(info):
    return {"totalBytes": info.st_size, "mode": info.st_mode & 0o777, "modTime": int(info.st_mtime)}

def do_upload(payload):
    lexical, target = remote_path_paths(payload.get("remotePath", ""))
    if target == os.path.dirname(target):
        raise ValueError("refusing transfer directly targeting the filesystem root")
    overwrite = bool(payload.get("overwrite"))
    if not overwrite and os.path.lexists(lexical):
        raise ValueError("E_EXISTS: remote destination already exists; set overwrite=true to replace it")
    if os.path.lexists(target):
        if not stat_mod.S_ISREG(os.lstat(target).st_mode):
            raise ValueError("upload destination must be a file path")
    size = int(payload.get("totalBytes", -1))
    if size < 0:
        raise ValueError("missing or invalid file size")
    mode = (int(payload.get("mode", 0o644)) & 0o777 & ~0o022) or 0o644
    mtime = int(payload.get("modTime", 0))
    parent = os.path.dirname(target)
    if not os.path.exists(parent):
        os.makedirs(parent)
    fd, tmp_path = tempfile.mkstemp(prefix=".ally-tmp-", dir=parent)
    try:
        with os.fdopen(fd, "wb") as output:
            raw_in = get_raw_stdin()
            copy_file_bytes(raw_in, output, size)
            if raw_in.read(1):
                raise ValueError("unexpected bytes after file stream")
            output.flush()
            os.fsync(output.fileno())
        os.chmod(tmp_path, mode)
        if mtime > 0:
            os.utime(tmp_path, (mtime, mtime))
        outcome = transfer_file_stats(os.stat(tmp_path))
        if not overwrite:
            # Publish without clobbering a concurrently created destination.
            try:
                if os.name == "nt":
                    # Windows rename never replaces a destination and also
                    # works on FAT and shares without hard-link support.
                    os.rename(tmp_path, target)
                else:
                    os.link(tmp_path, target)
                    os.unlink(tmp_path)
            except OSError as exc:
                if exc.errno == errno.EEXIST:
                    raise ValueError("E_EXISTS: remote destination already exists; set overwrite=true to replace it")
                if exc.errno in (errno.EPERM, errno.ENOSYS, getattr(errno, "EOPNOTSUPP", errno.EPERM)):
                    raise ValueError("E_UNSUPPORTED: remote filesystem cannot publish without overwrite; use a filesystem with hard-link support, or set overwrite=true if replacement is intended")
                raise
        else:
            replace_file(tmp_path, target)
    finally:
        if os.path.exists(tmp_path):
            os.unlink(tmp_path)
    ok(outcome)

def do_download(payload):
    lexical, source = remote_path_paths(payload.get("remotePath", ""))
    if source == os.path.dirname(source):
        raise ValueError("refusing transfer directly targeting the filesystem root")
    if not stat_mod.S_ISREG(os.lstat(lexical).st_mode):
        raise ValueError("download source must be a regular file; directories and symlinks are not supported")
    # Nonblocking open avoids hanging if a regular file is replaced by a FIFO.
    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0) | getattr(os, "O_BINARY", 0)
    fd = os.open(source, flags)
    with os.fdopen(fd, "rb") as input_file:
        info = os.fstat(input_file.fileno())
        if not stat_mod.S_ISREG(info.st_mode):
            raise ValueError("download source must be a regular file")
        raw_out = get_raw_stdout()
        copy_file_bytes(input_file, raw_out, info.st_size)
        raw_out.flush()
    ok(transfer_file_stats(info))

# op 名 → 实现：唯一的分发表，新增一个 op 只在这里加一行（实现本身仍是 op_* 函数）。
# 测试专用 _check_protected 留在下面的分支里。
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
    if TRANSFER_MODE:
        action = payload.get("action", "")
        if action == "upload":
            do_upload(payload)
        elif action == "download":
            do_download(payload)
        else:
            raise ValueError("unknown transfer action: " + to_unicode(action))
    op = payload.get("op")
    handler = OPS.get(op)
    if handler is not None:
        ok(handler(payload))
    elif op == "_check_protected":
        # 测试专用内部 op：直接暴露删除保护判定，便于本地单测。
        protected, reason = is_protected_delete_path(to_os_text(payload["path"]))
        ok({"protected": protected, "reason": reason})
    else:
        raise ValueError("unknown op: %s" % op)
except Exception as exc:
    fail(to_unicode(exc))
