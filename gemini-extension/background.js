/**
 * Free Gemini API Sync — Loopback-Only Background Service Worker
 * Pure memory-based zero-tab cookie synchronizer.
 * NEVER opens tabs, NEVER reloads tabs, NEVER touches window focus.
 *
 * HARDENED (local-only build):
 *   - The backend host is hard-locked to 127.0.0.1.
 *   - The autonomous LAN discovery engine (hardcoded 192.168.1.x / 10.56.65.x
 *     lists + 192.168.1.1-254 subnet sweep) has been REMOVED. That code would
 *     push Google session cookies to any device on the network that answered
 *     GET /health with {"status":"online"}.
 *   - The raw-cookie clipboard export primitive has been REMOVED.
 *   - Nothing is ever sent to a host other than loopback.
 */

// ─── Loopback-only backend (no discovery, no LAN scanning) ──────────────────
const LOOPBACK_HOST = '127.0.0.1';
const WS_PORT = 9226;
const HTTP_PORT = 8001;

let activeHost = LOOPBACK_HOST;
let ws = null;
let lastSyncTime = null;
let hasSyncedOnce = false;
let syncDebounceTimeout = null;

chrome.runtime.onInstalled.addListener(init);
chrome.runtime.onStartup.addListener(init);

chrome.alarms.onAlarm.addListener(async (alarm) => {
  if (alarm.name === 'reconnect') connectToBackend();
  if (alarm.name === 'keepAlive') keepAlive();
});

function isLoopback(host) {
  if (typeof host !== 'string') return false;
  const h = host.trim().toLowerCase();
  return h === '127.0.0.1' || h === 'localhost' || h === '::1' || h === '[::1]';
}

async function getServerHost() {
  return activeHost;
}

async function init() {
  const data = await chrome.storage.local.get(['lastSyncTime']);
  if (data.lastSyncTime) lastSyncTime = data.lastSyncTime;

  activeHost = LOOPBACK_HOST;
  connectToBackend();
  // Lightweight keep-alive ping every 25 seconds to preserve WebSocket channel
  chrome.alarms.create('keepAlive', { periodInMinutes: 0.4 });
}

async function connectToBackend() {
  if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
    return;
  }

  const host = LOOPBACK_HOST;
  const wsUrl = `ws://${host}:${WS_PORT}`;
  console.log(`[Gemini Sync] Connecting to local backend at: ${wsUrl}`);
  hasSyncedOnce = false;

  try {
    ws = new WebSocket(wsUrl);
  } catch (e) {
    scheduleReconnect();
    return;
  }

  ws.onopen = () => {
    console.log(`[Gemini Sync] Successfully connected to Go Backend on ${host} (Zero-Tab Mode)`);
    chrome.alarms.clear('reconnect');
    // Read and push cookies instantly from memory on connect
    performSync();
  };

  ws.onmessage = (event) => {
    try {
      const msg = JSON.parse(event.data);
      if (msg.type === 'trigger_sync') {
        console.log('[Gemini Sync] Backend requested fresh cookies. Syncing directly from memory (0 tabs)...');
        performSync();
      }
    } catch (e) {
      console.error(e);
    }
  };

  ws.onclose = () => {
    console.log(`[Gemini Sync] Connection to ${host} closed. Retrying...`);
    scheduleReconnect();
  };

  ws.onerror = () => {
    console.warn(`[Gemini Sync] WebSocket probe failed for ${host}:${WS_PORT}`);
    try { ws.close(); } catch (_) {}
  };
}

function scheduleReconnect() {
  chrome.alarms.create('reconnect', { delayInMinutes: 0.05 }); // ~3s
}

function keepAlive() {
  if (ws?.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({ type: 'ping' }));
  } else {
    connectToBackend();
  }
}

const ALLOWED_COOKIE_NAMES = new Set([
  '__Secure-1PSID',
  '__Secure-3PSID',
  '__Secure-1PAPISID',
  '__Secure-3PAPISID',
  '__Secure-1PSIDTS',
  '__Secure-3PSIDTS',
  '__Secure-1PSIDCC',
  '__Secure-3PSIDCC',
  'SID',
  'HSID',
  'SSID',
  'APISID',
  'SAPISID',
  'SIDCC',
  'OSID',
  '__Secure-OSID',
  'COMPASS',
  'NID'
]);

// Extracts and formats essential Google-only cookies directly from Chrome memory
function getFormattedCookies(callback) {
  chrome.cookies.getAll({}, (cookies) => {
    const googleCookies = cookies.filter(c =>
      (c.domain === '.google.com' || c.domain === '.google.co' || c.domain === 'gemini.google.com' || c.domain.includes('googleusercontent.com')) &&
      (ALLOWED_COOKIE_NAMES.has(c.name) || c.domain.includes('googleusercontent.com'))
    );

    const formatted = googleCookies.map(c => {
      let exp = c.expirationDate;
      if (!exp || c.session) {
        exp = Math.floor(Date.now() / 1000) + 31536000; // Default 1 year
      }

      let sameSite = c.sameSite || 'unspecified';
      if (sameSite === 'no_restriction') sameSite = 'none';

      return {
        domain: c.domain,
        expirationDate: exp,
        hostOnly: c.hostOnly,
        httpOnly: c.httpOnly,
        name: c.name,
        path: c.path,
        sameSite: sameSite,
        secure: c.secure,
        session: c.session,
        storeId: c.storeId || '0',
        value: c.value
      };
    });

    callback(formatted);
  });
}

// Performs instantaneous memory extraction and loopback-only transfer (ZERO tabs)
async function performSync() {
  const host = LOOPBACK_HOST;
  getFormattedCookies((formatted) => {
    if (formatted.length === 0) {
      hasSyncedOnce = false;
      return;
    }

    // If WebSocket is ready, push via WebSocket
    if (ws && ws.readyState === WebSocket.OPEN) {
      console.log(`[Gemini Sync] Synchronized ${formatted.length} essential cookies via WebSocket to ${host}`);
      ws.send(JSON.stringify({
        type: 'cookies_payload',
        cookies: formatted
      }));
    } else {
      // Dual resilience: push via direct HTTP fallback to loopback server
      fetch(`http://${host}:${HTTP_PORT}/api/sync-cookies`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ cookies: formatted })
      }).then(() => {
        console.log(`[Gemini Sync] Synchronized ${formatted.length} cookies via HTTP Fallback to http://${host}:${HTTP_PORT}`);
      }).catch((e) => {
        console.warn(`[Gemini Sync] HTTP Fallback failed to ${host}:`, e);
      });

      connectToBackend();
    }

    hasSyncedOnce = true;
    lastSyncTime = Date.now();
    chrome.storage.local.set({ lastSyncTime });
    chrome.runtime.sendMessage({ type: 'SYNC_UPDATE', success: true, count: formatted.length }).catch(() => {});
  });
}

// ─── Real-Time Passive Cookie Listener ──────────────────────────────────────
// Auto-detect when Google rotates session cookies and push them instantly (zero CPU idle)
chrome.cookies.onChanged.addListener((changeInfo) => {
  const cookie = changeInfo.cookie;

  const isTargetDomain = cookie.domain === '.google.com' ||
                         cookie.domain === '.google.co' ||
                         cookie.domain === 'gemini.google.com' ||
                         cookie.domain.includes('googleusercontent.com');

  if (isTargetDomain && ALLOWED_COOKIE_NAMES.has(cookie.name)) {
    if (changeInfo.removed) return;

    // Debounce multiple fast updates (Google rotates 2-3 tokens in a single batch)
    if (syncDebounceTimeout) clearTimeout(syncDebounceTimeout);
    syncDebounceTimeout = setTimeout(() => {
      performSync();
    }, 1500);
  }
});

// Receive message from Popup
chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  if (msg.type === 'GET_STATUS') {
    getServerHost().then((host) => {
      sendResponse({
        connected: !!(ws && ws.readyState === WebSocket.OPEN),
        lastSyncTime,
        hasSyncedOnce,
        serverHost: host
      });
    });
    return true; // async sendResponse
  } else if (msg.type === 'SET_SERVER_HOST') {
    // Still supported, but loopback-only: a non-local host is rejected so this
    // can never be used to redirect cookies to another machine.
    const newHost = (msg.host || '').trim();
    if (!isLoopback(newHost)) {
      sendResponse({ ok: false, error: 'Only 127.0.0.1 / localhost are allowed' });
      return true;
    }
    activeHost = newHost === 'localhost' ? 'localhost' : '127.0.0.1';
    chrome.storage.local.set({ lastWorkingHost: activeHost }).then(() => {
      if (ws) {
        try { ws.close(); } catch (e) {}
        ws = null;
      }
      connectToBackend();
      sendResponse({ ok: true, host: activeHost });
    });
    return true; // async sendResponse
  } else if (msg.type === 'FORCE_SYNC') {
    performSync();
    sendResponse({ ok: true });
    return true;
  }
  return false;
});
