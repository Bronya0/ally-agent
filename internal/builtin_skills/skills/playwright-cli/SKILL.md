---
name: playwright-cli
description: 通过 playwright-cli 命令行操作浏览器（打开页面、点击、填表、截图、抓取无障碍快照等）。首次使用时按需引导全局安装 @playwright/cli，所有操作经 run_command 执行，无需 Ally 内置浏览器依赖。
type: browser-automation
whenToUse: 用户要求操作浏览器、自动化网页交互、端到端测试、抓取需要 JS 渲染的页面内容、或对页面元素进行精确定位与点击时。
---

# Playwright CLI Skill

通过 `playwright-cli`（npm 包 `@playwright/cli`）以命令行方式驱动浏览器。所有命令都经 command工具执行，cwd 为当前 workspace，使用 bash 语法（Windows 上是 Git Bash）。

**完整命令与参数以 `playwright-cli --help` 和 `playwright-cli <command> --help` 为准**，版本可能变化，不要凭记忆猜参数。本 skill 只补充 help 不会说明的 Ally 集成约定与关键工作流。

## 1. 安装检查与引导

每次进入浏览器任务前，**先检查 `playwright-cli` 是否已安装**，未安装必须先用 `ask` 工具征求用户同意**，再执行安装。`ask` 必须是该轮模型响应里唯一的工具调用。

### ask 模板

```json
{
  "questions": [
    {
      "id": "install_playwright_cli",
      "question": "操作浏览器需要 Playwright CLI，本地未检测到。将执行 `npm install -g @playwright/cli@latest` 全局安装一次，后续无需安装。是否继续？",
      "options": [
        {"id": "confirm", "label": "确认安装", "description": "执行 npm install -g @playwright/cli@latest。", "recommended": true},
        {"id": "cancel", "label": "取消", "description": "不安装，本次浏览器任务无法继续。", "recommended": false}
      ]
    }
  ]
}
```

用户确认后执行 `npm install -g @playwright/cli@latest`，结束验证。安装失败时报告错误，不要静默重试。

## 2. 浏览器选择

**优先使用系统已安装的浏览器，避免下载新的浏览器二进制**。Playwright 支持的 `--browser=` 大致分两类：

- 系统浏览器（免下载）：`msedge`、`chrome` —— 要求系统已装对应浏览器
- Playwright 二进制（需 `playwright-cli install-browser` 下载，约 100-300 MB）：`chromium`、`firefox`、`webkit`

注意：**Playwright 不支持系统 Safari**。`webkit` 虽与 Safari 同内核，但它是 Playwright 自己的 patched 二进制，仍需下载。

### 探测流程（首次 `open` 前执行一次）

**Windows**：直接 `--browser=msedge`（Win10+ 自带 Edge）。

**macOS / Linux**：依次探测系统 Chrome 和 Edge，命中哪个用哪个：

```bash
if command -v google-chrome >/dev/null 2>&1 || command -v google-chrome-stable >/dev/null 2>&1 || command -v chromium >/dev/null 2>&1; then
  echo "BROWSER=chrome"
elif command -v microsoft-edge >/dev/null 2>&1 || command -v msedge >/dev/null 2>&1; then
  echo "BROWSER=msedge"
elif [ -x "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" ]; then
  echo "BROWSER=chrome"
elif [ -x "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge" ]; then
  echo "BROWSER=msedge"
else
  echo "BROWSER=none"
fi
```

- `BROWSER=chrome` → `--browser=chrome`
- `BROWSER=msedge` → `--browser=msedge`
- `BROWSER=none` → 用 `ask` 询问用户是否下载 chromium；确认后 `playwright-cli install-browser`，再用 `--browser=chromium`

### 回退

若 `open` 报 `Browser not installed` / `channel not found` 等，说明所选系统浏览器实际未装或路径未识别，改走下载分支：用 `ask` 告知用户后 `playwright-cli install-browser`，再用 `--browser=chromium` 重试。下载前同样要 `ask`。

用户明确指定浏览器时，从其要求。

### 默认有头

`open` 默认是无头模式（不弹窗），但**大部分场景应默认加 `--headed` 让用户看到浏览器操作过程**（调试、验证、演示、交互式任务）。仅在以下场景用无头（即不加 `--headed`）：

- 纯数据抓取、后台批量任务，用户不需要看过程
- 用户明确要求无头
- 无头环境（无显示器的服务器、CI）

若不确定，默认 `--headed`。

## 3. 关键工作流（help 不会讲的部分）

### 标准交互闭环

```
1. open      → playwright-cli open <url> --browser=<探测结果> --headed
2. snapshot  → playwright-cli snapshot        # 拿到元素 ref，如 e5
3. 交互       → playwright-cli click e9 / fill e3 "text" / ...
4. snapshot  → 验证结果（ref 每次导航/刷新后都可能变，操作前重取）
5. close     → playwright-cli close           # 任务结束释放浏览器进程
```

**核心原则**：`snapshot` 返回的无障碍树 ref（如 `e5`）是首选定位方式，比 CSS/坐标可靠；仅在 canvas、地图、自定义无 a11y 树的组件上才用 `screenshot` 看坐标 + 鼠标命令。

### 对话框

出现 alert/confirm/prompt 时其他命令会提示「⚠ Dialog appeared」，**必须先 `dialog-accept`/`dialog-dismiss` 再继续**。

## 4. Ally 集成注意事项（help 不会讲的部分）

- **路径**：传给 playwright-cli 的文件路径用 forward slash。Windows 绝对路径在 Git Bash 里写成 `/c/Users/...` 或 `"C:/Users/..."`。
- **cwd**：`run_command` 的 cwd 在 workspace 内；截图、state 等输出默认落 workspace，可用 `--filename=` 指定。
- **超时**：浏览器操作较慢，`run_command` 的 `timeoutSeconds` 适当调大（如 60-120）。
- **资源清理**：任务结束用 `close` 或 `close-all` 释放浏览器进程，避免残留。
- **不要**用 Ally 的 `read` 工具读 playwright-cli 生成的截图二进制文件；截图仅供用户查看或模型从命令输出文本判断。
- **会话保持**：默认每次 `open` 是全新会话；保留登录态用 `--persistent` 或 `state-save`/`state-load`（具体参数以 `--help` 为准）。

## 5. 错误恢复速查

- `playwright-cli: command not found` → 回第 1 步检查/安装。
- `Browser not installed` / `channel not found` → 走第 2 节回退分支，`ask` 后下载 chromium。
- `Element not found` / ref 失效 → 重新 `snapshot` 确认 ref。
- 其他命令参数错误 → 先 `playwright-cli <command> --help` 查准确用法，不要硬猜。
