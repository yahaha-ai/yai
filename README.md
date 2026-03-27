# yai — Yet Another AI proxy

[![CI](https://github.com/yahaha-ai/yai/actions/workflows/ci.yml/badge.svg)](https://github.com/yahaha-ai/yai/actions/workflows/ci.yml)

A lightweight reverse proxy for LLM APIs with multi-provider fallback, health checking, and key injection. Written in Go, zero external dependencies beyond `gopkg.in/yaml.v3`.

## Features

- **Reverse proxy** — route `/proxy/{provider}/...` to upstream APIs
- **SSE streaming** — transparent passthrough with real-time flushing
- **API key injection** — strip client tokens, inject real provider keys
- **OAuth2** — auto-refreshing tokens for Azure AD, GCP, Baidu, etc.
- **Health checking** — periodic upstream probes with configurable interval/timeout
- **Fallback** — priority-based failover across provider groups (retry on 429/5xx, pass through 4xx)
- **Bearer auth** — protect the proxy with your own tokens

## Quick Start

```bash
go build ./cmd/yai/
cp yai.example.yaml yai.yaml   # edit with your real keys
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

## Auth Types

| Type | Use Case | Docs |
|------|----------|------|
| `bearer` | OpenAI, DeepSeek, etc. | Static `Authorization: Bearer <key>` |
| `x-api-key` | Anthropic | Static `X-Api-Key: <key>` header |
| `query-param` | Google AI Studio | Appends `?key=<key>` to URL ([docs](https://ai.google.dev/gemini-api/docs/api-key)) |
| `none` | Ollama, local models | No auth header forwarded |
| `oauth2-client-credentials` | Baidu ERNIE, custom OAuth2 | Auto-refreshing [client credentials](https://datatracker.ietf.org/doc/html/rfc6749#section-4.4) flow |
| `oauth2-service-account` | Google Vertex AI | [GCP service account](https://cloud.google.com/iam/docs/service-account-overview) JWT → access token |
| `oauth2-azure-ad` | Azure OpenAI | [Microsoft Entra ID](https://learn.microsoft.com/en-us/azure/ai-services/openai/how-to/managed-identity) client credentials |

## Configuration

See [yai.example.yaml](yai.example.yaml) for a full example with all auth types.

## Architecture

```
Client → [Auth] → [Fallback] → [Proxy] → Upstream API
                       ↓
                  [Health Checker]
```

## Development

```bash
# Test (98 tests across 7 packages)
go test ./... -race

# Lint (requires golangci-lint)
golangci-lint run ./...
```

CI runs lint, test (`-race`), and build on every push/PR to `main`.

## License

MIT
