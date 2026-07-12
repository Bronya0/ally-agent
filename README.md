# Ally

[English](README.md) | [简体中文](README_zh-CN.md)

**Ally** is a desktop AI coding agent powered by Wails and configurable LLM providers. It sits in your project directory, understands your codebase, and helps you read, edit, search, and manage files through natural conversation.

Ally supports tool-assisted file editing, syntax-highlighted diff previews, regex search, batch file operations, MCP servers, skills injection, plan mode, goal tracking, session management, and more.

---

## Features

| Feature | Description |
|---------|-------------|
| **🧠 AI-Powered** | Supports OpenAI Chat Completions, OpenAI Responses, and Anthropic Messages APIs |
| **📝 Intelligent Editing** | `replace_exact`, `replace_lines`, `create_file` with sha256 verification |
| **🎨 Visual Diff** | Colored diff view (green additions / red deletions) with syntax highlighting |
| **🔍 Code Search** | Built-in `grep_files` powered by a bundled ripgrep binary |
| **📂 Batch Operations** | `batch_read` reads multiple files at once |
| **🔧 MCP Support** | Configure MCP servers in Settings — connected tools are auto-discovered and injected |
| **📜 Skills System** | Activate skill files (`.md`) from `.agents/skills/` via `/skillname` |
| **📋 Run Modes** | Switch between default YOLO execution mode and read-only PLAN mode from the composer toolbar |
| **🎯 Goal Mode** | Set and track goals with budget control (`create_goal`, `update_goal`) |
| **🕒 Scheduled Tasks** | Let the model create persistent one-time, interval, or Cron Agent tasks and manage them from the task drawer |
| **🔄 Session Management** | Multiple independent sessions with state isolation and auto-save |
| **🔀 Parallel Tool Calls** | Multiple tools executed in one LLM turn |
| **📦 Sub-Agent Delegation** | Delegate subtasks to child LLM calls (`agent_delegate`) |
| **⚙️ Configurable** | Provider, model, temperature, system prompt — all adjustable in UI |

---

## Prerequisites

- [Go](https://go.dev/dl/) ≥ 1.25
- [Wails CLI](https://wails.io/docs/gettingstarted/installation)
- [Node.js](https://nodejs.org/) ≥ 18

### Install Wails

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0
```

---

## Quick Start

```bash
# Clone
git clone https://github.com/Bronya0/ally-agent.git
cd ally-agent

# Install frontend dependencies
cd frontend && npm ci && cd ..

# Generate Wails bindings
wails generate module

# Run in development mode (hot-reload)
wails dev

# Or build a distributable binary
wails build
```

### Configure an LLM Provider

1. Launch Ally
2. Click the ⚙️ settings button in the top-right
3. Fill in:
   - **Provider Name**: e.g., `OpenAI Compatible`
   - **Interface Format**: OpenAI Chat Completions, OpenAI Responses, or Anthropic Messages
   - **Base URL**: e.g., `https://api.openai.com/v1` or `https://api.anthropic.com`
   - **Model**: e.g., `gpt-4o-mini` or `claude-sonnet-5`
   - **API Key**: your API key
4. Click **Save**

---

## Usage

### Commands

| Command | Description |
|---------|-------------|
| `/new` | Create a new session |
| `/sessions` | List and switch sessions (use `/switch N`) |
| `/init` | Generate `AGENTS.md` via AI codebase exploration |
| `/goal` | Set a goal mode |
| `/skills` | List loaded skills |
| `/clearskills` | Clear all loaded skills |

### MCP Servers

Open Settings → MCP and edit the JSON stored at `~/.ally_agent/mcp.json`:

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

MCP servers are initialized once at app startup and shared across sessions. Disabled servers (`"enabled": false`) are skipped. Only successfully connected MCP tools are exposed to the LLM.

### Skills

Place skill files (`.md` with YAML frontmatter) in:

- **User-level**: `~/.agents/skills/<skill>/SKILL.md`
- **Project-level**: `<workspace>/.agents/skills/<skill>/SKILL.md`

Activate with `/<skillname>`.

---

## Project Structure

```
├── app.go              # Go backend (tools, chat, sessions, MCP, skills)
├── mcp.go              # MCP server manager
├── main.go             # Wails app entry point
├── frontend/
│   ├── src/
│   │   ├── App.vue      # Main Vue UI
│   │   ├── style.css    # Global styles
│   │   ├── utils/
│   │   │   └── diff.js  # LCS diff algorithm (ported from kimi-code)
│   │   └── components/
│   │       ├── DiffView.vue   # Colorful diff display
│   │       └── CodeView.vue   # Syntax-highlighted code display
│   └── package.json
├── wails.json           # Wails project config
└── go.mod / go.sum      # Go dependencies
```

---

## Development

### Regenerate Wails Bindings

After adding new Go methods:

```bash
wails generate module
```

### Architecture

```
┌─────────────┐     Wails Runtime      ┌──────────────┐
│  Vue 3 UI   │ ◄──── events ───────► │  Go Backend  │
│  (Naive UI) │                        │  (OpenAI SDK)│
│  highlight.js│                       │  MCP Client   │
└─────────────┘                        └──────┬───────┘
                                              │
                                    ┌─────────▼────────┐
                                    │  LLM API (OpenAI │
                                    │  Compatible)     │
                                    └──────────────────┘
```

---

## License

Copyright (C) 2026 Bronya0.

Ally is free software licensed under the [GNU General Public License v3.0 only](LICENSE) (`GPL-3.0-only`). You may use, study, modify, and redistribute it under those terms. Distributions of Ally or modified versions must provide the corresponding source code and retain the GPLv3 license notices.

Release packages also include [ripgrep](https://github.com/BurntSushi/ripgrep) as a separate executable. ripgrep remains available under its own MIT/Unlicense terms. Other bundled assets and dependencies retain their respective licenses; see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

