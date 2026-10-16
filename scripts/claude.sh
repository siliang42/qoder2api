#!/usr/bin/env bash
# 通过 Qoder 桥启动 Claude Code CLI。
# 桥地址可用 QODER_BRIDGE_URL 覆盖，默认 http://127.0.0.1:8963
set -euo pipefail
BRIDGE="${QODER_BRIDGE_URL:-http://127.0.0.1:8963}"
exec env \
  ANTHROPIC_BASE_URL="$BRIDGE" \
  ANTHROPIC_API_KEY="${ANTHROPIC_API_KEY:-qoder-local}" \
  claude "$@"
