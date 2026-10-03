#!/usr/bin/env python3
"""核对/刷新 docs/provider-api-fields.md 对各家开放平台文档的覆盖度。

用途：文档把"抓了哪些页"当成可核验的事实。这个脚本从各家文档站**自己的**
sitemap.xml / llms.txt 重新取全量页面清单，逐条比对文档里出现的 URL，
从而回答两个问题：文档是不是还全？站点是不是新增/下架了页面？

用法（在仓库根目录执行）：
    python3 scripts/audit-provider-api-docs.py          # 只核对，打印覆盖率
    python3 scripts/audit-provider-api-docs.py --dump   # 同时把每家的规范 URL 清单
                                                        # 写到 .tmp/provider-docs/urls-<name>.txt
                                                        # （合稿时并入文档附录《各家页面清单》）

归一化规则（否则会出现假缺口）：
  - 域名别名：platform.minimaxi.com ≡ platform.minimax.cn；www 前缀忽略
  - 语言前缀 /zh-cn /zh-tw /zh-hant /zh /cn /en /ja … 一律剥离：同一页的多语言镜像
    只算一条，镜像条数单独统计，不混进缺口里（各家站点普遍带一份英文/繁中镜像）
  - Mintlify 的 .md 后缀镜像、锚点、查询串、末尾斜杠
  - HTML 转义：sitemap 里偶有 `&amp;`，先还原再比对

不计入缺口：站外域名、营销/法务/控制台页（blog、about、auth、terms…）、站点根页、
以及 sitemap 里路径含反斜杠的畸形条目。

周边页（PERIPHERAL）：客户端/IDE 接入教程、计费/套餐/发票/优惠券/席位、FAQ、更新日志/新闻、
法务与营销页——按本次合稿要求**不收进文档**，因此既不算缺口、也不写进 urls-*.txt；
单独列到 peripheral-*.txt 供人工复核。判据收口在 is_peripheral() 一处（文档侧复用同一函数）。

退出码：0 = 全覆盖；1 = 有缺口（便于 CI 或人工复核时一眼看出）。
"""
import re
import sys
import urllib.request
from collections import Counter
from pathlib import Path

UA = {"User-Agent": "Mozilla/5.0 (compatible; api-doc-audit)"}
DOC = Path("docs/provider-api-fields.md")
OUT = Path(".tmp/provider-docs")

HOST_ALIAS = {
    "platform.minimaxi.com": "platform.minimax.cn",
    "www.mimo-v2.com": "mimo-v2.com",
}
LOCALE = re.compile(
    r"^/(zh-cn|zh-tw|zh-hk|zh-hant|zh-hans|zh|cn|en-us|en|ja|ko|es|fr|de|ru|pt-br|pt|it|tr|ar|vi|th|id)(?=/|$)",
    re.I,
)
NON_DOC = re.compile(
    r"(^|/)(blog|about|auth|login|register|contact|cookie|terms|privacy|agreement|changelog|"
    r"careers|jobs|pricing|waitlist|cdn-cgi|_next)(/|$)"
)

# 周边页：按本次合稿要求不收进文档的页面（客户端接入 / 计费套餐 / FAQ / 更新日志 / 法务 / 营销）。
# 判据是**路径片段**（含前缀，因为站点把词缀在段名里，如 `faq-billing`、`news260910`），
# 不靠页面描述关键词猜；文档侧的删除名单由本函数生成，两处不会各写一份。
PERIPHERAL_TOKENS = (
    "news", "updates", "changelog", "release-notes",
    "faq", "pricing", "price",
    "bill", "billing", "invoice", "coupon", "recharge", "refund", "free-quota", "balance",
    "token-plan", "coding-plan", "m-plan", "product-plans",
    "integration", "integrations", "agent_integrations", "ai-tools", "clients-and-developer-tools",
    "claude-code", "codex", "cursor", "openclaw", "hermes", "opencode", "trae", "cline",
    "kilo", "qoder", "windsurf", "kimi-code", "hakimi", "coding-helper", "mcp-guide", "console",
    "account", "cost-optimization",
    "terms", "privacy", "agreement", "legal",
    "solution", "blog", "community", "careers", "about", "contact",
)
PERIPHERAL_EXACT = ("plan", "plans", "cli", "ide")


def is_peripheral(path: str) -> bool:
    """路径是否属于「周边页」（不看域名，只看路径片段）。"""
    for seg in path.strip("/").split("/"):
        s = seg.lower()
        if s in PERIPHERAL_EXACT:
            return True
        if any(s == t or s.startswith(t) for t in PERIPHERAL_TOKENS):
            return True
    return False

# prefer_locale：清单里同一页有多种语言版本时，优先收录哪一种（文档本身是中文站）。
SOURCES = {
    "deepseek": {
        "label": "DeepSeek",
        "hosts": ["api-docs.deepseek.com"],
        "prefer": "/zh-cn",
        "srcs": ["https://api-docs.deepseek.com/sitemap.xml"],
    },
    "zhipu-glm": {
        "label": "智谱 GLM",
        "hosts": ["docs.bigmodel.cn"],
        "prefer": "/cn",
        "srcs": ["https://docs.bigmodel.cn/sitemap.xml", "https://docs.bigmodel.cn/llms.txt"],
    },
    "kimi-moonshot": {
        "label": "Kimi / Moonshot",
        "hosts": ["platform.kimi.com"],
        "prefer": "",
        "srcs": [
            "https://platform.kimi.com/docs/sitemap.xml",
            "https://platform.kimi.com/docs/llms.txt",
        ],
    },
    "minimax": {
        "label": "MiniMax",
        "hosts": ["platform.minimax.cn"],
        "prefer": "",
        "srcs": [
            "https://platform.minimax.cn/docs/sitemap.xml",
            "https://platform.minimax.cn/docs/llms.txt",
        ],
    },
    "mimo": {
        "label": "MiMo",
        "hosts": ["mimo-v2.com"],
        "prefer": "/zh",
        "srcs": ["https://www.mimo-v2.com/sitemap.xml", "https://www.mimo-v2.com/zh/docs/llms.txt"],
    },
    "qianwen": {
        "label": "千问AI平台",
        "hosts": ["platform.qianwenai.com"],
        "prefer": "",
        # 该站没有 sitemap，全量页面清单只在 llms.txt 里（/docs/llms.txt 比根目录那份全）
        "srcs": ["https://platform.qianwenai.com/docs/llms.txt"],
        # 刻意缩小的收录范围（用户要求只抓协议核心）：只有命中这些前缀的页面算"应当覆盖"，
        # 其余约 800 个生成类/agent-infra 页面计入 out-of-scope，不算缺口。
        "include": (
            "/docs/openapi-openai-chat.json",
            "/docs/openapi-openai-responses.json",
            "/docs/openapi-anthropic.json",
            "/docs/openapi-dashscope.json",
            "/docs/api-reference/chat",
            "/docs/api-reference/more",
            "/docs/developer-guides/text-generation",
            "/docs/developer-guides/tool-calling",
            "/docs/developer-guides/getting-started",
            "/docs/developer-guides/accuracy-tuning",
            "/docs/developer-guides/clients-and-developer-tools",
            "/docs/developer-guides/third-party-models",
            "/docs/developer-guides/run-and-scale",
            "/docs/token-plan/",
            "/docs/resources/",
            "/docs/changelog/",
        ),
        "scope_note": (
            "本次只收录**协议核心**（四套协议 OpenAPI：openai-chat / openai-responses / anthropic / dashscope，"
            "以及 chat、文本生成、工具调用、思考、准确率调优、客户端与第三方模型、规模化等指南）"
            "外加 Token Plan、资源与更新日志；其余约 800 个页面（视频/图像/语音生成、world-model、"
            "agent-infra 的 managed-agents/rag/memory/sandbox 等）按用户要求**不在本次收录范围**，"
            "需要时重跑本脚本并扩大 include 前缀即可。"
        ),
    },
}


def fetch(url: str) -> str:
    try:
        req = urllib.request.Request(url, headers=UA)
        with urllib.request.urlopen(req, timeout=30) as resp:
            return resp.read().decode("utf-8", "replace")
    except Exception as exc:  # noqa: BLE001 - 拉不到就如实报告
        return f"__ERROR__ {exc}"


def sitemap_urls(url: str, depth: int = 0, seen: set[str] | None = None) -> list[str]:
    """取一个 sitemap 的全部 URL（sitemap index 递归展开）。"""
    if seen is None:
        seen = set()
    if url in seen or depth > 2:
        return []
    seen.add(url)
    body = fetch(url)
    if body.startswith("__ERROR__"):
        print(f"  [warn] 拉取失败 {url}: {body}", file=sys.stderr)
        return []
    locs = re.findall(r"<loc>\s*([^<\s]+)\s*</loc>", body)
    if "<sitemapindex" in body:
        out: list[str] = []
        for loc in locs:
            out += sitemap_urls(loc, depth + 1, seen)
        return out
    return locs


def split_host(u: str) -> tuple[str, str]:
    u = re.sub(r"^https?://", "", u.strip(), flags=re.I)
    u = u.replace("&amp;", "&").replace("&quot;", '"')
    u = u.split("#")[0]
    u = re.sub(r"\?.*$", "", u)
    u = u.rstrip(".,);]'\"")
    host, _, path = u.partition("/")
    return HOST_ALIAS.get(host.lower(), host.lower()), "/" + path


def norm(u: str) -> str:
    """比对用的归一化键（剥语言前缀、.md 镜像、大小写）。"""
    host, path = split_host(u)
    if path.endswith(".md"):
        path = path[:-3]
    return f"{host}{LOCALE.sub('', path).rstrip('/')}"


def doc_url_set(path: Path) -> set[str]:
    text = path.read_text(encoding="utf-8")
    # 正文里的引用允许写成不带 scheme 的 "host/path"，先补 scheme 再提取。
    # lookbehind 必须排除 `.`、`-`：否则会从 "platform.kimi.com/" 内部再次命中
    # "kimi.com/" 并把 URL 从中间截断（`.tmp` 里那版就踩过这个坑）。
    text = re.sub(r"(?<![\w:/.\\-])((?:[a-z0-9-]+\.)+[a-z]{2,}/)", r"https://\1", text, flags=re.I)
    return {norm(u) for u in re.findall(r"https?://[^\s)\]<>\"'|`]+", text)}


def main() -> int:
    dump = "--dump" in sys.argv
    doc_urls = doc_url_set(DOC)
    print(f"文档 {DOC} 中出现 {len(doc_urls)} 个归一化 URL\n")

    summary, gaps = [], 0
    for name, cfg in SOURCES.items():
        raw: set[str] = set()
        for src in cfg["srcs"]:
            if src.endswith(".xml"):
                raw |= set(sitemap_urls(src))
            else:
                body = fetch(src)
                if body.startswith("__ERROR__"):
                    print(f"  [warn] 拉取失败 {src}: {body}", file=sys.stderr)
                    continue
                raw |= set(re.findall(r"https?://[^\s)\]<>\"'`]+", body))

        pages, non_doc, offsite, locales, out_of_scope, peripheral = set(), set(), 0, 0, 0, set()
        canon: dict[str, str] = {}
        prefer = cfg["prefer"]
        include = cfg.get("include")
        for u in raw:
            host, path = split_host(u)
            if host not in cfg["hosts"] or "\\" in path:
                offsite += 1
                continue
            if LOCALE.match(path):
                locales += 1
            key = norm(u)
            # 规范 URL：剥掉语言段与 .md 镜像后再补回本家首选语言，使清单里的每一条
            # 都是可以直接打开的中文站页面地址（站点 sitemap 给的原形不一定能访问，
            # 例如智谱的真实地址带 /cn/、MiMo 的心跳页在 /zh/ 下）。
            base = LOCALE.sub("", path)
            if base.endswith(".md"):
                base = base[:-3]
            base = base.rstrip("/")
            if prefer and not base.startswith(prefer):
                base = prefer + base
            if NON_DOC.search(path) or base in ("", prefer):
                non_doc.add(key)
                continue
            # 周边页：本次不收进文档，也不计入缺口（文档侧用同一个 is_peripheral 生成删除名单）
            if is_peripheral(base):
                peripheral.add(key)
                continue
            # 收窄收录范围的厂商：不在 include 前缀内的页面不计入缺口
            # （key 形如 host/path，切掉 host 后要补回路径开头的 / 再比前缀）
            if include and not any(("/" + key.split("/", 1)[1]).startswith(p) for p in include):
                out_of_scope += 1
                continue
            pages.add(key)
            canon.setdefault(key, f"https://{host}{base}")

        missing = sorted(u for u in pages if u not in doc_urls)
        covered = len(pages) - len(missing)
        pct = 100.0 * covered / len(pages) if pages else 0.0
        gaps += len(missing)
        OUT.mkdir(parents=True, exist_ok=True)
        OUT.joinpath(f"missing-{name}.txt").write_text("\n".join(missing) + "\n", encoding="utf-8")
        OUT.joinpath(f"peripheral-{name}.txt").write_text(
            "\n".join(sorted(peripheral)) + "\n", encoding="utf-8"
        )
        urls = sorted(url for key, url in canon.items() if key in pages)
        OUT.joinpath(f"urls-{name}.txt").write_text("\n".join(urls) + "\n", encoding="utf-8")
        summary.append((cfg["label"], len(pages), covered, len(missing), pct))

        print(f"== {name}（{cfg['label']}） ==")
        print(f"   文档页 {len(pages)} 条 → 已覆盖 {covered}，缺 {len(missing)}（覆盖率 {pct:.1f}%）")
        print(f"   另：多语言镜像 {locales} 条、非文档页 {len(non_doc)} 条、站外/畸形 {offsite} 条（未计入）")
        if include:
            print(f"   本次收录范围外 {out_of_scope} 条（该家刻意缩小的范围，不计入缺口）")
            OUT.joinpath(f"scope-{name}.txt").write_text(cfg.get("scope_note", "") + "\n", encoding="utf-8")
        if peripheral:
            print(f"   周边页 {len(peripheral)} 条（客户端接入/计费/FAQ/更新日志/法务，本次不收，不计入缺口）")
        if dump:
            print(f"   已写出 {len(urls)} 条规范 URL → .tmp/provider-docs/urls-{name}.txt")
        groups = Counter("/".join(u.split("/")[:3]) for u in missing)
        for g, c in groups.most_common(15):
            print(f"     {c:>4}  {g}")
        print()

    print("== 汇总 ==")
    print(f"{'厂商':<20}{'文档页':>7}{'已覆盖':>8}{'缺失':>7}{'覆盖率':>9}")
    for label, total, covered, miss, pct in summary:
        print(f"{label:<20}{total:>7}{covered:>8}{miss:>7}{pct:>8.1f}%")
    tot = sum(s[1] for s in summary)
    cov = sum(s[2] for s in summary)
    print(f"合计：文档页 {tot} 条，已覆盖 {cov} 条，缺 {tot - cov} 条（{100.0 * cov / tot:.1f}%）")
    return 0 if gaps == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
