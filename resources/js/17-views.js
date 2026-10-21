// Card tilt, grid and list views, and the filters kept per page.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

// cards tilt slightly towards the pointer
function initCardTilt() {
	if (reducedMotion() || !window.matchMedia('(hover: hover) and (pointer: fine)').matches) {
		return;
	}
	document.querySelectorAll('.game-card').forEach(card => {
		card.addEventListener('pointermove', e => {
			const box = card.getBoundingClientRect();
			const x = (e.clientX - box.left) / box.width - .5;
			const y = (e.clientY - box.top) / box.height - .5;
			card.style.setProperty('--tilt-x', `${(-y * 6).toFixed(2)}deg`);
			card.style.setProperty('--tilt-y', `${(x * 6).toFixed(2)}deg`);
			card.classList.add('is-tilting');
		});
		card.addEventListener('pointerleave', () => {
			card.classList.remove('is-tilting');
			card.style.removeProperty('--tilt-x');
			card.style.removeProperty('--tilt-y');
		});
	});
}

const VIEW_STORAGE_KEY = 'slm-view';

// large or small covers, remembered by the browser
function initViewToggle() {
	const buttons = document.querySelectorAll('[data-view]');
	if (!buttons.length) {
		return;
	}

	const apply = view => {
		document.querySelectorAll('.game-grid').forEach(grid => grid.classList.toggle('is-compact', view === 'compact'));
		buttons.forEach(button => {
			const active = button.dataset.view === view;
			button.classList.toggle('active', active);
			button.setAttribute('aria-pressed', active ? 'true' : 'false');
		});
	};

	let view = 'cards';
	try {
		view = localStorage.getItem(VIEW_STORAGE_KEY) || 'cards';
	} catch (e) {
		// storage may be blocked, the default view is used
	}
	apply(view);

	buttons.forEach(button => button.addEventListener('click', () => {
		apply(button.dataset.view);
		try {
			localStorage.setItem(VIEW_STORAGE_KEY, button.dataset.view);
		} catch (e) {
			// not remembered
		}
	}));
}

// The filters of each list are remembered by the browser: coming back to a list from another
// page (the menu, a game page) shows it as it was left, from its first page. On the list itself,
// removing the filters shows everything.
const FILTER_PAGES = ['/index.html', '/updates.html', '/dlc.html', '/missing.html'];

function listPath(pathname) {
	return pathname === '/' ? '/index.html' : pathname;
}

// filtersOf is the query of a list without its page: going back to a list starts at its
// first page, with its search, order and filters
function filtersOf(query) {
	const params = new URLSearchParams(query);
	params.delete('page');
	const rest = params.toString();
	return rest ? '?' + rest : '';
}

function storedFilters(path) {
	try {
		return filtersOf(localStorage.getItem('slm-filters:' + path) || '');
	} catch (e) {
		return '';
	}
}

// restoreFilters returns true when the page is replaced by the remembered filters.
function restoreFilters() {
	const path = listPath(window.location.pathname);
	if (!FILTER_PAGES.includes(path)) {
		return false;
	}
	const key = 'slm-filters:' + path;
	let from = '';
	try {
		const referrer = new URL(document.referrer);
		if (referrer.origin === window.location.origin) {
			from = listPath(referrer.pathname);
		}
	} catch (e) {
		// no referrer
	}
	try {
		const query = window.location.search;
		if (!query && from !== path) {
			const stored = storedFilters(path);
			if (stored) {
				window.location.replace(path + stored);
				return true;
			}
		}
		localStorage.setItem(key, filtersOf(query));
	} catch (e) {
		// storage is not available: the lists start without filters
	}
	return false;
}

// linkRememberedFilters makes the links to the other lists carry their remembered filters:
// the list opens filtered at once, instead of opening and then loading again with them. The
// links to the list shown stay as they are, to show it without filters.
function linkRememberedFilters() {
	const here = listPath(window.location.pathname);
	document.querySelectorAll('a[href]').forEach(link => {
		const href = link.getAttribute('href');
		if (href === here || !FILTER_PAGES.includes(listPath(href))) {
			return;
		}
		const stored = storedFilters(listPath(href));
		if (stored) {
			link.setAttribute('href', listPath(href) + stored);
		}
	});
}
