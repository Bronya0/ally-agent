# Ally

[English](README.md) | [简体中文](README_zh-CN.md)

**Ally** 是一个基于 Wails 构建、支持多种 LLM Provider 的桌面 AI 编程助手。它运行在你的项目目录中，可以理解代码库，并通过自然语言帮助你读取、编辑、搜索和管理文件。

Ally 支持工具辅助文件编辑、语法高亮 Diff 预览、正则搜索、批量文件操作、MCP Server、Skills、计划模式、目标追踪、会话管理等功能。

---

## 功能

| 功能 | 说明 |
|------|------|
| **🧠 多模型支持** | 支持 OpenAI Chat Completions、OpenAI Responses 和 Anthropic Messages API |
| **📝 智能编辑** | 使用带版本校验的精确替换、批量编辑和文件创建工具 |
| **🎨 可视化 Diff** | 提供带语法高亮的新增、删除代码对比视图 |
| **🔍 代码搜索** | 内置由随包 ripgrep 二进制提供支持的 `grep_files` |
| **📂 批量操作** | 使用 `batch_read` 一次读取多个文件 |
| **🔧 MCP 支持** | 在设置中配置 MCP Server，自动发现并注入连接成功的工具 |
| **📜 Skills 系统** | 从 `.agents/skills/` 加载技能，并通过 `/<skillname>` 显式启用 |
| **📋 运行模式** | 在输入框工具栏切换 YOLO、只读 PLAN 和 GRILL 模式 |
| **🎯 目标模式** | 使用 `create_goal`、`update_goal` 创建和跟踪长期目标 |
| **🕒 定时任务** | 创建一次性、间隔或 Cron Agent 任务，并在任务面板中管理 |
| **🔄 会话管理** | 支持多个相互隔离、自动保存的会话 |
| **🔀 并行工具调用** | 在同一轮模型请求中并行执行多个非文件工具 |
| **📦 子 Agent** | 通过 `agent_delegate` 将独立子任务委派给子 Agent |
| **⚙️ 灵活配置** | 可在界面中配置 Provider、模型、温度和自定义提示词 |

---

## 环境要求

- [Go](https://go.dev/dl/) ≥ 1.25
- [Wails CLI](https://wails.io/docs/gettingstarted/installation)
- [Node.js](https://nodejs.org/) ≥ 18

### 安装 Wails

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0
```

---

## 快速开始

```bash
# 克隆仓库
git clone https://github.com/Bronya0/ally-agent.git
cd ally-agent

# 安装前端依赖
cd frontend && npm ci && cd ..

# 生成 Wails 绑定
wails generate module

# 以开发模式运行（支持热更新）
wails dev

# 构建可分发程序
wails build
```

### 配置模型 Provider

1. 启动 Ally。
2. 点击右上角的设置按钮。
3. 填写模型配置：
   - **Provider 名称**：例如 `OpenAI Compatible`
   - **接口格式**：OpenAI Chat Completions、OpenAI Responses 或 Anthropic Messages
   - **Base URL**：例如 `https://api.openai.com/v1` 或 `https://api.anthropic.com`
   - **模型**：例如 `gpt-4o-mini` 或 `claude-sonnet-5`
   - **API Key**：对应服务的密钥
4. 点击保存。

---

## 使用方式

### 命令

| 命令 | 说明 |
|------|------|
| `/new` | 创建新会话 |
| `/sessions` | 查看和切换会话，使用 `/switch N` 选择会话 |
| `/init` | 探索代码库并生成 `AGENTS.md` |
| `/goal` | 启动目标模式 |
| `/skills` | 查看发现的 Skills 及启用状态 |
| `/clearskills` | 停用所有 Skills |
| `/compact` | 压缩当前会话历史 |
| `/reload` | 重新加载模型配置 |

### MCP Server

打开“设置 → MCP”，编辑保存在 `~/.ally_agent/mcp.json` 中的 JSON：

```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "."]
    },
    "github": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": {
        "GITHUB_TOKEN": "your_token_here"
      }
    }
  }
}
```

MCP Server 在应用启动时初始化，并由所有会话共享。配置为 `"enabled": false` 的服务会被跳过，只有连接成功的 MCP 工具会提供给模型。

### Skills

Skill 文件支持以下目录：

- **用户级**：`~/.agents/skills/<skill>/SKILL.md`
- **项目级**：`<workspace>/.agents/skills/<skill>/SKILL.md`
- **项目 Kimi 风格**：`<workspace>/.kimi-code/skills/<skill>/SKILL.md`

通过 `/<skillname>` 或 `/skill:<name>` 显式加载完整 Skill 内容。

---

## 项目结构

```text
├── app.go                    # Go 后端：对话循环、工具、会话、Skills 和上下文管理
├── model_provider.go         # OpenAI/Anthropic 模型 Provider 适配层
├── mcp.go                    # MCP Server 生命周期与工具调用
├── scheduler.go              # 持久化定时 Agent 任务
├── services.go               # 后台服务进程管理
├── main.go                   # Wails 应用入口
├── frontend/
│   ├── src/
│   │   ├── App.vue           # Vue 主界面与运行时事件管理
│   │   ├── style.css         # 全局样式
│   │   ├── components/       # 聊天、工具卡片、设置等组件
│   │   └── utils/            # Diff、配置和工具预览工具函数
│   ├── wailsjs/              # Wails 自动生成的绑定
│   └── package.json
├── .github/workflows/        # Windows、Linux、macOS 构建流程
├── build/                    # Wails 平台资源
├── wails.json
└── go.mod / go.sum
```

---

## 开发

### 重新生成 Wails 绑定

添加或修改导出的 Go 方法后执行：

```bash
wails generate module
```

### 常用检查

```bash
go test ./...
go build ./...

cd frontend
npm ci
npm run build
```

### 构建发行包

GitHub Actions 支持手动触发，也会在发布 GitHub Release 时自动构建：

- Windows x64 ZIP
- Linux x64 TAR.GZ
- macOS Universal DMG

发行包内已携带 ripgrep，无需用户单独安装 `rg`。

---

## 许可证

Copyright (C) 2026 Bronya0。

Ally 是采用 [GNU General Public License v3.0 only](LICENSE)（`GPL-3.0-only`）发布的自由软件。你可以依照该许可证使用、研究、修改和重新分发本项目。分发 Ally 或其修改版本时，必须提供对应源代码并保留 GPLv3 许可证声明。

发行包同时包含作为独立可执行文件分发的 [ripgrep](https://github.com/BurntSushi/ripgrep)。ripgrep 仍采用其自身的 MIT/Unlicense 双许可证。其他第三方资源和依赖保留各自的许可证，详见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
