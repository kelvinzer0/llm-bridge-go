# llm-bridge-go 🚀

High-performance, lightweight self-hosted bridge server written in Go that turns web AI chatbots (via the ZeroLLM Chrome extension) into standard OpenAI-compatible endpoints (`/v1/chat/completions`, `/v1/models`, `/v1/responses`, `/v1/embeddings`).  
Replaces `llm-bridge-cf` (Cloudflare Workers) to bypass tier limitations, request caps, and cloud cost.

---

## ✨ Features

- **OpenAI Compatible**: 100% compatible with OpenAI SDKs, LangChain, LlamaIndex, LiteLLM, Open WebUI, Cline, Roo Code, etc.
- **SSE Streaming Support**: True Server-Sent Events (SSE) streaming with `data: {...}` and `data: [DONE]`.
- **Tool Calling Support**: Supports native tool calling and function calls forwarding.
- **Zero Cloud Cost & Tier Limits**: Host on any VPS, local machine, Raspberry Pi, or private container.
- **Ultra Lightweight**: Minimal memory consumption (< 20MB RAM) powered by Go's high-concurrency runtime.
- **Token & Room Authentication**: Automatic token parsing from `Authorization: Bearer <token>_<roomId>`.
- **Automatic Multi-Platform Releases**: GitHub Actions CI produces native binaries for Linux, macOS, and Windows (amd64 and arm64).

---

## 📡 API Endpoints

| Method | Endpoint | Description |
|---|---|---|
| `GET` / `POST` | `/new` | Create a new room, API Key, and setup URLs |
| `GET` | `/health?room=<roomId>` | Health status, extension connection, registered models |
| `GET` | `/ws/extension?room=<roomId>` | WebSocket endpoint for ZeroLLM Chrome extension |
| `POST` | `/v1/chat/completions` | Standard OpenAI Chat Completions (streaming & non-streaming) |
| `POST` | `/v1/responses` | OpenAI Responses API |
| `POST` | `/v1/embeddings` | OpenAI Embeddings |
| `GET` | `/v1/models` | List available models registered by the extension |
| `GET` | `/v1/models/{id}` | Retrieve specific model details |

---

## 🛠️ Quick Start

### 1. Download Pre-built Binary
Download the binary for your platform from the [GitHub Releases](https://github.com/kelvinzer0/llm-bridge-go/releases) page.

```bash
# Example for Linux amd64
tar -xvf llm-bridge-go-linux-amd64.tar.gz
./llm-bridge-go -port 8080
```

### 2. Build from Source
```bash
git clone https://github.com/kelvinzer0/llm-bridge-go.git
cd llm-bridge-go
go build -o llm-bridge-go .
./llm-bridge-go -port 8080
```

### 3. Run with Docker
```bash
docker build -t llm-bridge-go .
docker run -d -p 8080:8080 --name llm-bridge llm-bridge-go
```

---

## 🔌 Connecting ZeroLLM Chrome Extension

1. Start your `llm-bridge-go` server (e.g. on your VPS at `https://llm.example.com` or `http://localhost:8080`).
2. Generate a room:
   ```bash
   curl https://llm.example.com/new
   ```
   Output:
   ```json
   {
     "room": "a1b2c3d4",
     "extension_url": "wss://llm.example.com/ws/extension?room=a1b2c3d4",
     "api_base_url": "https://llm.example.com/v1",
     "api_key": "abc123token_a1b2c3d4",
     "health_url": "https://llm.example.com/health?room=a1b2c3d4"
   }
   ```
3. Paste the `extension_url` into your ZeroLLM extension settings.
4. Use `api_base_url` and `api_key` in any OpenAI-compatible client!

---

## ⚙️ Configuration & Flags

| Flag | Env Var | Default | Description |
|---|---|---|---|
| `-port` | `PORT` | `8080` | HTTP port to listen on |
| `-host` | - | `0.0.0.0` | IP host to bind to |

---

## 📦 CI / CD Automated Releases

Releases are automatically triggered when a Git tag starting with `v*` (e.g., `v1.0.0`) is pushed:

```bash
git tag v1.0.0
git push origin v1.0.0
```
GitHub Actions cross-compiles all target architectures, packages `.tar.gz` and `.zip`, generates SHA256 checksums, and publishes a GitHub Release automatically.
