#!/usr/bin/env bash

TITLE="${1:-Antigravity}"
MSG="${2:-Task completed}"

# 纯静音桌面通知 (使用标准 String Terminator 结束符，绝不触发响铃)
if [ -w /dev/tty ]; then
    printf "\x1b]777;notify;%s;%s\x1b\\\\" "$TITLE" "$MSG" > /dev/tty 2>/dev/null || true
fi
