// Service worker of Switch Library Manager Web. It only keeps the app's own static
// files (styles, scripts, fonts, icons) so pages open faster and the app can be
// installed; library pages and data always come from the server.
const CACHE = 'slm-static-__VERSION__';
const OFFLINE_PAGE = '/resources/static/offline.html';

// a cache that cannot be used (private mode, full disk) must not stop the app: everything
// then simply comes from the server
self.addEventListener('install', event => {
	event.waitUntil(caches.open(CACHE).then(cache => cache.add(OFFLINE_PAGE)).catch(() => undefined).then(() => self.skipWaiting()));
});

// caches of previous versions are removed
self.addEventListener('activate', event => {
	event.waitUntil(
		caches.keys()
			.then(keys => Promise.all(keys.filter(key => key.startsWith('slm-static-') && key !== CACHE).map(key => caches.delete(key))))
			.catch(() => undefined)
			.then(() => self.clients.claim())
	);
});

function isStatic(url) {
	return url.origin === self.location.origin &&
		(url.pathname.startsWith('/resources/static/') || url.pathname.startsWith('/resources/vendor/'));
}

self.addEventListener('fetch', event => {
	const request = event.request;
	if (request.method !== 'GET') {
		return;
	}
	const url = new URL(request.url);

	// static files: answered from the cache and refreshed in the background
	if (isStatic(url)) {
		event.respondWith(caches.open(CACHE).then(cache => cache.match(request).then(cached => {
			const fresh = fetch(request).then(response => {
				if (response.ok) {
					cache.put(request, response.clone()).catch(() => undefined);
				}
				return response;
			});
			if (cached) {
				event.waitUntil(fresh.catch(() => undefined));
				return cached;
			}
			return fresh;
		})).catch(() => fetch(request)));
		return;
	}

	// pages: from the server, with a notice when it cannot be reached
	if (request.mode === 'navigate') {
		event.respondWith(fetch(request).catch(() => caches.match(OFFLINE_PAGE).then(page => page || Response.error())));
	}
});
