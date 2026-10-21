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
			// a page is asked for while the worker starts, instead of after: going from one page
			// to another no longer waits for it
			.then(() => (self.registration.navigationPreload ? self.registration.navigationPreload.enable() : undefined))
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
			const fresh = () => fetch(request).then(response => {
				if (response.ok) {
					const copy = response.clone();
					cache.keys()
						.then(keys => Promise.all(keys.filter(key => new URL(key.url).pathname === url.pathname && key.url !== request.url).map(key => cache.delete(key))))
						.then(() => cache.put(request, copy))
						.catch(() => undefined);
				}
				return response;
			});
			// the styles and scripts with the version of their contents in their address never
			// change: kept as they are, without asking the server again on every page
			if (cached && url.searchParams.get('v')) {
				return cached;
			}
			if (cached) {
				event.waitUntil(fresh().catch(() => undefined));
				return cached;
			}
			return fresh();
		})).catch(() => fetch(request)));
		return;
	}

	// pages: from the server, with a notice when it cannot be reached
	if (request.mode === 'navigate') {
		event.respondWith(Promise.resolve(event.preloadResponse)
			.then(preloaded => preloaded || fetch(request))
			.catch(() => caches.match(OFFLINE_PAGE).then(page => page || Response.error())));
	}
});
