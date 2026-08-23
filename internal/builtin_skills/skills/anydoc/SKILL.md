---
name: anydoc
description: 通过 anydoc CLI（npx @firecrawl/anydoc）把 doc/docx/docm/ppt/pps/pot/pptx/pptm/ppsx/ppsm/xls/xlsx/xlsm/xlsb/odt/ods/odp/rtf/epub/csv/pdf 转 Markdown，保留表格、标题、脚注、备注等结构，公式转 LaTeX。无需安装，本地转换。
type: document-conversion
whenToUse: 用户要求把办公文档转换为 Markdown，或需要保留表格/标题树/脚注/公式等结构的文档内容提取、内置 read 的扁平文本抽取不够用时。注意：仅简单查阅 .docx/.pptx/.xlsx/.pdf 内容时优先直接用内置 read 工具，不要为普通阅读请求加载本 skill 增加转换开销。
---

# AnyDoc Skill

通过 anydoc CLI 把办公文档转成干净的 GitHub-Flavored Markdown。所有命令都经 `command` 工具执行，cwd 为当前 workspace，使用 bash 语法（Windows 上是 Git Bash）。CLI 参数以 `anydoc --help` 为准，版本可能变化，不要凭记忆猜参数；本 skill 只补充 help 不会说明的 Ally 集成约定。

## 1. 运行方式（无需安装）

anydoc 经 `npx` 按需运行，需要 **Node 20+**（不装全局包、不弹安装确认；首次运行 npx 自动下载平台二进制，之后走缓存）：

```bash
npx -y @firecrawl/anydoc report.docx              # Markdown 输出到 stdout
npx -y @firecrawl/anydoc slides.pptx -o slides.md # 写入文件
npx -y @firecrawl/anydoc - --format csv < d.csv   # 从 stdin 读
```

`-y` 让 npx 跳过交互确认（命令在非交互 shell 里跑，缺了会卡住）。首次运行有下载延迟属正常，不要误判为失败重试。若用户环境没有 Node 20+，如实报告版本要求，由用户决定是否升级；用户拒绝时不转换，仅对 `.docx/.pptx/.xlsx/.pdf` 可回退 Ally 内置 `read` 工具的简单文本抽取（丢表格结构，仅保底）。

## 2. 支持格式与能力

支持 21 种扩展名：Word（`.doc` `.docx` `.docm`）、PowerPoint（`.ppt` `.pps` `.pot` `.pptx` `.pptm` `.ppsx` `.ppsm`）、Excel（`.xls` `.xlsx` `.xlsm` `.xlsb`）、OpenDocument（`.odt` `.ods` `.odp`）、`.rtf` `.epub` `.csv` `.pdf`（文本型）。

保留结构：标题（带锚点）、加粗/斜体/删除线、行内代码与代码块、链接、嵌套/任务列表（保留原文编号）、表格（含合并单元格和表头行）、引用块、脚注/尾注、PPT 演讲者备注；Word/PPT 的 OMML、ODF/EPUB 的 MathML 公式转为 GitHub 风格 LaTeX（`$...$` / `$$...$$`）；嵌入图片输出 alt 文本，外链图片保持为 Markdown 图片。

`--format` 的规范名只有：`doc, docx, odt, pdf, ppt, pptx, rtf, epub, xlsx, ods, odp, csv`（`xls`/`docm`/`ppsx` 等别名会归一到这些）。

## 3. 退出码与错误（CLI 从不交互提示）

- `0` 成功；`1` 文档无法读取或转换；`2` 用法错误（未知选项、缺输入、非法 `--format`）
- 失败只在 stderr 打一行 `anydoc: <message>`；转换结果仅当"没有任何有意义的 Markdown 可产出"时才报错
- 常见错误：`unsupported`（未知格式或纯图片 PDF）、`encrypted`（加密/带密码文档，拒绝处理）、`malformed`（结构损坏、无可提取内容）、资源超限（zip 炸弹/深嵌套防护）

## 4. Ally 集成注意事项

- **路径**：传给 anydoc 的文件路径用 forward slash。Windows 绝对路径在 Git Bash 里写成 `/c/Users/...` 或 `"C:/Users/..."`；workspace 相对路径直接用。
- **格式检测看内容不看扩展名**：默认不传 `--format`，让内容检测生效（扩展名标错的文件通常也能转对）。仅当检测无法工作时显式传入：stdin 读入的 CSV（无内容签名、无扩展名可依）；文件缺失或标错扩展名且转换报错时，补 `--format <规范名>` 重试一次。
- **大文件**：优先 `-o out.md` 落盘到 workspace，再用 `read` 工具分段读取，不要把全部输出流进上下文。
- **网络输入**：可用管道 `curl -s <url> | npx -y @firecrawl/anydoc -` 直接转换远端文档；大 PDF 同样建议落盘再转。
- **与内置 read 的关系**：Ally 的 `read` 工具本身能读 `.docx/.pptx/.xlsx/.pdf`，但只做扁平文本抽取（表格结构丢失）。需要结构保真时才走本 skill。
- **代码集成**：若要在用户项目的代码里使用（而非本 skill 的命令行调用），官方建议优先用库而不是 shell 调用：npm `@firecrawl/anydoc`、PyPI `firecrawl-anydoc`、crates.io `anydoc`，三端 API 一致（`toMarkdown` / `to_markdown`）。
- **单向转换**：anydoc 只做文档 → Markdown，不支持反向生成 docx 等。

## 5. 错误恢复速查

- npx 卡住/首次慢 → 检查 Node 版本 ≥ 20；首次下载属正常，不要静默重试。
- `unsupported`（纯图片/扫描 PDF）→ 属预期边界，官方方案是托管 OCR（Firecrawl Parse API），开源替代可选 MinerU 等，不要重试。
- `encrypted` → 加密文档无法处理，请用户提供解密版本。
- 结果为空/`malformed` → 确认文件未损坏；必要时回退内置 `read` 保底。
- 其他参数错误 → 先 `npx -y @firecrawl/anydoc --help` 查准确用法，不要硬猜。
