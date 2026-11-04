# Security notes

This document describes what was hardened in this fork, how it was verified, and — importantly — what is still dangerous and cannot be fixed with code.

Baseline: [`kodelyx/free-gemini-api`](https://github.com/kodelyx/free-gemini-api) (v3.0.0 / Fiber v3.4.0). Build: `3.4.1-hardened`.

---

## 1. Threat model in one paragraph

The server holds **Google session cookies** (equivalent to your password *and* 2FA) and, by default, has **no authentication** of its own. Therefore the only thing standing between your Google account and the rest of the world is the network surface of the two ports it opens. This fork's entire security goal is to make that surface reachable from exactly one place: `127.0.0.1` on your own machine, from your own browser extension.

Attackers considered: websites you visit (CSRF / WebSocket hijack, since WebSockets bypass CORS), other devices on your LAN, anyone who can reach your machine over the network, and malicious/fake "API servers" on shared networks.

---

## 2. Data-exfiltration paths removed

### 2.1 LAN discovery engine (`gemini-extension/background.js`)

Upstream shipped a discovery loop that probed:

* `127.0.0.1`, `localhost`, `macbookpro.local`, `host.docker.internal`
* **14 hard-coded LAN IPs** belonging to the original author (`192.168.1.34`, `10.56.65.136`, `10.56.65.169`, …)
* a full **`192.168.1.1` → `192.168.1.254` sweep**

…and then sent your Google cookies to whichever host answered `GET /health` with `{"status":"online"}`, over **unencrypted** `ws://` or `http://`.

Consequence: on hotel, campus, coworking or office Wi-Fi, anyone running a fake server would receive a full Google session in plaintext.

**Now:** `const LOOPBACK_HOST = '127.0.0.1'`, and a guard that rejects any other host (`Only 127.0.0.1 / localhost are allowed`). No scan, no hard-coded IPs, no host chosen from the network.

### 2.2 Clipboard credential export (`popup.html`, `popup.js`, `background.js`)

A "Copy Cookies" button serialised the entire cookie jar to JSON and placed it on the clipboard, with a matching `GET_FORMATTED_COOKIES` message handler. It had nothing to do with normal operation — it was a credential-export primitive (clipboard content is readable by other apps and extensions).

**Now:** button, handler and the `clipboardWrite` permission are removed.

### 2.3 Unused 13.7 MB binary (`tuned.cact`)

Shipped in the repository, referenced by **zero** lines of code (`grep` finds nothing). Unexplained opaque binary.

**Now:** deleted, added to `.gitignore`.

### 2.4 Over-broad extension permissions (`manifest.json`)

```jsonc
// before
"permissions":      ["cookies", "storage", "alarms", "clipboardWrite"],
"host_permissions": ["*://*.google.com/*", "*://*.google.co/*",
                     "*://*.googleusercontent.com/*",
                     "http://*/*", "https://*/*"]        // read cookies from ANY site, talk to ANY host

// after
"permissions":      ["cookies", "storage", "alarms"],
"host_permissions": ["*://*.google.com/*", "*://*.google.co/*",
                     "*://*.googleusercontent.com/*",
                     "http://127.0.0.1/*", "http://localhost/*"]
```

---

## 3. Server-side exposure closed

| File | Upstream | This build |
|---|---|---|
| `main.go` | `app.Listen(":8001")` — all interfaces | `127.0.0.1:8001`, override with `BIND_ADDR` |
| `main.go` | `cors.New()` — wildcard origin | allowlist of `127.0.0.1` / `localhost` on `:8001` and `:3000` |
| `gemini/monitor.go` | WebSocket on `0.0.0.0:9226` | `127.0.0.1:9226`, override with `WS_BIND_ADDR` |
| `gemini/monitor.go` | `CheckOrigin: func(...) bool { return true }` — **any** website could open `ws://127.0.0.1:9226` and talk to the cookie bridge | accepts a missing `Origin` (CLI/native) or `chrome-extension://` / `moz-extension://`; everything else gets `403` |
| `gemini/monitor.go` | cookie jars `0644`, dir `0755` | `0600`, dir `0700` |
| `gemini/client.go` | session-token cache `0644`, dir `0755` | `0600` / `0700` |
| `gemini/registry.go` | `0644` | `0600` |
| `docker-compose.yml` | `"8001:8001"`, `"9226:9226"` published to the LAN | `"127.0.0.1:8001:8001"`, `"127.0.0.1:9226:9226"`; `BIND_ADDR=0.0.0.0` only inside the container |

The WebSocket origin check matters most: browsers deliberately exempt WebSockets from CORS, so before this change *any* page you visited could open the cookie bridge and inject or read session material.

---

## 4. Verification (run on the delivered build)

```
$ ss -ltnp | grep goapi
LISTEN 127.0.0.1:8001
LISTEN 127.0.0.1:9226

$ # WebSocket origin policy
no Origin header                        -> HTTP/1.1 101 Switching Protocols   (CLI/native client)
chrome-extension://abcdefghijklmnop     -> HTTP/1.1 101 Switching Protocols   (the extension still works)
https://evil.example                    -> HTTP/1.1 403 Forbidden             (blocked)

$ # CORS preflight from a hostile origin
OPTIONS /v1/chat/completions (Origin: https://evil.example)
   -> no Access-Control-Allow-Origin header

$ stat
700 cookies/            600 cookies/account_*.json

$ # functional paths still work
POST /api/sync-cookies     -> 200
POST /v1/chat/completions  -> 200 (non-stream and stream)
GET  /health               -> 200 {"status":"online","version":"3.4.1-hardened",...}
```

Note: when running inside a sandbox that proxies loopback, an extra listener may appear on a link-local address (`169.254.x.x`). That is the sandbox's own proxy, not the server binding `0.0.0.0` — on a normal machine you will see only `127.0.0.1`.

Automated checks in this repository:

```bash
cd free-gemini-api
go build ./... && go vet ./... && go test ./...
```

---

## 5. Bug fixes with security relevance

### 5.1 Streaming returned nothing, or crashed

`api/chat_handler.go` created the request context with a handler-level `defer cancel()`, but Fiber v3 runs the stream writer *after* the handler returns — the context was already dead, so every `stream: true` request failed with `failed to acquire worker for streaming: context canceled`. On top of that, the stream goroutine called `c.IP()` / `c.Host()` on a recycled Fiber context, which could panic the process.

Fix: the stream callback takes ownership of cancellation (`streamOwnsCancel`), and `reqHost` / `reqIP` are captured before the handler returns. Confirmed against upstream `HEAD~1` that both bugs were pre-existing.

### 5.2 Tool calls arrived with no arguments

See [section 8 of the changelog reasoning in the README](README.md#3-bug-fixes) and `gemini/arranger_tools_test.go`. Malformed JSON from the model used to be dropped silently; it is now repaired, and an empty argument object is backfilled from a fenced code block when possible.

### 5.3 Duplicate workers per account

`/api/sync-cookies` dropped the `profileId` field, so each `__Secure-1PSID` rotation created a new cookie file and the pool grew duplicate workers for the same Google account. Fixed by binding `profileId` into the payload struct.

---

## 6. Residual risk — not fixable in code

1. **Cookies are stored in plaintext** in `cookies/*.json`. Permissions are `0600`, but any process running as your user (including malware) can read them. There is no encryption at rest; adding one would need a passphrase prompt or an OS keychain, which upstream has no concept of.
2. **Never attach a primary Google account.** `__Secure-1PSID`, `SAPISID` and `OSID` grant immediate access to Gmail, Drive, Photos and more — password and 2FA are irrelevant once a cookie is captured. Use a throwaway account, and sign that browser profile out of everything else.
3. **Terms of Service.** This project reverse-engineers Google's internal `batchexecute` endpoints to obtain paid-tier behaviour for free, and supports multi-account pooling. Account suspension or permanent ban is a realistic outcome. This is the largest risk in the project and **no code change removes it**.
4. **No authentication on the API.** Anyone who can reach `127.0.0.1:8001` can spend the quota of the account behind it. Do not run it on a shared machine, do not bind to `0.0.0.0`, and never put a tunnel, reverse proxy or ngrok in front of it.
5. **The extension still needs `cookies` permission** for Google domains. That is inherent to the design: a cookie bridge must read cookies.
6. **Logs and analytics**: request history is written to a local SQLite DB and exported to `output/`. Prompts and responses are stored in plaintext. Both paths are git-ignored.

---

## 7. Reporting

This is a fork, not a maintained product. If you find a problem, open an issue in your own repository or patch it locally. Do not report issues in this fork to Google — nothing here is supported by Google, and the account risk is yours.
