---
name: review
description: 审查分支、PR 或工作区改动是否符合代码规范和原始需求时使用。
type: code-review
whenToUse: 用户要求审查分支、PR、提交或工作区改动是否符合代码规范与原始需求时。
---

# Review Skill

双轴审查：对 `HEAD` 与用户指定固定点之间的 diff（或工作区未提交改动）做两个维度的独立审查：

- **Standards** — 代码是否符合本仓库文档化的编码规范？
- **Spec** — 代码是否忠实实现了原始需求 / issue / PRD？

两个轴用**并行 `subagent`** 审查，避免互相污染上下文，最后由主 agent 汇总双方发现。主 agent **不重新读 diff**，只消费两份子报告。

## 流程

### 0. 预检（fail-fast，入口处完成）

开始前必须确认：

1. **固定点可解析**：`git rev-parse <fixed-point>` 成功；工作区模式跳过此项。
2. **diff 非空**：`git diff <fixed-point>...HEAD --stat`（或 `git diff HEAD --stat`）有输出。空 diff 立即报错并停止，不要派子代理。
3. **commit 列表就绪**：`git log <fixed-point>..HEAD --oneline` 用于两份子代理 brief。

无效引用或空 diff 必须在入口失败，不要拖到子代理里。

### 1. 确定固定点

用户说的固定点可以是：commit SHA、分支名、tag、`main`、`HEAD~5`，或**工作区未提交改动**。

- 已提交范围：`git diff <fixed-point>...HEAD`（三点，按 merge-base 比较），commit 列表用 `git log <fixed-point>..HEAD --oneline`
- 工作区未提交改动：`git diff HEAD`，配合 `git status --short` 确认改动文件

若用户没给固定点，先 `git status` 看是否有未提交改动；有就用工作区改动，没有就问用户要固定点。

### 2. 找 spec 来源

按优先级：

1. commit message 里的 issue 引用（`#123`、`Closes #45` 等）——Ally 没有 GitHub MCP，issue 文本要么由用户提供（粘贴/路径），要么从本地 issue 文件读。
2. 用户传入的路径参数。
3. `docs/`、`specs/`、`.scratch/` 下匹配分支名或功能名的 PRD/spec 文件。
4. 找不到就问用户。用户说没有 spec，则 Spec 轴跳过并在最终报告注明 "no spec available"。

### 3. 找规范来源

仓库里任何文档化编码规范：`AGENTS.md`、`CODEGRAPH.md`、`CODING_STANDARDS.md`、`CONTRIBUTING.md`、`.editorconfig` 等。全部列给 Standards 子代理。

若以上文件都不存在（极少见），Standards 轴报告 "no documented standards; cannot assess" 并跳过，不要凭通用审美编造规范。

### 4. 并行派两个 subagent

在同一轮响应里发两个 `subagent` 调用（并行执行），各自独立完成任务。**主 agent 不读 diff、不读规范、不读 spec**——只整理两份报告。

**Standards 子代理**，任务包含：

- diff 命令和 commit 列表
- 第 3 步找到的规范文件清单
- brief："逐文件/hunk 报告 diff 中每处违反文档化规范的地方。每条 finding 必须包含：(a) 规范出处 `文件#行号`，(b) 违规位置 `路径#行范围`，(c) 一句话说明违反什么，(d) 严重等级 blocker/major/minor/nit。区分**硬违规**（明确写在规范文档里的规则被破坏）与**判断性意见**（规范未明写但通常期望的实践）。跳过工具链已强制的事（lint/formatter 能查的不要重复报）。没有硬违规就回 'Standards: pass'。"

**Spec 子代理**，任务包含：

- diff 命令和 commit 列表
- spec 的路径或内容
- brief："对 spec 中每条需求，标记：(a) 已实现——给出对应的 diff hunk `路径#行范围`，(b) 部分实现——说明缺什么，(c) 缺失。再单独列出 diff 里出现但 spec 没要求的越界行为。每条标严重等级。没有问题就回 'Spec: pass'。"

没有 spec 时跳过 Spec 子代理，在最终报告里注明。没有规范文档时同样跳过 Standards 子代理。

### 5. 汇总

两个报告分别放在 `## Standards` 和 `## Spec` 标题下，原样或轻度整理。**不要合并、不要重排、不要跨轴选冠军**——两轴刻意分离（见下）。

每条 finding 保留子代理给的 `路径#行范围` 与严重等级，方便用户跳转。

结尾一行总结：每轴状态（pass / fail / skipped）+ 每轴内最严重的问题（若有）。两轴都 pass 时直接写 `Standards: pass. Spec: pass.` 并停，不要凑字。

## 为什么双轴

一个改动可能过一轴挂另一轴：

- 完全符合规范但实现错了需求 → **Standards pass, Spec fail**
- 需求做对了但破坏了项目约定 → **Spec pass, Standards fail**

分开报告，防止一轴掩盖另一轴。

## 严重等级约定

- **blocker** — 必须修，否则不能合并（数据损坏、安全漏洞、spec 完全没实现的核心需求、违反安全规范）
- **major** — 应当修，影响正确性或可维护性（逻辑错误、spec 部分实现、违反文档化的架构约定）
- **minor** — 建议修（命名、注释、可读性、轻微 spec 偏差）
- **nit** — 可选（风格偏好、个人意见）

只报真正的问题；不要为了凑数把 nit 升级成 minor。
