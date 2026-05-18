// TokenMeter Service Worker
//
// Cache strategy:
//   /api/events (SSE)        -> pass-through, never intercept
//   GET /api/*               -> network-first w/ 5s timeout, API cache fallback
//   GET document/script/...  -> stale-while-revalidate static cache
//   non-GET                  -> network-only (never cache mutations)
//
// On activate every cache whose name does not start with CACHE_VERSION
// is deleted; bumping CACHE_VERSION purges the previous generation so
// users do not get stuck on stale assets.

const CACHE_VERSION = 'tm-v2';
const STATIC_CACHE = 'tm-v2-static';
const API_CACHE = 'tm-v2-api';
const API_TTL_MS = 5000;
const MAX_API_CACHE_BYTES = 1024 * 1024;
const STATIC_ASSETS = [
  '/',
  '/index.html',
  '/manifest.json',
  '/icon-192.svg',
  '/icon-512.svg',
  '/icon-maskable.svg',
  '/favicon.svg',
];

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(STATIC_CACHE).then((cache) =>
      cache.addAll(STATIC_ASSETS).catch(() => {})
    )
  );
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(
        keys
          .filter((k) => !k.startsWith(CACHE_VERSION))
          .map((k) => caches.delete(k))
      )
    )
  );
  self.clients.claim();
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;

  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;

  // SSE: leave the browser to manage the stream end-to-end.
  if (url.pathname === '/api/events' || url.pathname.endsWith('/events')) return;

  if (url.pathname.startsWith('/api/')) {
    event.respondWith(networkFirst(req, API_TTL_MS));
    return;
  }

  const dest = req.destination;
  if (
    dest === 'document' ||
    dest === 'script' ||
    dest === 'style' ||
    dest === 'image' ||
    dest === 'font' ||
    dest === '' // manifest / icon requests can leave destination empty
  ) {
    event.respondWith(staleWhileRevalidate(req));
  }
});

async function networkFirst(req, timeoutMs) {
  const cache = await caches.open(API_CACHE);
  try {
    const network = await fetchWithTimeout(req, timeoutMs);
    if (network && network.ok) {
      cacheAPIResponse(cache, req, network).catch(() => {});
    }
    return network;
  } catch (_) {
    const cached = await cache.match(req);
    if (cached) return withCacheHeader(cached);
    return new Response(JSON.stringify({ error: 'offline', cached: false }), {
      status: 503,
      headers: { 'Content-Type': 'application/json' },
    });
  }
}

function fetchWithTimeout(req, timeoutMs) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('timeout')), timeoutMs);
    fetch(req).then(
      (resp) => {
        clearTimeout(timer);
        resolve(resp);
      },
      (err) => {
        clearTimeout(timer);
        reject(err);
      }
    );
  });
}

async function staleWhileRevalidate(req) {
  const cache = await caches.open(STATIC_CACHE);
  const cached = await cache.match(req);
  const network = fetch(req)
    .then((resp) => {
      if (resp && resp.ok && resp.type === 'basic') {
        cache.put(req, resp.clone()).catch(() => {});
      }
      return resp;
    })
    .catch(() => null);
  if (cached) return cached;
  const fresh = await network;
  if (fresh) return fresh;
  // Last-resort offline fallback so SPA navigations still render.
  const shell = await cache.match('/index.html');
  if (shell) return shell;
  return new Response('offline', { status: 503, headers: { 'Content-Type': 'text/plain' } });
}

async function cacheAPIResponse(cache, req, resp) {
  if (!resp || !resp.ok) return;
  const contentType = resp.headers.get('Content-Type') || '';
  if (!contentType.includes('application/json')) return;

  const contentLength = Number(resp.headers.get('Content-Length') || 0);
  if (contentLength > MAX_API_CACHE_BYTES) return;

  if (contentLength > 0) {
    await cache.put(req, resp.clone());
    return;
  }
  const clone = resp.clone();
  const body = await clone.arrayBuffer();
  if (body.byteLength > MAX_API_CACHE_BYTES) return;
  await cache.put(
    req,
    new Response(body, {
      status: resp.status,
      statusText: resp.statusText,
      headers: resp.headers,
    })
  );
}

function withCacheHeader(resp) {
  const headers = new Headers(resp.headers);
  headers.set('X-TokenMeter-Cache', 'hit');
  return new Response(resp.body, {
    status: resp.status,
    statusText: resp.statusText,
    headers,
  });
}

// Allow the page to trigger immediate activation of a new SW build via
// navigator.serviceWorker.controller.postMessage({type: 'SKIP_WAITING'}).
self.addEventListener('message', (event) => {
  if (event.data && event.data.type === 'SKIP_WAITING') self.skipWaiting();
});
