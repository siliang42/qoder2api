#!/usr/bin/env bash
# 通过 Qoder 桥启动 Codex CLI。
# 桥地址可用 QODER_BRIDGE_URL 覆盖，默认 http://127.0.0.1:8963
set -euo pipefail
BRIDGE="${QODER_BRIDGE_URL:-http://127.0.0.1:8963}"
exec env OPENAI_API_KEY="${OPENAI_API_KEY:-qoder-local}" codex \
  -c model_provider=custom \
  -c "model_providers.custom.name=\"qoder-bridge\"" \
  -c "model_providers.custom.base_url=\"${BRIDGE}/v1\"" \
  -c 'model_providers.custom.wire_api="responses"' \
  -c 'model_providers.custom.requires_openai_auth=false' \
  "$@"
