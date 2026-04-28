# llm-proxy

Lightweight LLM API proxy that converts between OpenAI, Claude, and Gemini API formats.

[English](README.md) | [中文](docs/README.zh-CN.md)

## Features

- **Multi-format conversion**: OpenAI ↔ Claude Messages API ↔ Google Gemini
- **Model aliases**: Map any model name to an upstream model (e.g. `claude-sonnet-4-6` → `Qwen3-32B`)
- **API key management**: Per-key model authorization with allowed/denied lists
- **Model-based routing**: Route different models to different upstream channels
- **Streaming support**: SSE streaming with real-time format conversion
- **Thinking tags**: Automatic `<think>`/`</think>` tag handling for reasoning models
- **Multi-channel**: Multiple upstream channels with pass-through or proxy mode
- **HTTP proxy**: Optional SOCKS5 proxy support
- **Token statistics**: Per-key input/output token usage with interval or request-count triggers
- **Token counting**: Precise pre-count via tiktoken, fallback when upstream usage is missing
- **Automatic retry/failover**: Try other channels on upstream failure (configurable status codes)
- **Parameter override**: Modify request fields before forwarding (set/delete/prepend/append)
- **Channel health check**: Background health probing with `GET /health/channels` endpoint

## Quick Start

```bash
go build -o llm-proxy && ./llm-proxy --config config.yaml
```

## Configuration

```yaml
server:
  port: 8080
  tls:
    cert: /path/to/cert.pem
    key: /path/to/key.pem

channels:
  - name: "primary"
    api_key: "sk-xxx"
    base_url: "https://api.upstream.com"
    format: "openai"
    models:
      - "model-a"
      - "model-b"

default_channel: "primary"

model_aliases:
  "claude-sonnet-4-6": "model-a"
  "gpt-4o": "model-b"

api_keys:
  - key: "sk-user-1"
    name: "user-1"
    allowed:
      - "claude-sonnet-4-6"
    denied: []
    channels:
      "claude-*": ["primary"]

proxy:
  enabled: true
  type: "socks5"
  addr: "127.0.0.1:1080"

stats:
  enabled: true
  interval: "5m"
  request_count: 100

# Automatic retry/failover
retry:
  enabled: true
  max_attempts: 3
  status_codes: [429, 500, 502, 503, 504]

# Parameter override — modify request fields before forwarding
param_override:
  - path: "temperature"
    mode: "set"
    value: 0.7

# Channel health check
health_check:
  enabled: true
  interval: "30s"
  timeout: "5s"
  unhealthy_threshold: 3
  healthy_threshold: 1
```

### Configuration Reference

| Field | Description |
|-------|-------------|
| `server.port` | Listen port, default 8080 |
| `channels` | Upstream channel list (name, API key, base URL, format, models) |
| `default_channel` | Fallback channel when no API key matches |
| `model_aliases` | Model alias mapping, client uses alias, proxy translates to upstream model |
| `api_keys[].key` | Client API key |
| `api_keys[].name` | Human-readable name for logging |
| `api_keys[].allowed` | Allowed models (empty = all allowed) |
| `api_keys[].denied` | Denied models |
| `api_keys[].channels` | Model-to-channel routing with `*` wildcard support |
| `proxy.enabled` | Enable SOCKS5 proxy |
| `stats.enabled` | Enable token usage statistics |
| `stats.interval` | Periodic stats output (e.g. `"5m"`, `"1h"`) |
| `stats.request_count` | Stats output after N requests (0 = disabled) |
| `retry.enabled` | Enable automatic retry on upstream failure |
| `retry.max_attempts` | Maximum retry attempts across channels |
| `retry.status_codes` | HTTP status codes that trigger retry |
| `param_override[].path` | Request field to modify (e.g. `"model"`, `"temperature"`) |
| `param_override[].mode` | `set`, `delete`, `prepend`, or `append` |
| `param_override[].value` | Value for `set`/`prepend`/`append` |
| `health_check.enabled` | Enable background channel health probing |
| `health_check.interval` | Check interval (e.g. `"30s"`) |
| `health_check.timeout` | Request timeout per check (e.g. `"5s"`) |
| `health_check.unhealthy_threshold` | Consecutive failures to mark unhealthy |
| `health_check.healthy_threshold` | Consecutive successes to mark healthy |

## API

### Chat Completions

```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-user-1" \
  -d '{
    "model": "claude-sonnet-4-6",
    "messages": [{"role": "user", "content": "Hello"}],
    "stream": true
  }'
```

### List Models

```bash
# All models
curl http://localhost:8080/v1/models

# Filtered by API key
curl -H "Authorization: Bearer sk-user-1" http://localhost:8080/v1/models

# Single model info
curl http://localhost:8080/v1/models/claude-sonnet-4-6
# Returns: {"id":"claude-sonnet-4-6","object":"model","owned_by":"llm-proxy","alias_for":"model-a"}
```

### Route Selection

The proxy selects an upstream channel in this order:

1. `X-Channel` header — explicit channel name
2. `X-Target-Format` header — channel format
3. `Authorization` / `X-API-Key` — API key routing
4. `default_channel` — fallback

### Supported API Key Headers

| Header | Description |
|--------|-------------|
| `Authorization: Bearer <key>` | Standard OpenAI format |
| `X-API-Key: <key>` | Generic format |
| `x-goog-api-key: <key>` | Gemini format |

## Format Support

| Client Format | Upstream Format | Direction |
|---|---|---|
| Claude Messages | OpenAI | Claude → OpenAI |
| Gemini | OpenAI | Gemini → OpenAI |
| OpenAI | Gemini | OpenAI → Gemini |
| OpenAI | OpenAI | Passthrough |

### Claude Format Support

- `text` / `image` / `tool_use` / `tool_result` content types
- System prompt (string and array)
- Streaming events: `message_start`, `content_block_start`, `content_block_delta`, `content_block_stop`, `message_delta`, `message_stop`
- Thinking tags (`thinking`) auto-detected and converted

### Gemini Format Support

- `contents` / `systemInstruction` message format
- `functionCall` / `functionResponse` tool calls
- `thought` thinking content
- Streaming response converted to Gemini format

## Token Statistics

Per-API-key token usage is tracked and logged. Two trigger modes can be configured independently:

```yaml
stats:
  enabled: true
  interval: "5m"        # Print stats every 5 minutes
  request_count: 100    # Print stats after every 100 requests
```

Example output:

```
[stats] === interval:5m0s ===
[stats]   api_key:claude-user          | requests=     5 | input_tokens=        55 | output_tokens=       250
[stats]   api_key:openai-user          | requests=     2 | input_tokens=        22 | output_tokens=       100
[stats]   (total)                 | requests=     7 | input_tokens=        77 | output_tokens=       350
```

Requests without an API key are counted under `(anonymous)`.

## Token Counting

Input tokens are counted locally using tiktoken before sending to upstream.
This ensures accurate token statistics even when the upstream doesn't return usage data,
and enables quota checks before the request is sent.

## Retry / Failover

When an upstream channel returns a retriable status code (429/500/502/503/504 by default),
the proxy automatically retries the request on the next healthy channel:

```yaml
retry:
  enabled: true
  max_attempts: 3          # Try up to 3 channels
  status_codes: [429, 500, 502, 503, 504]
```

Channels are tried in health order: healthy > unknown > unhealthy.

## Parameter Override

Modify request fields before forwarding to upstream. Supports four modes:

| Mode | Description |
|------|-------------|
| `set` | Replace the field value entirely |
| `delete` | Remove the field |
| `prepend` | Prepend value to existing string |
| `append` | Append value to existing string |

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

## Channel Health Check

Background goroutine pings each channel periodically to determine health status.
Unhealthy channels are deprioritized in the retry pool.

```yaml
health_check:
  enabled: true
  interval: "30s"
  timeout: "5s"
  unhealthy_threshold: 3
  healthy_threshold: 1
```

### Health Status API

```bash
curl http://localhost:8080/health/channels
```

Returns:

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

Possible statuses: `healthy`, `unhealthy`, `unknown`.

## Build

```bash
# Static binary
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -buildid=" -o llm-proxy
```

## Use Cases

### Scenario 1: Access OpenAI upstream via Claude/Gemini CLI

```bash
export ANTHROPIC_BASE_URL=http://your-proxy:8080/v1
export ANTHROPIC_AUTH_TOKEN=sk-claude-user-1
claude -p "hello" --model claude-sonnet-4-6
```

### Scenario 2: Multi-user model authorization

```yaml
api_keys:
  - key: "sk-team-dev"
    name: "dev-team"
    allowed:
      - "claude-sonnet-4-6"
      - "gpt-4o"

  - key: "sk-team-test"
    name: "test-team"
    allowed:
      - "gpt-3.5-turbo"
    denied:
      - "claude-opus-4-7"
```

### Scenario 3: Model-based channel routing

Route different models to different upstream channels:

```yaml
api_keys:
  - key: "sk-user-1"
    channels:
      "claude-*": ["claude-channel"]
      "gpt-*": ["openai-channel"]
```
