---
name: codegraph
description: 生成或更新工作区根目录的 CODEGRAPH.md 代码图谱，供 AI 编码代理快速理解项目架构、模块职责、依赖与调用流。
type: code-graph
whenToUse: 用户要求生成项目代码图谱、刷新 CODEGRAPH.md，或在重大架构变更后更新架构导航文件时。
---

# Code Graph Skill

生成或更新工作区根目录的 `CODEGRAPH.md`。该文件**不是给人读的**，而是给后续的 AI 编码代理在改代码前快速理解架构、模块职责、依赖与调用流的可机读导航图。优化目标是 token 效率与语义清晰度，而不是排版美观。

## 1. 决策逻辑（先做这一步）

读取工作区根目录的 `CODEGRAPH.md`：

- **不存在** → 从零生成。
- **已存在** → 读现有文件，与当前代码对比（新增文件、删除文件、结构变化），**就地增量更新**：只改实际变动的章节，保留其余内容。不要整体重写，不要从零再生成一次。

## 2. 探测流程

1. **语言/工具链探测**：读根目录配置文件（`go.mod`、`package.json`、`Cargo.toml`、`pyproject.toml`、`pom.xml`、`*.csproj`、`mix.exs`、`rebar.config`、`build.gradle`、`CMakeLists.txt`、`Makefile` 等）确定技术栈。多语言项目逐一列出。
2. **模块树绘制**：用 `list_files` 走目录。按目录与 import/包路径把相关文件归到模块。
3. **关键文件精读**：每个模块只读**入口点、核心类型、公共 API、行数最高的几个文件**。提取 struct/class/interface/trait 定义、函数/方法签名、import/use/require 关系。不要逐行读实现。
4. **关系图构建**：谁 import 谁？哪个模块依赖哪个？从入口点出发的主调用链是什么？
5. **写入**：用 `create`（新建）或 `edit`（更新已存在文件）。

## 3. 输出格式（严格规范）

`CODEGRAPH.md` 必须严格按以下结构。文件以单个 H1 `# Code Graph: <project-name>` 开头，`<project-name>` 是工作区根目录的 basename（不是 package name）。所有路径**必须是工作区相对路径**。文件以一个空行结尾。全文不超过 800 行。

下面六个 H2 章节按顺序出现：

### `## Language and Build`

每个主要语言/框架一行，bullet 列表：

```
- language: <name> (<version or toolchain if detected>)
```

例：
- `language: Go 1.22 (go.mod)`
- `language: TypeScript / Node 20 (package.json + vite)`

### `## Module Hierarchy`

层级 bullet 列表。每个目录/模块节点带一个语义 tag 和一行职责说明，下面列出文件及其贡献。

格式：
```
- [<tag>] <relative-path>/ — <one-line purpose>
  - <filename> — <what it contains, key types/functions>
  - <filename> [Lines: N] — <if large, mention why>
```

每个节点选**最贴切的一个** tag：

| tag | 含义 |
|-----|------|
| `[entry]` | 程序入口、main、bootstrap、app shell |
| `[handler]` | HTTP/事件 handler、路由、controller、presenter |
| `[model]` | 数据模型、entity、schema、struct、DTO |
| `[adapter]` | 外部集成、provider 封装、client、driver |
| `[service]` | 业务逻辑、use case、workflow、orchestration |
| `[infra]` | 基础设施、config、storage、networking、platform |
| `[util]` | 共享工具、helper、常量、类型 |
| `[test]` | 测试套件、mock、fixture、集成测试 |
| `[ui]` | UI 组件、页面、screen、view、template |
| `[config]` | 配置、环境、常量 |
| `[data]` | 数据访问、repository、DAO、migration、query |
| `[proto]` | protobuf / IDL / schema 定义 |
| `[build]` | 构建脚本、CI/CD、Docker、tooling |

### `## Key Types / Interfaces`

每个模块列出架构上最重要的类型，一行一个，格式：
```
[<kind>] <name> — <responsibility>
```

`<kind>` 可选：`struct`、`class`、`interface`、`trait`、`enum`、`config`、`typedef`、`type`、`protocol`、`record`。

**只列跨文件出现或对架构中心的类型**，跳过内部 helper 和琐碎 wrapper。

### `## Call Flow / Data Flow`

主执行路径。缩进箭头标注，缩进随调用深度递增。格式：
```
- [<action>] <source> → <target>  // <semantic meaning>
```

action 前缀：
- `[call]` 函数/方法直接调用
- `[data]` 数据流（返回值、channel、stream、props、events）
- `[ext]` 外部系统调用（DB、API、文件系统、网络）
- `[event]` 事件发射、回调、订阅、hook
- `[init]` 初始化 / bootstrap 时

调用目标是已知模块/层时，后缀 `[<module-tag>]`。

例：
```
- [call] main() → StartChat  // entry point
  - [call] StartChat → buildMessages()    [service]
  - [call] StartChat → buildToolsWithMcp()  [infra]
    - [ext] buildToolsWithMcp() → mcp.ListTools()  // MCP server discovery
```

### `## Inter-Module Dependencies`

模块间依赖边。格式：
```
<relative-path> → <relative-path>  // <nature of dependency>
```

### `## Hot Paths / Files to Focus`

架构上关键、频繁变动或体量很大的文件：
- `[***] <path> — <reason>` 关键/中心/极大
- `[**] <path> — <reason>` 重要
- `[*] <path> — <reason>` 值得注意

## 4. 约束

- **不要**输出 Mermaid、ASCII 图、表格或任何视觉格式——纯文本 bullet 与缩进。
- **不要**记录内部 helper、private 实现、测试 mock 细节。
- **不要**复制函数体或长签名，只保留名字与一句话职责。
- 全文不超过 800 行。
- 所有路径必须工作区相对；绝对路径会污染跨机器复用。
- 若项目 >500 个源文件，**只聚焦 3-5 个架构最重要的模块**，并明说"其他模块存在但已省略"。
- 单文件扁平模块用一句话说明，不要硬塞层级。
- 大项目优先模块级粒度，避免文件级膨胀。
- 增量更新时，保留 H1 的 `<project-name>` 除非工作区目录改名。
- `create`/`edit` 完成后用 `read` 回读校验。

## 5. 校验

写完后 `read` 回读 `CODEGRAPH.md`，确认：

1. 至少 15 行有效内容。
2. 以 H1 `# Code Graph: <name>` 开头。
3. 包含 `## Language and Build`、`## Module Hierarchy`，以及其余 H2 中的至少两个。
4. 最后一行是空行。

任一项不满足，诊断并修复。若项目过小装不下完整图谱，直接说明"项目较小"并列出文件与角色。

## 6. 入口动作

从 `list_files` 走项目根 + 读根目录配置文件开始。
