// TokenMeter Service Worker
//
// Strategy:
//   /api/events  -> pass through (SSE: never cache, never buffer)
//   GET /api/*   -> stale-while-revalidate API cache
//   mutating API -> network only
//   everything   -> cache-first static assets

const STATIC_CACHE = 'tokenmeter-static-v2';
const API_CACHE = 'tokenmeter-api-v1';
const MAX_API_CACHE_BYTES = 1024 * 1024;
const STATIC_ASSETS = [
  '/',
  '/index.html',
  '/manifest.json',
  '/icon-192.svg',
  '/icon-512.svg',
];

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(STATIC_CACHE).then((cache) => cache.addAll(STATIC_ASSETS))
  );
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(
        keys
          .filter((key) => key !== STATIC_CACHE && key !== API_CACHE)
          .map((key) => caches.delete(key))
      )
    )
  );
  self.clients.claim();
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;

  // SSE endpoint: never intercept. Returning without respondWith() leaves
  // the browser to handle it directly so EventSource keep-alive works.
  if (url.pathname === '/api/events') return;

  if (url.pathname.startsWith('/api/')) {
    if (event.request.method === 'GET') {
      event.respondWith(staleWhileRevalidate(req));
    }
    // POST/PUT/DELETE stay network-only.
    return;
  }

  if (req.method !== 'GET') return;
  event.respondWith(cacheFirst(req));
});

async function cacheFirst(req) {
  const cached = await caches.match(req);
  if (cached) {
    refreshStatic(req);
    return cached;
  }
  const resp = await fetch(req);
  if (resp && resp.ok && resp.type === 'basic') {
    const cache = await caches.open(STATIC_CACHE);
    await cache.put(req, resp.clone());
  }
  return resp;
}

function refreshStatic(req) {
  fetch(req)
    .then((resp) => {
      if (resp && resp.ok && resp.type === 'basic') {
        caches.open(STATIC_CACHE).then((cache) => cache.put(req, resp.clone()));
      }
    })
    .catch(() => {});
}

async function staleWhileRevalidate(req) {
  const cache = await caches.open(API_CACHE);
  const cached = await cache.match(req);
  const network = fetch(req)
    .then((resp) => {
      cacheAPIResponse(cache, req, resp).catch(() => {});
      return resp;
    })
    .catch(() => null);

  if (cached) {
    return withCacheHeader(cached);
  }

  const resp = await network;
  if (resp) return resp;
  return new Response(JSON.stringify({ error: 'offline', cached: false }), {
    status: 503,
    headers: { 'Content-Type': 'application/json' },
  });
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
