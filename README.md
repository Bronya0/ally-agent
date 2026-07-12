# Ally

[English](README.md) | [简体中文](README_zh-CN.md)

A desktop AI coding assistant that works with your local projects. Ally helps you understand code, edit files, search a workspace, manage tasks, and complete development work through conversation.

<p>
  <a href="https://github.com/Bronya0/ally-agent/releases">
    <img alt="Download Ally" src="https://img.shields.io/badge/Download-GitHub_Releases-2ea44f?style=for-the-badge&logo=github">
  </a>
</p>

Download packages for Windows, macOS, and Linux from the [Releases page](https://github.com/Bronya0/ally-agent/releases).

![Ally screenshot](docs/img.jpg)

## Features

- Work with a project through natural-language conversations
- Read, search, create, and edit files
- Review clear visual diffs before or after changes
- Use OpenAI-compatible and Anthropic model providers
- Connect external tools through MCP
- Extend workflows with Skills
- Manage multiple workspaces and chat sessions
- Use execution, planning, and interview modes
- Delegate work and create scheduled tasks
- Bundled code search—no separate ripgrep installation required

## Getting started

1. Download the package for your platform from [GitHub Releases](https://github.com/Bronya0/ally-agent/releases).
2. Start Ally and select a project directory.
3. Open Settings and configure your model provider, model, API URL, and API key.
4. Start chatting about your project.

## Local build

```bash
wails build
```

## License

Copyright (C) 2026 Bronya0.

Ally is free software licensed under the [GNU General Public License v3.0 only](LICENSE) (`GPL-3.0-only`). You may use, study, modify, and redistribute it under those terms. Distributions of Ally or modified versions must provide the corresponding source code and retain the GPLv3 license notices.

Release packages include [ripgrep](https://github.com/BurntSushi/ripgrep) under its own MIT/Unlicense terms. Other third-party resources retain their respective licenses; see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

