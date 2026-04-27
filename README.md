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
    channels:
      "claude-*": ["primary"]

proxy:
  enabled: true
  type: "socks5"
  addr: "127.0.0.1:1080"
```

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
```

### Route Selection

The proxy selects an upstream channel in this order:

1. `X-Channel` header — explicit channel name
2. `X-Target-Format` header — channel format
3. `Authorization` / `X-API-Key` — API key routing
4. `default_channel` — fallback

## Format Support

| Client Format | Upstream Format | Direction |
|---|---|---|
| Claude Messages | OpenAI | Claude → OpenAI |
| Gemini | OpenAI | Gemini → OpenAI |
| OpenAI | Gemini | OpenAI → Gemini |
| OpenAI | OpenAI | Passthrough |

## Build

```bash
# Static binary
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -buildid=" -o llm-proxy
```
