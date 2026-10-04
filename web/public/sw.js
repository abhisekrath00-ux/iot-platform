/* Offline app shell. Self-hosted, no network dependency of its own.
 *
 * What it does: keeps the built UI (index.html and the hashed /assets/ files) so the app opens with no
 * connection, for example on a phone on a plant floor. What it never does: cache anything from /v1/, /auth/
 * or /healthz. Live data, sign-in and tokens always go to the network, so nothing private is stored and
 * nothing stale is shown as current. Offline, the app shell loads and each page shows its own "could not
 * reach the server" state.
 */
var CACHE = 'hexmon-shell-v1';

// route decides how a request is handled: 'bypass' (let the browser do it), 'asset' (cache first, files are
// content-hashed) or 'nav' (network first, fall back to the cached shell).
function route(method, urlString, mode, origin) {
  if (method !== 'GET') return 'bypass';
  var u = new URL(urlString);
  if (u.origin !== origin) return 'bypass';
  var p = u.pathname;
  if (p.indexOf('/v1/') === 0 || p.indexOf('/auth/') === 0 || p === '/healthz' || p === '/sw.js') return 'bypass';
  if (p.indexOf('/assets/') === 0) return 'asset';
  if (mode === 'navigate') return 'nav';
  if (p === '/manifest.webmanifest' || p === '/icon.svg') return 'asset';
  return 'bypass';
}

if (typeof self !== 'undefined' && self.addEventListener) {
  self.addEventListener('install', function (e) {
    e.waitUntil(caches.open(CACHE).then(function (c) { return c.addAll(['/', '/manifest.webmanifest', '/icon.svg']); }).then(function () { return self.skipWaiting(); }));
  });
  self.addEventListener('activate', function (e) {
    e.waitUntil(caches.keys().then(function (ks) {
      return Promise.all(ks.filter(function (k) { return k !== CACHE; }).map(function (k) { return caches.delete(k); }));
    }).then(function () { return self.clients.claim(); }));
  });
  self.addEventListener('fetch', function (e) {
    var r = e.request;
    var kind = route(r.method, r.url, r.mode, self.location.origin);
    if (kind === 'bypass') return;
    if (kind === 'asset') {
      e.respondWith(caches.match(r).then(function (hit) {
        if (hit) return hit;
        return fetch(r).then(function (res) {
          if (res.ok && res.type === 'basic') { var copy = res.clone(); caches.open(CACHE).then(function (c) { c.put(r, copy); }); }
          return res;
        });
      }));
      return;
    }
    e.respondWith(fetch(r).then(function (res) {
      if (res.ok && res.type === 'basic') { var copy = res.clone(); caches.open(CACHE).then(function (c) { c.put('/', copy); }); }
      return res;
    }).catch(function () { return caches.match('/'); }));
  });
}
if (typeof module !== 'undefined') module.exports = { route: route };
