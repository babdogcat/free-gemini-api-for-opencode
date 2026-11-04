# Changelog

All notable changes to this hardened fork. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning is independent of upstream.

## [3.4.1-hardened] — 2026-11-04

### Added
- `GET /health` reports `version`, so builds can be told apart.
- `gemini/arranger_tools_test.go` — 11 cases covering the tool-call parser and empty-argument backfill.
- `.github/workflows/ci.yml` — build, vet and test on every push and pull request.
- English `README.md`, `SECURITY.md` and this changelog.

### Fixed
- **Tool calls with empty arguments** (`"arguments": "{}"`), which made clients reject calls with `Invalid arguments for tool "shell": - command: Missing key`:
  - `parseSingleToolCall()` now repairs malformed JSON instead of discarding the call — smart quotes, trailing commas, unescaped inner quotes, double-encoded payloads, OpenAI-style `{"function":{...}}` wrappers and flat argument objects are all accepted.
  - `backfillEmptyToolArgs()` recovers a command from a fenced code block when the model returns no arguments at all.
  - Prompt rules now explicitly forbid empty argument objects and carry a worked example.
- `/api/sync-cookies` ignored `profileId`, which created a new cookie file (and a duplicate pool worker) on every `__Secure-1PSID` rotation.

### Changed
- Build identity `3.4.1-hardened` is logged at startup.
- Documentation rewritten in English; the Thai hardening notes were replaced by `SECURITY.md`.

## [3.4.0-hardened] — 2026-11-04

### Security
- Removed the extension's **LAN discovery engine**: 4 hostnames, 14 hard-coded LAN IPs and a `192.168.1.1–254` sweep that pushed Google cookies to whichever host answered `/health` over plaintext `ws://`/`http://`. The bridge is now hard-locked to `127.0.0.1`.
- Removed the **"Copy Cookies"** button, the `GET_FORMATTED_COOKIES` handler and the `clipboardWrite` permission.
- Deleted the unused 13.7 MB `tuned.cact` binary blob.
- Tightened `manifest.json`: `host_permissions` limited to Google domains + `127.0.0.1` / `localhost` (was `http://*/*` and `https://*/*`).
- WebSocket bridge binds `127.0.0.1:9226` (was `0.0.0.0`) and validates `Origin` — only non-browser clients and extension origins are accepted (was `return true`).
- HTTP API binds `127.0.0.1:8001` (was all interfaces) and restricts CORS to loopback UI origins (was wildcard).
- Cookie jars, session cache and registry files are written `0600` in `0700` directories (were `0644` / `0755`).
- `docker-compose.yml` publishes both ports on `127.0.0.1`; containers still bind `0.0.0.0` internally via `BIND_ADDR` / `WS_BIND_ADDR`.

### Fixed
- **Streaming produced no output**: a handler-level `defer cancel()` killed the request context before Fiber ran the stream writer (`failed to acquire worker for streaming: context canceled`). Cancellation is now owned by the stream callback (`streamOwnsCancel`).
- **Streaming could panic**: the stream goroutine touched a recycled Fiber context (`c.IP()`, `c.Host()`); both are captured before the handler returns.

### Removed
- Dead `probeLoopback` helper.

---

## Upstream baseline

Forked from [`kodelyx/free-gemini-api`](https://github.com/kodelyx/free-gemini-api) at commit `1b4492d` ("docs: enhance community section in README"), which sits on top of `25d20e3` (Gemini 4 Argon support).
