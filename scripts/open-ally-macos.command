#!/bin/zsh
set -euo pipefail

APP_PATH="/Applications/Ally.app"
USER_APP_PATH="$HOME/Applications/Ally.app"

if [[ -d "$APP_PATH" ]]; then
  APP="$APP_PATH"
elif [[ -d "$USER_APP_PATH" ]]; then
  APP="$USER_APP_PATH"
else
  osascript <<'APPLESCRIPT'
display dialog "请先把 Ally.app 拖到 Applications 文件夹，再运行此脚本。" buttons {"确定"} default button "确定" with icon caution
APPLESCRIPT
  exit 1
fi

if ! xattr -dr com.apple.quarantine "$APP"; then
  osascript <<'APPLESCRIPT'
display dialog "无法移除 Ally 的隔离属性。请确认 Ally.app 已安装到 Applications 文件夹，并重试。" buttons {"确定"} default button "确定" with icon stop
APPLESCRIPT
  exit 1
fi

osascript <<'APPLESCRIPT'
display notification "隔离属性已移除，正在启动 Ally。" with title "Ally"
APPLESCRIPT
open "$APP"
