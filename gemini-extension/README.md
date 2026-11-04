# 🧩 Gemini Api Agent (Chrome Extension)

A lightweight, automated Chrome Extension (Manifest V3) that syncs your active Google Gemini session cookies to the local **Free Gemini API server** (`free-gemini-api/` in this repository) over a **loopback-only** WebSocket.

> This build is hard-locked to `127.0.0.1`. Unlike the upstream extension it does **not** discover servers on the LAN, does not probe hard-coded IPs or sweep your subnet, and cannot export cookies to the clipboard. See [`../SECURITY.md`](../SECURITY.md).
>
> ⚠️ A synced cookie is equivalent to your Google password + 2FA. Use a throwaway Google account.

---

## 🚀 Quick Setup & Installation

1. Open Google Chrome and navigate to:
   ```text
   chrome://extensions/
   ```
2. Enable **Developer mode** (toggle switch in the top right corner).
3. Click **Load unpacked** and select the `gemini-extension` folder from this repository.
4. Start the server first (`cd free-gemini-api && go run .`).
5. Log in to [gemini.google.com](https://gemini.google.com) — the extension pushes session cookies to `ws://127.0.0.1:9226` (HTTP fallback: `http://127.0.0.1:8001/api/sync-cookies`).
6. Verify with `curl -s http://127.0.0.1:8001/health` → `cookie_accounts_in_pool` should be greater than 0.

---

## ⚡ Features

- **Multi-Account Support:** Automatically detects and manages multiple logged-in Google accounts.
- **WebSocket Real-Time Sync:** Instant zero-delay cookie sync on login, rotation, or session refresh.
- **Background Keepalive:** Runs seamlessly in the background without slowing down your browser.
- **Zero Configuration:** Works right out of the box with the Free Gemini API Server.

---

## 🔗 Related Repositories

- 🚀 **Free Gemini API server** — `free-gemini-api/` in this repository ([`../README.md`](../README.md))
- 🔒 **Security notes and threat model** — [`../SECURITY.md`](../SECURITY.md)
- Original upstream project: [`kodelyx/free-gemini-api`](https://github.com/kodelyx/free-gemini-api)
