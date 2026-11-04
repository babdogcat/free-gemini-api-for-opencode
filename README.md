# Free Gemini API — Hardened Local Build

An OpenAI-compatible HTTP server that talks to the Google Gemini **web** backend, plus a Chrome extension that hands your browser session to the local server. This repository is a **hardened fork** of [`kodelyx/free-gemini-api`](https://github.com/kodelyx/free-gemini-api): every outbound/credential-exporting path that could leak your Google session has been removed or locked to loopback, and several upstream bugs (streaming, tool calls) are fixed.

<p>
  <img alt="Go" src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white">
  <img alt="OpenAI compatible" src="https://img.shields.io/badge/API-OpenAI%20compatible-FF6F00">
  <img alt="Local only" src="https://img.shields.io/badge/network-loopback%20only-success">
  <img alt="Version" src="https://img.shields.io/badge/build-3.4.1--hardened-blue">
  <img alt="License" src="https://img.shields.io/badge/license-MIT-green">
</p>

> ### ⚠️ Read before you run this
>
> * **This is not affiliated with Google.** It reverse-engineers Google's internal `batchexecute` / JSPB endpoints. Using it almost certainly **violates the Google Terms of Service** and can get an account suspended. The most dangerous thing about this project is not the code — it is the account you attach to it.
> * **Never use your primary Google account.** A synced cookie (`__Secure-1PSID`, `SAPISID`, `OSID`) is a login token that bypasses your password *and* 2FA — it is full access to Gmail, Drive and everything else.
> * **Cookies are stored in plaintext** on disk (`cookies/*.json`, mode `0600`). File permissions are the only protection.
> * Run it on your own machine, on a throwaway account. Do not put it behind a tunnel or reverse proxy. See [SECURITY.md](SECURITY.md) for the full threat model.

---

## Table of contents

- [What this is](#what-this-is)
- [What this fork changes](#what-this-fork-changes)
- [Requirements](#requirements)
- [Quick start](#quick-start)
- [Attaching a Google account (optional)](#attaching-a-google-account-optional)
- [Docker](#docker)
- [Configuration](#configuration)
- [Models](#models)
- [API reference](#api-reference)
- [Client setup](#client-setup)
- [Troubleshooting](#troubleshooting)
- [Publishing your own fork](#publishing-your-own-fork)
- [Repository layout](#repository-layout)
- [Credits & license](#credits--license)

---

## What this is

```
┌────────────────┐   ws://127.0.0.1:9226    ┌──────────────────────────┐
│ Chrome extension│ ───────────────────────► │  Go server (Fiber v3)    │
│ (cookie bridge) │   session cookies        │  worker pool / queue      │
└────────────────┘                          │  arranger (tool calls)    │
                                            └────────────┬─────────────┘
                                                         │ HTTP/2 + JSPB
┌────────────────┐   http://127.0.0.1:8001               ▼
│ OpenAI client  │ ───────────────────────►  batchexecute (google.com)
│ OpenCode, etc. │   /v1/chat/completions
└────────────────┘
```

* **`free-gemini-api/`** — the server (Go, Fiber v3, SQLite, pure-Go tokenizer). Exposes `/v1/chat/completions`, `/v1/models`, streaming SSE, tool calling, media generation, an MCP stdio mode and a small analytics dashboard.
* **`gemini-extension/`** — a Manifest V3 Chrome extension that copies the Gemini session cookies from your logged-in browser to the server over a loopback WebSocket.
* **Guest mode** — works with no cookies at all (free-tier models), so you can evaluate it before attaching an account.

---

## What this fork changes

### 1. Removed data-exfiltration paths

| # | What upstream did | What this build does |
|---|---|---|
| 1.1 | `background.js` ran a **LAN discovery engine**: it probed 4 hostnames, **14 hard-coded LAN IPs** belonging to the original author, and swept `192.168.1.1–254`, then pushed your Google cookies to whichever host answered `{"status":"online"}` over **plaintext** `ws://` / `http://`. On hotel/campus/office Wi-Fi, anyone running a fake server would receive your Google session. | Hard-locked to `127.0.0.1` (`const LOOPBACK_HOST`). No scan, no hard-coded IPs, no host picked from the network. |
| 1.2 | A **"Copy Cookies"** button plus a `GET_FORMATTED_COOKIES` message handler dumped every session cookie as JSON into the clipboard. Pure credential-export primitive, unrelated to normal operation. | Button, handler and the `clipboardWrite` permission are gone. |
| 1.3 | A 13.7 MB binary blob `tuned.cact` shipped in the repo, referenced by **zero lines of code**. | Deleted and added to `.gitignore`. The repo is ~130 KB. |
| 1.4 | `manifest.json` requested `"http://*/*"` and `"https://*/*"` — read cookies from *any* site, talk to *any* host. | `host_permissions` limited to Google domains plus `http://127.0.0.1/*` and `http://localhost/*`. |

### 2. Closed server-side exposure

| File | Upstream | This build |
|---|---|---|
| `main.go` | `app.Listen(":8001")` — every interface | `127.0.0.1:8001` (override with `BIND_ADDR`) |
| `main.go` | `cors.New()` — wildcard origin: any website could drive the API from your browser | allowlist: `127.0.0.1` / `localhost` on ports 8001 and 3000 |
| `gemini/monitor.go` | WebSocket bridge on `0.0.0.0:9226`; `CheckOrigin: return true` — **any** web page could open `ws://127.0.0.1:9226` (WebSockets bypass CORS) and talk to the cookie bridge | `127.0.0.1:9226`, `CheckOrigin` accepts only a missing `Origin` (CLI/native) or `chrome-extension://` / `moz-extension://` |
| `gemini/monitor.go`, `client.go`, `registry.go` | cookie jars `0644`, directories `0755` | `0600` / `0700` |
| `docker-compose.yml` | published `"8001:8001"`, `"9226:9226"` to the whole LAN | `"127.0.0.1:8001:8001"`, `"127.0.0.1:9226:9226"`; `BIND_ADDR=0.0.0.0` only *inside* the container |

### 3. Bug fixes

| File | Upstream bug | Fix |
|---|---|---|
| `api/chat_handler.go` | `stream: true` returned **0 SSE events** and logged `failed to acquire worker for streaming: context canceled`: the request context was cancelled by a handler-level `defer cancel()` before Fiber ran the stream writer. | Ownership hand-off (`streamOwnsCancel`) — the stream callback cancels the context itself. Verified against upstream `HEAD~1` to confirm the bug was pre-existing. |
| `api/chat_handler.go` | Streaming a reply could **panic** (nil pointer) because the stream goroutine called `c.IP()` / `c.Host()` on a recycled Fiber context. | `reqHost` / `reqIP` are captured before the handler returns. |
| `api/routes.go` | `/api/sync-cookies` ignored `profileId`, so every `__Secure-1PSID` rotation minted a new `account_<hash>.json` and the worker pool grew duplicate workers for the same account. | Payload binding keeps `profileId`; one file per profile. |
| `gemini/arranger.go` | Tool calls arrived as `"arguments": "{}"`, so clients rejected them (`Invalid arguments for tool "shell": - command: Missing key`). Malformed JSON from the model (nested quotes, trailing commas, smart quotes, OpenAI-style `{"function":{...}}` wrapper, flat args) was silently dropped. | Tolerant decode/repair chain (`decodeJSONObject`, `repairStrayQuotes`, `normalizeToolPayload`, `repairArgsJSON`), `backfillEmptyToolArgs()` (recovers the command from a fenced code block), and extra prompt rules that forbid empty argument objects. Covered by `gemini/arranger_tools_test.go`. |
| `api/version.go` | no build identity | `GET /health` now returns `"version": "3.4.1-hardened"`. |

### 4. Other clean-up

* Dead code removed (`probeLoopback`), loopback-only startup banner, and `go.mod`/`go.sum` tidied.
* `/health` no longer leaks paths; docs rewritten in English.

---

## Requirements

* **Go 1.24+** — `go.mod` declares `go 1.26.0`, so `GOTOOLCHAIN=auto` (the default) will download the right toolchain on first build.
* **or** Docker + Docker Compose.
* **Chrome / Chromium** — only if you want to attach a Google account. Guest mode needs no browser.
* Linux, macOS or Windows. Roughly 40 MB of RAM for the server.

---

## Quick start

```bash
cd free-gemini-api
go run .
```

Expected output:

```
🧩 Hardened build 3.4.1-hardened — loopback defaults, origin allowlist, tool-call parser fixes
🚀 Free Gemini API Server starting on 127.0.0.1:8001 (loopback-only unless BIND_ADDR is set)...
📡 Cookie WebSocket Server listening on 127.0.0.1:9226
```

Verify:

```bash
curl http://127.0.0.1:8001/health
curl http://127.0.0.1:8001/v1/models | head
```

First request (guest mode — no account needed, free-tier models only):

```bash
curl -s http://127.0.0.1:8001/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"gemini-3.8-flash","messages":[{"role":"user","content":"Say OK"}]}'
```

> Responses can take 3–160 s depending on the model and how throttled the account is. `gemini-4-argon-thinking-high` has been measured at ~2.6 minutes. Set client timeouts accordingly (see [Troubleshooting](#troubleshooting)).

---

## Attaching a Google account (optional)

Guest mode works out of the box but is rate-limited and cannot reach subscription models. To use your own session:

1. `chrome://extensions` → enable **Developer mode** → **Load unpacked** → select the `gemini-extension` folder.
2. Log in to <https://gemini.google.com> in that browser.
3. The extension pushes cookies to `ws://127.0.0.1:9226` automatically (falls back to `http://127.0.0.1:8001/api/sync-cookies`).
4. Confirm:

```bash
curl -s http://127.0.0.1:8001/health   # cookie_accounts_in_pool > 0
curl -s http://127.0.0.1:8001/v1/workers
```

Multiple accounts are supported: each gets its own `cookies/account_<profile>.json` and its own worker in the pool; requests are routed to the least-busy worker.

**Use a throwaway Google account. This is not a hypothetical risk** — see [SECURITY.md](SECURITY.md).

---

## Docker

```bash
cd free-gemini-api
docker compose up -d --build
curl http://127.0.0.1:8001/health
```

Ports are published on `127.0.0.1` only. Inside the container the processes must bind `0.0.0.0` (that is what `BIND_ADDR` / `WS_BIND_ADDR` are for), but nothing outside your machine can reach them.

---

## Configuration

All configuration is environment variables or an optional `.env` file in `free-gemini-api/`:

| Variable | Default | Meaning |
|---|---|---|
| `PORT` | `8001` | HTTP API port |
| `BIND_ADDR` | `127.0.0.1` | API bind address. Set `0.0.0.0` **only** inside Docker. |
| `WS_PORT` | `9226` | Cookie bridge WebSocket port |
| `WS_BIND_ADDR` | `127.0.0.1` | Cookie bridge bind address |
| `GEMINI_API_URL` | Google's endpoint | Override the upstream base URL (mirror/proxy debugging) |
| `GEMINI_ACCOUNT_PREFIX` | `account` | Cookie file prefix, e.g. `cookies/account_1.json` |
| `GEMINI_ACCOUNT_INDEX` | — | Cookie file index for `chrome-manage` profiles |
| `USD_TO_INR` | `83` | Only affects the cosmetic "cost saved" figure |

---

## Models

`GET /v1/models` returns 14 entries. Aliases resolve to the same model, so `"model":"argon-high"` and `"model":"gemini-4-argon-thinking-high"` are equivalent.

| Model ID | Tier | Thinking level | Common aliases |
|---|---|---|---|
| `gemini-3.8-flash` *(default)* | free | — | `flash`, `gemini-flash`, `default`, `3.8-flash` |
| `gemini-3.8-flash-thinking-low` | free | low | `flash-thinking-low`, `flash-low` |
| `gemini-3.8-flash-thinking-medium` | free | medium | `flash-thinking-medium`, `flash-medium` |
| `gemini-3.8-flash-thinking-high` | free | high | `flash-thinking-high`, `flash-high` |
| `gemini-3.5-flash-lite` | free | — | `flash-lite`, `lite` |
| `gemini-4-argon` | free | — | `argon`, `gemini-4`, `4` |
| `gemini-4-argon-thinking-low` | free | low | `argon-thinking-low`, `argon-low` |
| `gemini-4-argon-thinking-medium` | free | medium | `argon-thinking-medium`, `argon-medium` |
| `gemini-4-argon-thinking-high` | free | high | `argon-thinking-high`, `argon-high` |
| `gemini-3.8-thinking` | subscription | low | `gemini-thinking`, `thinking` |
| `gemini-3.1-pro` | subscription | — | `pro`, `gemini-pro` *(actually needs an attached account)* |
| `gemini-3.1-pro-thinking-low` | subscription | low | `pro-thinking-low`, `pro-low` |
| `gemini-3.1-pro-thinking-medium` | subscription | medium | `pro-thinking-medium`, `pro-medium` |
| `gemini-3.1-pro-thinking-high` | subscription | high | `pro-thinking-high`, `pro-high` |

"Tier" is enforced by the worker pool: `free` models run on any worker (including the guest worker), `subscription` models require an attached account that Google reports as eligible.

---

## API reference

### Endpoints

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/v1/chat/completions` | OpenAI-compatible chat (stream and non-stream, tools, vision) |
| `GET` | `/v1/models` | Model catalog (OpenAI shape) |
| `POST` | `/v1/models` · `/v1/models/discover` | Catalog refresh |
| `GET` | `/v1/workers`, `/api/pool` | Worker pool / queue metrics |
| `GET` | `/health` | Status, build version, cookie health |
| `POST` | `/api/sync-cookies` | HTTP fallback used by the extension |
| `POST` | `/chat` | Native endpoint (images, video, music, model selection) |
| `POST` | `/music` | Lyria music generation |
| `GET` | `/media`, `/output/*` | Generated media |
| `GET` | `/history`, `/history/search`, `/stats`, `/status` | Local SQLite history and analytics |
| `GET` | `/export/excel`, `/export/json`, `/v1/analytics` | Analytics export |
| `GET` | `/help` | CLI help text |

Multi-agent headers: `X-Agent-ID`, `X-Session-ID`, `X-Conversation-ID` pin a request to a session for cache/affinity.

### Chat completions

```bash
curl -s http://127.0.0.1:8001/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer any-key" \
  -d '{
    "model": "argon-high",
    "messages": [{"role":"user","content":"A farmer has 17 sheep. All but 9 die. How many are left alive?"}]
  }'
```

The bearer token is **ignored** — the server has no authentication. That is exactly why it binds to loopback only.

### Streaming

```bash
curl -N http://127.0.0.1:8001/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"gemini-3.8-flash","messages":[{"role":"user","content":"Count to 5"}],"stream":true}'
```

Returns standard `data: {...}` chunks terminated by `data: [DONE]`.

### Tool calling

Send OpenAI-shaped `tools`; the server injects a tool-call instruction into the prompt and parses the model's `<tool_call>` block back into `tool_calls`:

```bash
curl -s http://127.0.0.1:8001/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-3.8-flash",
    "messages": [{"role":"user","content":"List files in the current directory"}],
    "tools": [{"type":"function","function":{
      "name":"shell","description":"Run a shell command",
      "parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}}]
  }'
```

`finish_reason` will be `tool_calls` and `arguments` is guaranteed to be parseable JSON — if the model returns a command in a fenced code block instead of arguments, it is recovered automatically.

---

## Client setup

### OpenCode v2

Save as `~/.config/opencode/opencode.jsonc` (or `<project>/opencode.jsonc`):

```jsonc
{
  "$schema": "https://opencode.ai/config.json",

  "providers": {
    "gemini-local": {
      "name": "Gemini Local (free-gemini-api)",
      "package": "@opencode/ai/providers/openai-compatible",
      "settings": {
        "baseURL": "http://127.0.0.1:8001/v1"
        // no apiKey needed; the server does not check it.
        // default timeouts: headerTimeout 5m / chunkTimeout 15m — enough for argon-thinking-high.
        // raise them if you use slower models: "headerTimeout": 600000, "chunkTimeout": 600000
      },

      "models": {
        "flash":                 { "name": "Gemini 3.8 Flash (fastest)", "modelID": "gemini-3.8-flash",
                                   "capabilities": { "tools": true, "input": ["text"], "output": ["text"] },
                                   "limit": { "context": 200000, "output": 16384 } },
        "flash-thinking-high":   { "name": "Gemini 3.8 Flash — thinking high", "modelID": "gemini-3.8-flash-thinking-high",
                                   "capabilities": { "tools": true, "input": ["text"], "output": ["text"] },
                                   "limit": { "context": 200000, "output": 32768 } },
        "flash-thinking-medium": { "name": "Gemini 3.8 Flash — thinking medium", "modelID": "gemini-3.8-flash-thinking-medium",
                                   "capabilities": { "tools": true, "input": ["text"], "output": ["text"] },
                                   "limit": { "context": 200000, "output": 32768 } },
        "flash-lite":            { "name": "Gemini 3.5 Flash Lite", "modelID": "gemini-3.5-flash-lite",
                                   "capabilities": { "tools": true, "input": ["text"], "output": ["text"] },
                                   "limit": { "context": 100000, "output": 8192 } },
        "argon":                 { "name": "Gemini 4 Argon", "modelID": "gemini-4-argon",
                                   "capabilities": { "tools": true, "input": ["text"], "output": ["text"] },
                                   "limit": { "context": 200000, "output": 32768 } },
        "argon-high":            { "name": "Gemini 4 Argon — thinking high (slow)", "modelID": "gemini-4-argon-thinking-high",
                                   "capabilities": { "tools": true, "input": ["text"], "output": ["text"] },
                                   "limit": { "context": 200000, "output": 32768 } },
        "argon-medium":          { "name": "Gemini 4 Argon — thinking medium", "modelID": "gemini-4-argon-thinking-medium",
                                   "capabilities": { "tools": true, "input": ["text"], "output": ["text"] },
                                   "limit": { "context": 200000, "output": 32768 } },
        "pro":                   { "name": "Gemini 3.1 Pro (needs attached account)", "modelID": "gemini-3.1-pro",
                                   "capabilities": { "tools": true, "input": ["text"], "output": ["text"] },
                                   "limit": { "context": 200000, "output": 32768 } }
      }
    }
  },

  "model": "gemini-local/flash"
}
```

Notes for v2:

* `providers` is plural, `package` is `@opencode/ai/providers/openai-compatible` (not `@ai-sdk/*`), and `baseURL` lives under `settings` — v1 configs will not work here.
* Model references are `provider/model` — `gemini-local/flash` above refers to the **alias** key, not the Google model ID; `modelID` maps it to the real one.
* Config is JSONC — comments and trailing commas are allowed.

### OpenCode v1

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "gemini-local": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "Gemini Local",
      "options": { "baseURL": "http://127.0.0.1:8001/v1" },
      "models": { "flash": { "name": "Gemini 3.8 Flash" } }
    }
  },
  "model": "gemini-local/flash"
}
```

### Any OpenAI SDK

```python
from openai import OpenAI

client = OpenAI(base_url="http://127.0.0.1:8001/v1", api_key="not-needed", timeout=300)
r = client.chat.completions.create(
    model="gemini-3.8-flash",
    messages=[{"role": "user", "content": "Hello"}],
)
print(r.choices[0].message.content)
```

```bash
export OPENAI_BASE_URL=http://127.0.0.1:8001/v1
export OPENAI_API_KEY=any-key
```

Works with Cursor, Cline, Continue, OpenWebUI, LangChain, NextChat and anything else that speaks the OpenAI wire format. Set a generous timeout (≥300 s).

---

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `Invalid arguments for tool "shell": - command: Missing key` | The model emitted an empty arguments object. Fixed in `gemini/arranger.go` (tolerant parser + fence backfill). If you still see it, retry once, or use `gemini-3.8-flash-thinking-medium`; wrap odd filenames in backticks so the model copies them verbatim. |
| Stream returns no events (`failed to acquire worker for streaming: context canceled`) | You are running an older build. This fork fixes it (`streamOwnsCancel`). |
| Request hangs, client times out | Slow model or a throttled/guest worker. Increase client timeout; OpenCode v2 defaults (`headerTimeout` 5 min, `chunkTimeout` 15 min) are enough. |
| `pool.ExecuteQueued: no worker available for tier ...` | Subscription model requested without a matching account. Use a free model or attach an eligible account. |
| `/health` shows `cookie_accounts_in_pool: 0` | Extension not loaded, or not logged in to `gemini.google.com`. Reload the extension and check `chrome://extensions` → service worker console. |
| `WS Upgrade failed: request origin not allowed` | A web page (not the extension, not a CLI client) tried to open the cookie bridge. This is the hardening working as intended. |
| Port already in use | `PORT=8002 go run .` (and update clients). |

---

## Publishing your own fork

The tree is ready to push as-is; there is no `.git` directory inside the archive.

```bash
# 1. create an empty repo on GitHub (no README, no license) — e.g. my-gemini-proxy
cd free-gemini-api-hardened
git init -b main
git add -A
git commit -m "Hardened local-only build: loopback binds, origin allowlist, streaming + tool-call fixes"

# 2. push
git remote add origin git@github.com:<you>/<repo>.git
git push -u origin main
```

What you get out of the box: `.gitignore` (cookies, SQLite, generated media, binaries), `SECURITY.md`, `CHANGELOG.md`, and `.github/workflows/ci.yml` (build + vet + tests on every push). `.github/workflows/docker-publish.yml` pushes an image to GHCR on `main` — disable it if you do not want that.

Before publishing, decide two things:

1. **License** — see below.
2. Whether to keep the upstream name/attribution in a way that makes clear this is a fork, not the original project. Recommended: keep the "fork of" line in the README and a `NOTICE`/credits section, because you did not write the original code.

---

## Repository layout

```
.
├── README.md
├── SECURITY.md                  threat model + full hardening notes
├── CHANGELOG.md
├── LICENSE                      MIT License
├── .github/workflows/
│   ├── ci.yml                   build / vet / tests
│   └── docker-publish.yml       GHCR image on main
├── free-gemini-api/             Go server (module `goapi`)
│   ├── main.go
│   ├── api/                     HTTP handlers, OpenAI adapter, MCP, queue
│   ├── gemini/                  upstream client, arranger, cookie monitor, model registry
│   ├── db/                      SQLite history + analytics
│   ├── Dockerfile, docker-compose.yml
│   └── schema.sql
└── gemini-extension/            Chrome MV3 cookie bridge (loopback only)
```

---

## Credits & license

* Original project: [`kodelyx/free-gemini-api`](https://github.com/kodelyx/free-gemini-api) — architecture, JSPB client, arranger engine, model registry.
* This fork: security hardening, loopback defaults, streaming and tool-call fixes, English documentation.

**License:**

This project is licensed under the terms of the **MIT License**. See the [LICENSE](LICENSE) file for details.

```text
MIT License

Copyright (c) 2026

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

Nothing in this repository grants any rights to Google's services, and using it may breach Google's Terms of Service. Use at your own risk.
