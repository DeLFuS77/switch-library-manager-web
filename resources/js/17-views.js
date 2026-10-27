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

// the Tasks page by bars or by covers, remembered by the browser
function initTaskView() {
	const list = document.getElementById('taskList');
	const buttons = document.querySelectorAll('[data-task-view]');
	if (!list || !buttons.length) {
		return;
	}
	const apply = view => {
		list.classList.toggle('tasks-by-covers', view === 'covers');
		buttons.forEach(button => {
			const active = button.dataset.taskView === view;
			button.classList.toggle('active', active);
			button.setAttribute('aria-pressed', active ? 'true' : 'false');
		});
	};
	let view = 'bars';
	try {
		view = localStorage.getItem('slm-task-view') || 'bars';
	} catch (e) {
		// storage may be blocked, the bars are shown
	}
	apply(view);
	buttons.forEach(button => button.addEventListener('click', () => {
		apply(button.dataset.taskView);
		try {
			localStorage.setItem('slm-task-view', button.dataset.taskView);
		} catch (e) {
			// not remembered
		}
	}));
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
// page (the menu, a game page) shows it with its order and filters, from its first page and
// without a search made before (going back keeps it, from the history of the browser). On the
// list itself, removing the filters shows everything.
const FILTER_PAGES = ['/index.html', '/updates.html', '/dlc.html', '/missing.html'];

function listPath(pathname) {
	return pathname === '/' ? '/index.html' : pathname;
}

// filtersOf is the query of a list without its page and its search: going back to a list
// starts at its first page, with its order and filters, and a search made before is not kept
function filtersOf(query) {
	const params = new URLSearchParams(query);
	params.delete('page');
	params.delete('q');
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

// Page tabs: the parts of a long page (Organize, Compress) one at a time, like the tabs of a
// folder, instead of one under the other. The tab shown is in the address (#name), so a reload
// or a link opens it again. A panel shown tells it (slm:tab-shown), for what it loads then.
function initPageTabs() {
	document.querySelectorAll('[data-page-tabs]').forEach(list => {
		const tabs = [...list.querySelectorAll('[role="tab"]')];
		if (tabs.length === 0) {
			return;
		}
		const show = (tab, focus, remember) => {
			tabs.forEach(other => {
				const selected = other === tab;
				other.setAttribute('aria-selected', String(selected));
				other.tabIndex = selected ? 0 : -1;
				other.classList.toggle('active', selected);
				const panel = document.getElementById(other.getAttribute('aria-controls'));
				if (panel) {
					const wasHidden = panel.hidden;
					panel.hidden = !selected;
					if (selected && wasHidden) {
						panel.dispatchEvent(new CustomEvent('slm:tab-shown'));
					}
				}
			});
			// on phones the row scrolls sideways: the tab shown comes into view
			if (typeof tab.scrollIntoView === 'function') {
				tab.scrollIntoView({ block: 'nearest', inline: 'nearest' });
			}
			if (focus) {
				tab.focus();
			}
			if (remember) {
				history.replaceState(history.state, '', '#' + tab.dataset.tab);
			}
		};
		const named = tabs.find(tab => '#' + tab.dataset.tab === window.location.hash);
		show(named || tabs.find(tab => tab.getAttribute('aria-selected') === 'true') || tabs[0], false, false);
		tabs.forEach((tab, i) => {
			tab.addEventListener('click', () => show(tab, false, true));
			// the arrows go from tab to tab, as in the tabs of the system
			tab.addEventListener('keydown', e => {
				const next = {
					ArrowRight: tabs[(i + 1) % tabs.length],
					ArrowLeft: tabs[(i - 1 + tabs.length) % tabs.length],
					Home: tabs[0],
					End: tabs[tabs.length - 1],
				}[e.key];
				if (next) {
					e.preventDefault();
					show(next, true, true);
				}
			});
		});
	});
}