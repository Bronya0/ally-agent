# Ally

[English](README.md) | [简体中文](README_zh-CN.md)

一个面向本地项目的桌面 AI 编程助手。Ally 可以通过对话帮助你理解代码、编辑文件、搜索项目、管理任务并完成开发工作。

<p>
  <a href="https://github.com/Bronya0/ally-agent/releases">
    <img alt="下载 Ally" src="https://img.shields.io/badge/下载_Ally-GitHub_Releases-2ea44f?style=for-the-badge&logo=github">
  </a>
</p>

前往 [Releases 页面](https://github.com/Bronya0/ally-agent/releases)下载 Windows、macOS 或 Linux 安装包。

![Ally 界面截图](docs/img.jpg)

## 主要功能

- 通过自然语言对话处理本地项目
- 读取、搜索、创建和编辑文件
- 使用清晰的可视化 Diff 查看代码改动
- 支持 OpenAI 兼容接口和 Anthropic 模型服务
- 通过 MCP 连接外部工具
- 使用 Skills 扩展工作流程
- 管理多个工作区和聊天会话
- 支持执行、计划和访谈模式
- 支持任务委派和定时任务
- 已内置代码搜索，无需单独安装 ripgrep

## 开始使用

1. 从 [GitHub Releases](https://github.com/Bronya0/ally-agent/releases) 下载对应平台的软件包。
2. 启动 Ally 并选择项目目录。
3. 打开设置，填写模型服务、模型名称、API 地址和 API Key。
4. 开始与 Ally 对话。

## 本地构建

```bash
wails build
```

## 许可证

Copyright (C) 2026 Bronya0。

Ally 是采用 [GNU General Public License v3.0 only](LICENSE)（`GPL-3.0-only`）发布的自由软件。你可以依照该许可证使用、研究、修改和重新分发本项目。分发 Ally 或其修改版本时，必须提供对应源代码并保留 GPLv3 许可证声明。

发行包包含采用自身 MIT/Unlicense 条款的 [ripgrep](https://github.com/BurntSushi/ripgrep)。其他第三方资源保留各自的许可证，详见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
