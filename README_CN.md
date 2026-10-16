# qoder2api

[English](README.md)

Go 语言实现的协议桥，将 Qoder 暴露为 OpenAI 兼容和 Anthropic 兼容的本地 API。

支持的端点：

- `POST /v1/chat/completions` — OpenAI Chat Completions
- `POST /v1/responses` — OpenAI Responses（Codex CLI 使用）
- `POST /v1/messages` — Anthropic Messages（Claude Code CLI 使用）

## 快速开始

```bash
# 1. 登录授权（仅首次或 token 过期时需要）
go run ./cmd/qoder2api-login

# 2. 启动服务
go run ./cmd/qoder2api

# 3. 配置客户端指向 http://127.0.0.1:8963
```

## 授权

### CLI 登录（推荐）

```bash
go run ./cmd/qoder2api-login
```

浏览器会自动打开 Qoder 授权页面，完成登录后 token 自动保存到 `~/.config/qoder2api/auth.json`。之后直接启动服务即可，无需设置环境变量。

自定义保存路径：

```bash
go run ./cmd/qoder2api-login -o /path/to/auth.json
```

### QODER_AUTH_JSON

兼容模式，使用从 Qoder 客户端导出的 auth JSON：

```bash
QODER_AUTH_JSON=/absolute/path/to/auth.json go run ./cmd/qoder2api
```

支持的 JSON 格式：

- 包含 `securityOauthToken` 和 `refreshToken` 的根对象
- 包含 `auth_user_info_raw` 的根对象
- 第一个元素包含 `auth_user_info_raw` 的数组

### QODER_PAT

Cloud Agents API 模式：

```bash
QODER_PAT=your_pat go run ./cmd/qoder2api
```

## 运行

默认监听地址：

```text
http://127.0.0.1:8963
```

可选环境变量：

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `QODER_HOST` | 监听地址 | `127.0.0.1` |
| `QODER_PORT` | 监听端口 | `8963` |
| `QODER_MODEL` | 默认模型 | `auto` |
| `QODER_TIMEOUT_SEC` | 请求超时（秒） | `300` |
| `QODER_BASE_URL` | Qoder API 地址 | `https://api.qoder.com` |
| `QODER_WORKSPACE_ROOT` | 工作区路径 | 空 |
| `QODER_PROXY_URL` | HTTP 代理 | 空 |
| `QODER_CA_FILE` | 自定义 CA 证书 | 空 |
| `QODER_INSECURE_SKIP_VERIFY` | 跳过 TLS 验证 | `false` |

## Docker

PAT 模式：

```bash
QODER_PAT=your_pat docker compose up --build
```

Auth JSON 模式：

```bash
docker build -t qoder2api:local .
docker run --rm -p 8963:8963 \
  -e QODER_HOST=0.0.0.0 \
  -e QODER_AUTH_JSON=/auth.json \
  -v /absolute/path/to/auth.json:/auth.json:ro \
  qoder2api:local
```

## 客户端配置

### Codex CLI

Codex 使用 Responses API，base URL 需要以 `/v1` 结尾。

编辑 `~/.codex/config.toml`：

```toml
[model_providers.custom]
name = "custom"
wire_api = "responses"
base_url = "http://127.0.0.1:8963/v1"
```

### Claude Code CLI

```bash
ANTHROPIC_BASE_URL=http://127.0.0.1:8963 ANTHROPIC_API_KEY=test-key claude
```

如果设置覆盖了 base URL，使用 `--setting-sources local`。

## 兼容性说明

- `QODER_AUTH_JSON` 使用基于官方 Qoder 客户端流量的兼容后端，上游客户端/API 变更可能需要更新
- 使用 `QODER_AUTH_JSON` 时建议 `QODER_MODEL=auto`，除非你已验证特定模型 key
- Anthropic `thinking` 块仅在请求显式启用时暴露
- `/v1/messages` 对于不使用客户端工具的图片流式传输，使用兼容回退模式：聚合上游结果后发出合法的 Anthropic SSE

## 开发

```bash
go test ./...
go build -o qoder2api ./cmd/qoder2api
```

## 免责声明

本项目仅供**学习和研究目的**使用，使用者需自行承担风险。作者不对因使用本软件而产生的任何后果负责，包括但不限于账号封禁、数据丢失或法律纠纷。

请勿将本项目用于任何商业用途，或以任何违反 Qoder 或第三方服务条款的方式使用。

Auth JSON 文件和 token 文件包含有效凭据，**请勿提交到版本控制系统**。

## 许可证

[MIT](LICENSE)


## 本仓库（Fork）的改动

在 `jyao0708/qoder2api` 基础上补齐了 **Claude Code CLI** 与 **Codex CLI** 的真实可用性（工具调用链路）：

- **工具调用**：按客户端请求把工具注册成 Qoder 云端的 custom tool（`type: custom`），把云端返回的 `agent.custom_tool_use` 事件解析成标准 `tool_calls` / `tool_use`，收到工具结果后继续下一轮。实测 `claude` 与 `codex exec` 都能真正执行 shell、读写文件。
- **工具名冲突处理**：云端内建工具名（`bash/read/write/edit/glob/grep/webfetch/web_search`）与 `mcp__` 前缀均为保留名，注册时统一加 `cc_` 前缀，回译时还原成客户端原始工具名。
- **模型回落**：云端只接受官方模型 key（`auto/dmodel/qmodel/...`），客户端传来的 `claude-*`、`gpt-*` 会自动回落到 `QODER_MODEL`（默认 `auto`）。
- **多轮上下文**：转写 transcript 时带上历史的 `tool_calls` / `tool_call_id`，保证多轮工具循环上下文完整。

### 用 Claude Code CLI

```bash
scripts/claude.sh -p "用 bash 写一个文件并读回"
```

等价于：

```bash
ANTHROPIC_BASE_URL=http://127.0.0.1:8963 ANTHROPIC_API_KEY=qoder-local claude
```

### 用 Codex CLI

```bash
scripts/codex.sh exec "用 shell 运行 echo hi"
```

等价于：

```bash
OPENAI_API_KEY=qoder-local codex \
  -c model_provider=custom \
  -c 'model_providers.custom.base_url="http://127.0.0.1:8963/v1"' \
  -c 'model_providers.custom.wire_api="responses"' \
  -c 'model_providers.custom.requires_openai_auth=false'
```
