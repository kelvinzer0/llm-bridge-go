# llm-bridge-go

Self-hosted HTTP/WebSocket bridge written in Go that translates web browser LLM interactions into standard OpenAI-compatible endpoints (`/v1/chat/completions`, `/v1/models`, `/v1/responses`, `/v1/embeddings`).

## Architecture & Security

- Default binding is strictly restricted to `127.0.0.1`. Global binding (`0.0.0.0`) is actively blocked to avoid exposing backend channels without reverse proxy authentication.
- Authentication format: `Authorization: Bearer <token>_<roomId>`.
- Supports SSE streaming (`stream: true`) with standard delta chunks and `data: [DONE]`.
- Supports native tool calling and function arguments.

## API Endpoints

- `GET /new`: Allocates a new session room, generates API credentials, and outputs setup URLs.
- `GET /health?room=<room_id>`: Reports active status, extension connectivity, and registered model list.
- `GET /ws/extension?room=<room_id>`: WebSocket connection endpoint used by the ZeroLLM browser extension.
- `POST /v1/chat/completions`: OpenAI Chat Completions API (streaming & non-streaming).
- `POST /v1/responses`: OpenAI Responses API.
- `POST /v1/embeddings`: OpenAI Embeddings API.
- `GET /v1/models`: Returns list of models registered by the connected browser tab.
- `GET /v1/models/{id}`: Returns metadata for a specific model.

## Installation on Linux

### Quick Install via Release Tarball

Download and extract the Linux tarball matching your system architecture:

```bash
tar -xvf llm-bridge-linux-amd64.tar.gz
cd llm-bridge-linux-amd64
sudo ./install.sh
```

The installer performs the following actions:
1. Installs binary to `/usr/local/bin/llm-bridge`.
2. Creates default configuration at `/etc/llm-bridge/llm-bridge.env`.
3. Registers and starts the service under `systemd` (or `init.d` fallback).

### Service Management

Control the daemon using standard Linux service commands:

```bash
service llm-bridge start
service llm-bridge stop
service llm-bridge restart
service llm-bridge status
```

Or using `systemctl`:

```bash
systemctl start llm-bridge
systemctl status llm-bridge
```

### Configuration

Edit `/etc/llm-bridge/llm-bridge.env`:

```ini
HOST=127.0.0.1
PORT=8080
```

Apply changes:

```bash
service llm-bridge restart
```

### Uninstallation

```bash
sudo ./uninstall.sh
```

## Building from Source

Requirements: Go 1.22+

```bash
git clone https://github.com/kelvinzer0/llm-bridge-go.git
cd llm-bridge-go
go build -trimpath -ldflags="-s -w" -o bin/llm-bridge ./cmd/llm-bridge
./bin/llm-bridge -host 127.0.0.1 -port 8080
```
