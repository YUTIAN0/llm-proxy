# llm-proxy

[English](../README.md) | [中文](README.zh-CN.md)

轻量级 LLM API 代理，支持在 OpenAI、Claude、Gemini 不同 API 格式之间进行转换。

## 功能特性

- **多格式转换**: OpenAI ↔ Claude Messages API ↔ Google Gemini
- **模型别名**: 将任意模型名映射到上游模型（如 `claude-sonnet-4-6` → `Qwen3-32B`）
- **API Key 管理**: 每个 Key 独立配置允许/拒绝的模型列表
- **模型路由**: 根据不同模型路由到不同的上游渠道
- **流式支持**: SSE 流式输出，实时格式转换
- **思考标签**: 自动处理 `<think>` / `</think>` 推理模型标签
- **多渠道**: 支持多个上游渠道，可 pass-through 或代理模式
- **HTTP 代理**: 可选 SOCKS5 代理支持
- **Token 统计**: 按 API Key 统计输入输出 token，支持定时或按请求次数输出
- **Token 精确计数**: 使用 tiktoken 本地预计算输入 token，上游无 usage 时自动回退
- **自动重试/故障转移**: 上游渠道失败时自动切换其他健康渠道
- **参数覆盖**: 转发前修改请求字段（set/delete/prepend/append）
- **渠道健康检查**: 后台定期探测渠道健康状态，支持 `/health/channels` 接口

## 快速开始

```bash
go build -o llm-proxy && ./llm-proxy --config config.yaml
```

## 配置文件说明

```yaml
server:
  port: 8080              # 监听端口
  tls:
    cert: /path/to/cert.pem
    key: /path/to/key.pem  # HTTPS 证书（可选）

channels:
  - name: "primary"
    api_key: "sk-xxx"      # 上游 API Key
    base_url: "https://api.upstream.com"
    format: "openai"       # openai | claude | gemini | gemini_to_openai | openai_to_gemini
    models:
      - "model-a"
      - "model-b"
    headers:               # 自定义请求头（可选）
      "X-Custom-Header": "custom-value"

default_channel: "primary"  # 默认上游渠道

# 模型别名映射
model_aliases:
  "claude-sonnet-4-6": "model-a"
  "gpt-4o": "model-b"

# API Key 配置
api_keys:
  - key: "sk-user-1"
    name: "user-1"         # 日志展示用名称
    # 允许的模型（留空表示全部允许）
    allowed:
      - "claude-sonnet-4-6"
    # 拒绝的模型
    denied: []
    # 模型路由：根据模型名将请求路由到不同渠道
    channels:
      "claude-*": ["primary"]
      "gpt-3.5-*": ["backup"]

# SOCKS5 代理（可选）
proxy:
  enabled: true
  type: "socks5"
  addr: "127.0.0.1:1080"

# Token 用量统计
stats:
  enabled: true
  interval: "5m"
  request_count: 100

# 自动重试/故障转移
retry:
  enabled: true
  max_attempts: 3
  status_codes: [429, 500, 502, 503, 504]

# 参数覆盖 — 转发前修改请求字段
param_override:
  - path: "temperature"
    mode: "set"
    value: 0.7

# 渠道健康检查
health_check:
  enabled: true
  interval: "30s"
  timeout: "5s"
  unhealthy_threshold: 3
  healthy_threshold: 1
```

### 配置字段说明

| 字段 | 说明 |
|------|------|
| `server.port` | 服务监听端口，默认 8080 |
| `channels` | 上游渠道列表，每个渠道指定 API Key、地址、格式和模型 |
| `default_channel` | 未匹配到 API Key 时使用的默认渠道 |
| `model_aliases` | 模型别名映射，客户端用别名请求，代理自动转为上游真实模型 |
| `api_keys[].key` | 客户端使用的 API Key |
| `api_keys[].name` | 日志展示名称 |
| `api_keys[].allowed` | 允许使用的模型列表，留空表示无限制 |
| `api_keys[].denied` | 禁止使用的模型列表 |
| `api_keys[].channels` | 模型到渠道的路由映射，支持 `*` 通配符（如 `claude-*`） |
| `proxy.enabled` | 是否启用 SOCKS5 代理 |
| `stats.enabled` | 是否启用 token 统计 |
| `stats.interval` | 定时输出统计（如 `"5m"`、`"1h"`） |
| `stats.request_count` | 每 N 次请求输出统计（0 = 不启用） |
| `retry.enabled` | 启用上游失败时自动重试 |
| `retry.max_attempts` | 最大重试次数（跨渠道） |
| `retry.status_codes` | 触发重试的 HTTP 状态码 |
| `param_override[].path` | 要修改的请求字段（如 `"model"`、`"temperature"`） |
| `param_override[].mode` | `set`、`delete`、`prepend`、`append` |
| `param_override[].value` | `set`/`prepend`/`append` 的值 |
| `health_check.enabled` | 启用后台渠道健康探测 |
| `health_check.interval` | 检查间隔（如 `"30s"`） |
| `health_check.timeout` | 单次请求超时（如 `"5s"`） |
| `health_check.unhealthy_threshold` | 连续失败次数标记为不健康 |
| `health_check.healthy_threshold` | 连续成功次数标记为健康 |

## API 使用

### 对话补全

```bash
# OpenAI 格式
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-user-1" \
  -d '{
    "model": "claude-sonnet-4-6",
    "messages": [{"role": "user", "content": "Hello"}],
    "stream": true
  }'
```

### 获取模型列表

```bash
# 获取所有模型
curl http://localhost:8080/v1/models

# 按 API Key 过滤
curl -H "Authorization: Bearer sk-user-1" http://localhost:8080/v1/models

# 获取单个模型信息（含别名解析）
curl http://localhost:8080/v1/models/claude-sonnet-4-6
# 返回: {"id":"claude-sonnet-4-6","object":"model","owned_by":"llm-proxy","alias_for":"model-a"}
```

### 路由选择顺序

代理按以下优先级选择上游渠道：

1. `X-Channel` 请求头 — 指定渠道名称
2. `X-Target-Format` 请求头 — 指定渠道格式
3. `Authorization` / `X-API-Key` 请求头 — 通过 API Key 路由
4. `default_channel` — 默认渠道

### 支持的 API Key 请求头

| 请求头 | 说明 |
|--------|------|
| `Authorization: Bearer <key>` | 标准 OpenAI 格式 |
| `X-API-Key: <key>` | 通用格式 |
| `x-goog-api-key: <key>` | Gemini 格式 |

## 格式转换支持

| 客户端格式 | 上游格式 | 方向 |
|---|---|---|
| Claude Messages | OpenAI | Claude → OpenAI |
| Gemini | OpenAI | Gemini → OpenAI |
| OpenAI | Gemini | OpenAI → Gemini |
| OpenAI | OpenAI | 透传 |

### Claude 格式支持

- `text` / `image` / `tool_use` / `tool_result` 内容类型
- 系统提示（字符串和数组形式）
- 流式事件：`message_start`、`content_block_start`、`content_block_delta`、`content_block_stop`、`message_delta`、`message_stop`
- 思考标签（`thinking`）自动识别和转换

### Gemini 格式支持

- `contents` / `systemInstruction` 消息格式
- `functionCall` / `functionResponse` 工具调用
- `thought` 思考内容
- 流式响应转换为 Gemini 格式

## Token 统计

按 API Key 统计输入输出 token，支持两种触发方式（可独立配置）：

```yaml
stats:
  enabled: true
  interval: "5m"        # 每 5 分钟输出一次统计
  request_count: 100    # 每 100 次请求输出一次统计
```

输出示例：

```
[stats] === interval:5m0s ===
[stats]   api_key:claude-user          | requests=     5 | input_tokens=        55 | output_tokens=       250
[stats]   api_key:openai-user          | requests=     2 | input_tokens=        22 | output_tokens=       100
[stats]   (total)                 | requests=     7 | input_tokens=        77 | output_tokens=       350
```

未携带 API Key 的请求统一归类为 `(anonymous)`。

## Token 精确计数

使用 tiktoken 在发送请求前本地计算输入 token 数量。即使上游不返回 usage 数据，也能保证统计准确，并支持在请求发送前进行配额检查。

## 自动重试/故障转移

当上游渠道返回可重试的状态码（默认 429/500/502/503/504）时，代理自动切换到下一个健康的渠道重试：

```yaml
retry:
  enabled: true
  max_attempts: 3          # 最多尝试 3 个渠道
  status_codes: [429, 500, 502, 503, 504]
```

渠道按健康状态排序尝试：健康 > 未知 > 不健康。

## 参数覆盖

在转发到上游之前修改请求字段，支持四种模式：

| 模式 | 说明 |
|------|------|
| `set` | 完全替换字段值 |
| `delete` | 删除该字段 |
| `prepend` | 在原有字符串前追加 |
| `append` | 在原有字符串后追加 |

```yaml
param_override:
  - path: "temperature"
    mode: "set"
    value: 0.7
  - path: "max_tokens"
    mode: "delete"
  - path: "model"
    mode: "set"
    value: "claude-sonnet-4-6"
```

## 渠道健康检查

后台协程定期 ping 每个渠道以探测健康状态。不健康的渠道在重试池中优先级最低。

```yaml
health_check:
  enabled: true
  interval: "30s"
  timeout: "5s"
  unhealthy_threshold: 3
  healthy_threshold: 1
```

### 健康状态 API

```bash
curl http://localhost:8080/health/channels
```

返回：

```json
{
  "channels": [
    {
      "name": "primary",
      "status": "healthy",
      "consecutive_ok": 5,
      "consecutive_fail": 0,
      "response_time_ms": 120,
      "last_check": "2026-04-28T10:00:00Z"
    }
  ]
}
```

健康状态可选值：`healthy`（健康）、`unhealthy`（不健康）、`unknown`（未知）。

## 构建

```bash
# 静态编译
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -buildid=" -o llm-proxy
```

## 部署

```bash
./llm-proxy --config config.yaml
```

## 使用场景

### 场景 1：统一上游访问多个客户端工具

使用 Claude CLI / Gemini CLI 访问任意 OpenAI 格式的上游服务：

```bash
export ANTHROPIC_BASE_URL=http://your-proxy:8080/v1
export ANTHROPIC_AUTH_TOKEN=sk-claude-user-1
claude -p "hello" --model claude-sonnet-4-6
```

### 场景 2：多用户多模型授权

为不同用户配置不同的模型访问权限：

```yaml
api_keys:
  - key: "sk-team-dev"
    name: "开发团队"
    allowed:
      - "claude-sonnet-4-6"
      - "gpt-4o"

  - key: "sk-team-test"
    name: "测试团队"
    allowed:
      - "gpt-3.5-turbo"
    denied:
      - "claude-opus-4-7"
```

### 场景 3：模型路由到不同渠道

根据模型名将请求路由到不同的上游：

```yaml
api_keys:
  - key: "sk-user-1"
    channels:
      "claude-*": ["claude-channel"]
      "gpt-*": ["openai-channel"]
```
