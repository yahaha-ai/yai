# yai — Yet Another AI proxy

A lightweight reverse proxy for LLM APIs with multi-provider fallback, health checking, and key injection. Written in Go, zero external dependencies beyond `gopkg.in/yaml.v3`.

## Features

- **Reverse proxy** — route `/proxy/{provider}/...` to upstream APIs (Anthropic, OpenAI, DeepSeek, Ollama, etc.)
- **SSE streaming** — transparent passthrough with real-time flushing
- **API key injection** — strip client tokens, inject real provider keys (Bearer, X-Api-Key, or none)
- **Health checking** — periodic upstream probes with configurable interval/timeout
- **Fallback** — priority-based failover across provider groups (retry on 429/5xx, pass through 4xx)
- **Bearer auth** — protect the proxy with your own tokens

## Quick Start

```bash
# Build
go build ./cmd/yai/

# Configure
cp yai.example.yaml yai.yaml
# Edit yai.yaml with your real API keys

# Run
./yai -config yai.yaml
```

## Usage

```bash
# Health check (no auth)
curl http://localhost:8080/health

# Proxy to Anthropic
curl http://localhost:8080/proxy/anthropic/v1/messages \
  -H "Authorization: Bearer yai_your_token" \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-sonnet-4-20250514","max_tokens":1024,"messages":[{"role":"user","content":"hi"}]}'

# Proxy to DeepSeek (with fallback to Ollama if configured)
curl http://localhost:8080/proxy/deepseek/v1/chat/completions \
  -H "Authorization: Bearer yai_your_token" \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}'
```

## Configuration

See [yai.example.yaml](yai.example.yaml) for a full example.

## Architecture

```
Client → [Auth] → [Fallback] → [Proxy] → Upstream API
                       ↓
                  [Health Checker]
```

## Tests

```bash
go test ./... -v
# 50 tests across 6 packages
```

## License

MIT
