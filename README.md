<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo-light.png">
    <source media="(prefers-color-scheme: light)" srcset="assets/logo-dark.png">
    <img src="assets/logo-dark.png" alt="Diction" height="50">
  </picture>
  <br><br>
  <strong>You talk. We type.</strong>
  <br><br>
  Voice keyboard for iOS. Works in every app.<br>On-device, cloud, or self-hosted — no limits.
</p>

<p align="center">
  <a href="https://apps.apple.com/app/id6759807364"><img src="https://developer.apple.com/assets/elements/badges/download-on-the-app-store.svg" alt="Download on the App Store" height="40"></a>
</p>

<p align="center">
  <a href="https://diction.one">Website</a> &bull;
  <a href="https://diction.one/self-hosted">Self-Hosting Guide</a> &bull;
  <a href="https://diction.one/privacy">Privacy Policy</a>
</p>

<p align="center">
  <a href="https://github.com/omachala/diction/blob/main/LICENSE"><img src="https://img.shields.io/github/license/omachala/diction?style=for-the-badge" alt="License"></a>
</p>

---

<p align="center">
  <img src="assets/slide-01.png" width="200" alt="You talk. We type.">&nbsp;
  <img src="assets/slide-02.png" width="200" alt="No limits. No word caps. No catch.">&nbsp;
  <img src="assets/slide-03.png" width="200" alt="What you say stays with you.">&nbsp;
  <img src="assets/slide-04.png" width="200" alt="Self-host. Your server, your rules.">
</p>

## Why Diction?

- **Works in every app.** Tap the mic, speak, watch text land in whatever app you're in — Telegram, Mail, Notes, the search bar, anywhere a keyboard appears.
- **Self-hosted in minutes.** `docker compose up -d` and paste your server's IP. Your hardware, your models, your data.
- **Works with any Whisper-compatible server.** The gateway speaks the OpenAI transcription API (`POST /v1/audio/transcriptions`). Point it at any endpoint that implements it.
- **On-device.** Whisper runs locally on your iPhone via WhisperKit. No network, no server, nothing leaves the device.
- **AI transcript cleanup.** Wire any OpenAI-compatible LLM — OpenAI, Groq, Ollama, Anthropic — into the gateway to strip filler words and fix punctuation before text reaches the app. BYO prompt.
- **End-to-end encrypted.** AES-256-GCM with X25519 key exchange between the app and the gateway. Same primitives used by Signal and WireGuard.
- **Zero tracking in the app.** No analytics, no telemetry, no data collection. Audit the source yourself.
- **Free and unlimited.** On-device and self-hosted modes have no caps, no word limits, no expiry.

## Self-Hosting

The Diction app streams audio over a WebSocket connection, so you need the Diction Gateway in front of whatever speech model you run. The gateway handles the WebSocket protocol, end-to-end encryption, optional LLM cleanup, and model routing. The speech model container handles the actual transcription.

> **Full walkthrough with screenshots:** [How to Set Up Diction — the self-hosted speech-to-text alternative to Wispr Flow](https://dev.to/omachala/how-to-set-up-diction-the-self-hosted-speech-to-text-alternative-to-wispr-flow-20km)

### Requirements

- Any machine that can run Docker: Mac, Linux box, NUC, home server, VPS. Apple Silicon works (via Rosetta).
- iPhone running iOS 17.0 or later.
- For remote access: both on the same network, or Tailscale/Cloudflare Tunnel (see [Reach From Anywhere](#reach-from-anywhere)).

### Step 1 — Install Docker

Install [Docker Desktop](https://www.docker.com/products/docker-desktop/) on macOS or Windows. On Linux:

```bash
# Ubuntu / Debian
sudo apt update && sudo apt install docker.io docker-compose-plugin

# Add yourself to the docker group (log out and back in after)
sudo usermod -aG docker "$USER"
```

Verify it works:

```bash
docker --version
docker compose version
```

**Apple Silicon note:** Open Docker Desktop → Settings → General → enable "Use Rosetta for x86/amd64 emulation". The gateway image is amd64-only; Rosetta handles it transparently.

**Memory note:** The default Docker Desktop VM is 2 GB. Bump it to 4 GB (Settings → Resources → Memory) if you plan to run `medium` (~2.1 GB) or larger models.

### Step 2 — Write the Compose File

Create a folder for the stack and save this as `docker-compose.yml`:

```yaml
services:
  whisper-small:
    image: fedirz/faster-whisper-server:latest-cpu
    container_name: diction-whisper-small
    restart: unless-stopped
    volumes:
      - whisper-models:/root/.cache/huggingface
    environment:
      WHISPER__MODEL: Systran/faster-whisper-small
      WHISPER__INFERENCE_DEVICE: cpu

  gateway:
    image: ghcr.io/omachala/diction-gateway:latest
    platform: linux/amd64
    container_name: diction-gateway
    restart: unless-stopped
    ports:
      - "8080:8080"
    depends_on:
      - whisper-small
    environment:
      DEFAULT_MODEL: small

volumes:
  whisper-models:
```

**What each piece does:**

- `whisper-small` — [faster-whisper-server](https://github.com/fedirz/faster-whisper-server) wraps OpenAI's Whisper in a Python server that speaks the OpenAI transcription API. The `latest-cpu` tag is the CPU build; swap to `latest-cuda` for NVIDIA.
- `volumes: whisper-models` — persists the model weights (~500 MB for `small`) across container rebuilds. Without this, the model re-downloads on every restart.
- `gateway` — the Diction Gateway. Handles the WebSocket streaming protocol, end-to-end encryption, model routing, and optional LLM cleanup.
- `platform: linux/amd64` — required on Apple Silicon so Docker uses Rosetta for the gateway image. Drop this line on native x86.
- `depends_on` — starts whisper first so the gateway doesn't log spurious connection errors on startup.
- `DEFAULT_MODEL: small` — tells the gateway which backend to route to when the iPhone doesn't specify a model. The value `small` maps to a service named `whisper-small` on port 8000. See [Swap the Speech Model](#swap-the-speech-model) if you change the model.

### Step 3 — Start the Stack

```bash
docker compose up -d
```

First run pulls the images and downloads the model weights. Give it 2–3 minutes. Watch progress with:

```bash
docker compose logs -f
```

Check everything came up:

```bash
docker compose ps
```

Expected output:

```
NAME                     STATUS
diction-gateway          Up 30 seconds
diction-whisper-small    Up 2 minutes (healthy)
```

Common startup errors:

| Error | Fix |
|-------|-----|
| `pull access denied` on gateway image | Run `docker logout ghcr.io` and retry |
| `exec format error` on Apple Silicon | Enable Rosetta in Docker Desktop → Settings → General |
| `health: starting` for > 3 minutes | Model still downloading — check `docker compose logs -f whisper-small` |
| Gateway exits immediately | Likely the whisper container failed to start; check its logs |

### Step 4 — Test the Server

Before touching the iPhone, verify the server works from the terminal. You need an audio file — record a voice memo on your phone and AirDrop it over, or on macOS:

```bash
say -o test.aiff "Hello from my home server"
```

Hit the gateway:

```bash
curl -X POST http://localhost:8080/v1/audio/transcriptions \
  -F "file=@test.aiff" \
  -F "model=small"
```

Expected response:

```json
{"text":"Hello from my home server."}
```

Check response headers for timing info:

```bash
curl -sS -D - -o /dev/null \
  -X POST http://localhost:8080/v1/audio/transcriptions \
  -F "file=@test.aiff" -F "model=small" | grep -i diction
```

You should see `X-Diction-Whisper-Ms` — the speech model's inference latency in milliseconds.

Troubleshooting:

| Response | Cause |
|----------|-------|
| Connection refused | Gateway not running — `docker compose ps` |
| 504 Gateway Timeout | Whisper still loading model into RAM — wait 60s |
| 404 Not Found | URL typo — path must be exactly `/v1/audio/transcriptions` |
| OOM / container crash | Model too large for available RAM — try `small` |

### Step 5 — Find Your Server's IP

Your iPhone needs the server's local network address.

**macOS:**
```bash
ipconfig getifaddr en0
# or, to catch all interfaces:
ifconfig | grep 'inet ' | grep -v 127.0.0.1
```

**Linux:**
```bash
hostname -I | awk '{print $1}'
```

**Windows:**
```powershell
ipconfig | findstr IPv4
```

Pick the `192.168.x.x` or `10.x.x.x` address. Ignore anything starting with `100.` — that's Tailscale.

**Keep it stable:** DHCP leases expire. Log into your router and set a DHCP reservation for the server's MAC address so it always gets the same IP. Alternatively, use [Tailscale](#reach-from-anywhere) — it gives every device a stable address that doesn't change.

### Step 6 — Connect the App

Install [Diction](https://apps.apple.com/app/id6759807364) on your iPhone.

On first launch, the app walks you through adding the keyboard:

1. Settings → General → Keyboard → Keyboards → Add New Keyboard → **Diction**
2. Tap Diction in the list and enable **Allow Full Access** (required for any keyboard that makes network requests)
3. Grant microphone access when prompted

Then point the app at your server:

1. Open the Diction app → **Preferences**
2. Tap **Mode** → select **Self-Hosted**
3. Enter your endpoint: `http://192.168.1.42:8080` (your IP from Step 5)
4. Tap **Test connection** — you should get a green check within a second

To use the keyboard: open any app that accepts text, tap the text field, long-press the globe icon (bottom-left of the default iOS keyboard), and pick **Diction**. Tap the mic button, speak, release. Text lands in the field.

### Reach From Anywhere

The setup above only works on your home network. Three clean ways to access it from anywhere:

**Tailscale (recommended for personal use)**

[Tailscale](https://tailscale.com/) creates a private WireGuard mesh between your devices. Install it on the server and on the iPhone, sign in to the same account, and you get a stable `100.x.x.x` address that works on any network.

```bash
# Linux server
curl -fsSL https://tailscale.com/install.sh | sh
sudo tailscale up
tailscale ip -4   # note this address
```

Install the Tailscale app on iPhone, sign in. Update Diction's endpoint to `http://100.x.x.x:8080`. Works on cellular, café WiFi, anywhere. Free for personal use.

**Cloudflare Tunnel (pretty URL, no port forwarding)**

Add this service to your compose file:

```yaml
  cloudflared:
    image: cloudflare/cloudflared:latest
    container_name: diction-cloudflared
    restart: unless-stopped
    command: tunnel --no-autoupdate run
    environment:
      TUNNEL_TOKEN: "${CLOUDFLARE_TUNNEL_TOKEN}"
```

Create a tunnel in the [Cloudflare Zero Trust dashboard](https://one.dash.cloudflare.com/), grab the token, add it to `.env`, and set the public hostname to route to `http://gateway:8080`. You get a `https://dictation.yourdomain.com` URL. Free tier. Note: transcripts pass through Cloudflare's network (encrypted via HTTPS, but a third party is in the path).

**ngrok (quick testing)**

```bash
ngrok http 8080
```

Prints a public URL instantly. Good for a demo. Free tier URLs change on restart, which is annoying for daily use.

---

## Swap the Speech Model

`small` is the starter. Swap by changing two lines in your compose file:

| `DEFAULT_MODEL` | Service name | `WHISPER__MODEL` | RAM | Notes |
|-----------------|--------------|------------------|-----|-------|
| `small` | `whisper-small` | `Systran/faster-whisper-small` | ~850 MB | Best for CPU |
| `medium` | `whisper-medium` | `Systran/faster-whisper-medium` | ~2.1 GB | More accurate, slower on CPU |
| `large-v3-turbo` | `whisper-large-turbo` | `deepdml/faster-whisper-large-v3-turbo-ct2` | ~2.3 GB | Best with NVIDIA GPU |
| `parakeet-v3` | `parakeet` | — (baked into image) | ~2 GB | NVIDIA GPU, 25 European languages |

**Two rules when swapping:**
1. Set `DEFAULT_MODEL` on the gateway to the short name in the table.
2. Name the service exactly as shown — the gateway resolves backends by Docker service hostname. A mismatch gives 404 on every request.

Apply changes: `docker compose up -d`. Docker recreates only the changed container; the other keeps running.

---

## NVIDIA GPU

If the server has an NVIDIA card, you can skip `small` and run models that beat most paid cloud services.

Install the [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html) on the host first.

### Option A — Parakeet TDT 0.6B v3 (fastest, 25 European languages)

[Parakeet](https://huggingface.co/nvidia/parakeet-tdt-0.6b-v3) is NVIDIA's speech engine. On a consumer GPU it transcribes a 5-second clip in well under a second.

| | Whisper Large-v3 | Parakeet TDT 0.6B v3 |
|---|---|---|
| WER (English) | 7.4% | ~6.3% |
| Latency (GPU) | Under 2s | Sub-second |
| VRAM (INT8) | ~2.3 GB | ~2 GB |
| Languages | 99 | 25 European |

**Supported languages:** English, Bulgarian, Croatian, Czech, Danish, Dutch, Estonian, Finnish, French, German, Greek, Hungarian, Italian, Latvian, Lithuanian, Maltese, Polish, Portuguese, Romanian, Slovak, Slovenian, Spanish, Swedish, Russian, Ukrainian.

For languages outside this list, use Option B.

```yaml
services:
  parakeet:
    image: ghcr.io/achetronic/parakeet:latest-int8
    container_name: diction-parakeet
    restart: unless-stopped
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: 1
              capabilities: [gpu]

  gateway:
    image: ghcr.io/omachala/diction-gateway:latest
    platform: linux/amd64
    container_name: diction-gateway
    restart: unless-stopped
    ports:
      - "8080:8080"
    depends_on:
      - parakeet
    environment:
      DEFAULT_MODEL: parakeet-v3
```

Model weights are baked into the Parakeet image — no download delay on first start.

Or use the profile from this repo's compose file:

```bash
docker compose --profile parakeet up -d
```

### Option B — large-v3-turbo (multilingual, 99 languages)

```yaml
services:
  whisper-large-turbo:
    image: fedirz/faster-whisper-server:latest-cuda
    container_name: diction-whisper-large-turbo
    restart: unless-stopped
    volumes:
      - whisper-models:/root/.cache/huggingface
    environment:
      WHISPER__MODEL: deepdml/faster-whisper-large-v3-turbo-ct2
      WHISPER__INFERENCE_DEVICE: cuda
      WHISPER__COMPUTE_TYPE: float16
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: 1
              capabilities: [gpu]

  gateway:
    image: ghcr.io/omachala/diction-gateway:latest
    platform: linux/amd64
    container_name: diction-gateway
    restart: unless-stopped
    ports:
      - "8080:8080"
    depends_on:
      - whisper-large-turbo
    environment:
      DEFAULT_MODEL: large-v3-turbo

volumes:
  whisper-models:
```

First boot downloads ~1.6 GB of model weights into the volume. Subsequent starts are instant.

---

## Already Have a Voice Server?

If you already run a speech server — a local `faster-whisper-server`, a company internal API, a fine-tuned model — keep it. You only need the Diction Gateway in front of it for WebSocket streaming and end-to-end encryption. Use `CUSTOM_BACKEND_URL`:

```yaml
services:
  gateway:
    image: ghcr.io/omachala/diction-gateway:latest
    platform: linux/amd64
    container_name: diction-gateway
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      CUSTOM_BACKEND_URL: http://your-existing-server:8000
      CUSTOM_BACKEND_MODEL: Systran/faster-whisper-small
```

Additional knobs:

| Variable | Description |
|----------|-------------|
| `CUSTOM_BACKEND_AUTH` | Authorization header forwarded to your backend, e.g. `Bearer sk-xxx` |
| `CUSTOM_BACKEND_NEEDS_WAV` | Set to `"true"` if your backend only accepts WAV — the gateway converts with ffmpeg |
| `CUSTOM_BACKEND_CANONICAL_ID` | HuggingFace-style ID advertised via `/v1/models` (default: `CUSTOM_BACKEND_MODEL`) |

Point the iPhone at the gateway (`http://your-server:8080`). Your existing speech server stays untouched.

---

## AI Cleanup (BYO LLM)

The gateway can pass transcripts through any LLM before returning them to the app. You say "so um basically the meeting went well and uh they agreed to the timeline." The LLM returns "The meeting went well. They agreed to the timeline." Any OpenAI-compatible provider works: OpenAI, Groq, Anthropic, Ollama on the same machine.

The app sends `?enhance=true` when its **AI Companion** toggle is on. The gateway forwards the transcript to `{LLM_BASE_URL}/chat/completions` with your prompt and returns the cleaned text. If the LLM errors out, the gateway falls back to the raw transcript — dictation never breaks.

### Configuration

Four environment variables on the gateway:

| Variable | Required | Description |
|----------|----------|-------------|
| `LLM_BASE_URL` | Yes | OpenAI-compatible endpoint, e.g. `https://api.openai.com/v1` |
| `LLM_MODEL` | Yes | Model identifier, e.g. `gpt-4o-mini` |
| `LLM_API_KEY` | No | Bearer token. Not needed for local Ollama. |
| `LLM_PROMPT` | No | System prompt string, or a file path starting with `/` (mount via volume) |

Both `LLM_BASE_URL` and `LLM_MODEL` must be set. If either is missing, the feature is off.

### Option A — Cloud LLM (OpenAI, Groq, etc.)

Create `.env` in the same folder as your compose file:

```bash
echo "OPENAI_API_KEY=sk-your-key-here" > .env
```

Add to the `gateway` environment:

```yaml
environment:
  DEFAULT_MODEL: small
  LLM_BASE_URL: "https://api.openai.com/v1"
  LLM_API_KEY: "${OPENAI_API_KEY}"
  LLM_MODEL: "gpt-4o-mini"
  LLM_PROMPT: "Clean up this voice transcription. Remove filler words (um, uh, like). Fix punctuation and capitalization. Return only the cleaned text, nothing else."
```

Docker Compose reads `${OPENAI_API_KEY}` from `.env` automatically. Works with any OpenAI-compatible provider — Groq, Together, Fireworks, Mistral, OpenRouter — just swap `LLM_BASE_URL` and `LLM_MODEL`.

### Option B — Local Ollama (zero cost, fully private)

Add Ollama as a service:

```yaml
  ollama:
    image: ollama/ollama:latest
    container_name: diction-ollama
    restart: unless-stopped
    volumes:
      - ollama-models:/root/.ollama

volumes:
  whisper-models:
  ollama-models:
```

Update the gateway:

```yaml
  gateway:
    environment:
      DEFAULT_MODEL: small
      LLM_BASE_URL: "http://ollama:11434/v1"
      LLM_MODEL: "gemma2:9b"
      LLM_PROMPT: "Clean up this voice transcription. Remove filler words. Fix punctuation and capitalization. Return only the cleaned text, nothing else."
```

Start and pull a model:

```bash
docker compose up -d
docker exec diction-ollama ollama pull gemma2:9b
```

Model recommendations:

| Model | Memory | Notes |
|-------|--------|-------|
| `gemma2:9b` | ~6 GB | Best cleanup quality at this size |
| `qwen2.5:7b` | ~5 GB | Strong instruction following |
| `llama3.1:8b` | ~5 GB | Most popular, well-tested |
| `gemma3:4b` | ~3 GB | For tighter machines |

Models under 7B tend to answer questions about the transcript instead of cleaning it up. 7B or larger recommended.

### Test cleanup

```bash
curl -X POST "http://localhost:8080/v1/audio/transcriptions?enhance=true" \
  -F "file=@test.aiff" \
  -F "model=small"
```

Confirm the LLM fired by checking response headers:

```bash
curl -sS -D - -o /dev/null \
  -X POST "http://localhost:8080/v1/audio/transcriptions?enhance=true" \
  -F "file=@test.aiff" -F "model=small" | grep -i diction
```

You should see both `X-Diction-Whisper-Ms` and `X-Diction-LLM-Ms`.

### Prompt file (for longer prompts)

Mount a file and point `LLM_PROMPT` at the path:

```yaml
  gateway:
    volumes:
      - ./cleanup-prompt.txt:/config/prompt.txt:ro
    environment:
      LLM_PROMPT: "/config/prompt.txt"
```

If `LLM_PROMPT` starts with `/`, the gateway reads it as a file. Otherwise it uses the string directly.

---

## NixOS

The repo ships a flake with a hardened systemd module — no Docker needed.

Try it without committing to anything:

```bash
nix run github:omachala/diction#diction-gateway
```

Enable as a service:

```nix
{
  inputs.diction.url = "github:omachala/diction";

  outputs = { nixpkgs, diction, ... }: {
    nixosConfigurations.your-host = nixpkgs.lib.nixosSystem {
      modules = [
        diction.nixosModules.default
        {
          services.diction-gateway = {
            enable = true;
            openFirewall = true;
            # customBackend.url = "http://127.0.0.1:8000";
            # llm.baseUrl = "http://127.0.0.1:11434/v1";
            # llm.model = "gemma2:9b";
            # environmentFile = "/run/secrets/diction-gateway.env";
          };
        }
      ];
    };
  };
}
```

The unit runs under `DynamicUser` with `ProtectSystem=strict`, `NoNewPrivileges`, and a narrow syscall filter. Use `environmentFile` for secrets like `CUSTOM_BACKEND_AUTH` and `LLM_API_KEY` so they don't end up in the world-readable Nix store. Full option list: [`nix/module.nix`](nix/module.nix).

---

## OpenAI API Compatibility

The gateway implements the OpenAI audio transcription API. Any client that works against `api.openai.com/v1/audio/transcriptions` works against a Diction gateway.

**Quickstart with the Python SDK:**

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://your-server:8080/v1",
    api_key="anything",  # not checked when AUTH_ENABLED=false
)

with open("audio.wav", "rb") as f:
    result = client.audio.transcriptions.create(
        file=f,
        model="small",            # or "Systran/faster-whisper-small"
        response_format="text",
    )
print(result)
```

Works with the Node SDK, LangChain, Flowise, n8n, or any tool that expects OpenAI's speech API.

**Supported:**

- `POST /v1/audio/transcriptions` — `file`, `model`, `language`, `prompt`, `response_format=json|text`
- `GET /v1/models` — returns both an OpenAI-compatible `data[]` array and a `providers[]` grouping consumed by the iOS app. HuggingFace model IDs (`Systran/faster-whisper-small`, `nvidia/parakeet-tdt-0.6b-v3`, etc.) and short aliases (`small`, `medium`, `large-v3-turbo`, `parakeet-v3`) are both accepted.
- WebSocket `/v1/audio/stream` — used by the Diction app for low-latency streaming

**Not supported:**

- TTS (`/v1/audio/speech`)
- `response_format=verbose_json|srt|vtt` (no word-level timestamps)
- SSE streaming on the REST endpoint (use WebSocket `/v1/audio/stream` instead)
- Model download/delete (`POST`/`DELETE /v1/models/{id}`)
- OpenAI Realtime API (`/v1/realtime`)

**Authentication** is off by default (`AUTH_ENABLED=false`). Pass any non-empty string as the API key from the client — the gateway doesn't check it. To lock down a public-facing deployment, set `AUTH_ENABLED=true` and configure tokens in the gateway env.

**Error shape caveat:** errors return `{"error":"<message>"}`, not OpenAI's nested `{"error":{"message":"...","type":"..."}}`. Most SDKs surface these as raw `HTTPError` rather than `APIError` — catch both if you're writing defensive code.

---

## Privacy

Keyboards can read everything you type. Here's exactly what Diction does with your audio:

- **On-device**: Everything stays on your phone. No network connection is made.
- **Self-hosted**: Audio goes to your server only. Nothing else sees it. Neither the gateway nor `faster-whisper-server` persists audio — it's transcribed and discarded.
- **If AI cleanup is enabled**: The transcript (plain text, no audio) goes to your configured LLM endpoint. If you use Ollama locally, nothing leaves your machine.
- **Diction One (cloud)**: Audio is transcribed and immediately discarded. Not stored, not used for training.
- **Zero third-party SDKs** in the app. No analytics, no tracking, no telemetry of any kind.
- **Full Access** is required by iOS for any keyboard that makes network requests. Diction has no QWERTY input to log — the only data that leaves the app is the audio recording, sent to the endpoint you configured.

Read the full [Privacy Policy](https://diction.one/privacy).

---

## Diction One

On-device and self-hosted are completely free with no word limits.

If you don't want to run a server, Diction One gives you a fine-tuned cloud model with advanced audio filtering — without the setup. Audio is sent to the Diction endpoint, transcribed, and immediately discarded. Pricing and trial details are in the app.

---

## Requirements

- **iOS 17.0+** (iPhone)
- For self-hosting: any machine that can run Docker

---

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT. See [LICENSE](LICENSE).

The iOS app is distributed via the App Store. This repository contains the self-hosting infrastructure and documentation.
