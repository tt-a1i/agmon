// TokenMeter Service Worker
//
// Strategy:
//   /api/events  → pass through (SSE: never cache, never buffer)
//   /api/*       → network-first; offline → 503 JSON {error:"offline"}
//   everything   → cache-first with background refresh (stale-while-revalidate)
//
// Bump VERSION on every release that ships a new HTML/JS/SVG asset — old
// caches are dropped in activate().

const VERSION = 'tokenmeter-v1';
const STATIC_ASSETS = [
  '/',
  '/index.html',
  '/manifest.json',
  '/icon-192.svg',
  '/icon-512.svg',
];

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(VERSION).then((cache) => cache.addAll(STATIC_ASSETS))
  );
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((k) => k !== VERSION).map((k) => caches.delete(k)))
    )
  );
  self.clients.claim();
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;

  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;

  // SSE endpoint: never intercept. Returning without respondWith() leaves
  // the browser to handle it directly so EventSource keep-alive works.
  if (url.pathname === '/api/events') return;

  // API endpoints: network-first. Failure surfaces as 503 JSON so the
  // existing dashboard error handlers can detect offline state.
  if (url.pathname.startsWith('/api/')) {
    event.respondWith(
      fetch(req).catch(
        () =>
          new Response(JSON.stringify({ error: 'offline' }), {
            status: 503,
            headers: { 'Content-Type': 'application/json' },
          })
      )
    );
    return;
  }

  // Static assets: serve cached copy immediately if present, refresh in
  // the background so the next visit sees newer content.
  event.respondWith(
    caches.match(req).then((cached) => {
      const network = fetch(req)
        .then((resp) => {
          if (resp && resp.ok && resp.type === 'basic') {
            const clone = resp.clone();
            caches.open(VERSION).then((cache) => cache.put(req, clone));
          }
          return resp;
        })
        .catch(() => cached);
      return cached || network;
    })
  );
});
