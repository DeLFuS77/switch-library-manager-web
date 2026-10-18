// The quick search (Ctrl K).
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

function initQuickSearch() {
	const element = document.getElementById('quickSearch');
	const input = document.getElementById('quickSearchInput');
	const list = document.getElementById('quickSearchResults');
	if (!element || !input || !list) {
		return;
	}
	const empty = element.querySelector('.quick-search-empty');
	const status = document.getElementById('quickSearchStatus');
	const modal = Modal.getOrCreateInstance(element);
	// the focus goes back where it was when the window closes (opened with Ctrl+K too)
	let opener = null;
	element.addEventListener('show.bs.modal', () => {
		opener = document.activeElement;
	});
	element.addEventListener('hidden.bs.modal', () => {
		if (opener && document.contains(opener) && typeof opener.focus === 'function') {
			opener.focus();
		}
		opener = null;
	});
	if (/Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent)) {
		document.querySelectorAll('[data-search-kbd]').forEach(kbd => {
			kbd.textContent = '⌘K';
		});
	}

	let options = [];
	let active = -1;
	let timer = 0;
	let controller = null;
	const cache = new Map();

	const setActive = index => {
		if (options.length === 0) {
			active = -1;
			input.removeAttribute('aria-activedescendant');
			return;
		}
		active = (index + options.length) % options.length;
		options.forEach((option, i) => {
			const selected = i === active;
			option.classList.toggle('active', selected);
			option.setAttribute('aria-selected', selected ? 'true' : 'false');
		});
		input.setAttribute('aria-activedescendant', options[active].id);
		options[active].scrollIntoView({ block: 'nearest' });
	};

	const option = (item, id) => {
		const link = document.createElement('a');
		link.className = 'quick-search-option';
		link.href = item.href;
		link.id = id;
		link.setAttribute('role', 'option');
		link.setAttribute('aria-selected', 'false');
		link.tabIndex = -1;
		const media = document.createElement('span');
		media.className = 'quick-search-media';
		media.setAttribute('aria-hidden', 'true');
		const icon = () => media.classList.add('bi', item.icon || 'bi-arrow-right');
		if (item.image) {
			media.classList.add('has-cover');
			const img = document.createElement('img');
			img.src = item.image;
			img.alt = '';
			img.decoding = 'async';
			img.addEventListener('error', () => {
				img.remove();
				media.classList.remove('has-cover');
				icon();
			}, { once: true });
			media.appendChild(img);
		} else {
			icon();
		}
		const text = document.createElement('span');
		text.className = 'quick-search-text';
		const label = document.createElement('span');
		label.className = 'quick-search-label';
		label.textContent = item.label;
		text.appendChild(label);
		if (item.meta) {
			const meta = document.createElement('span');
			meta.className = 'quick-search-meta';
			meta.textContent = item.meta;
			text.appendChild(meta);
		}
		const go = document.createElement('span');
		go.className = 'quick-search-go bi bi-arrow-return-left';
		go.setAttribute('aria-hidden', 'true');
		link.append(media, text, go);
		return link;
	};

	const render = (results, query) => {
		list.replaceChildren();
		const groups = query ? ['games', 'series', 'collections', 'pages'] : ['pages'];
		let count = 0;
		groups.forEach(group => {
			const items = results[group] || [];
			if (items.length === 0) {
				return;
			}
			const section = document.createElement('div');
			section.className = 'quick-search-group';
			section.setAttribute('role', 'group');
			const heading = document.createElement('div');
			heading.className = 'quick-search-heading';
			heading.id = `quickSearchGroup-${group}`;
			heading.textContent = list.dataset[`label${group[0].toUpperCase()}${group.slice(1)}`] || group;
			section.setAttribute('aria-labelledby', heading.id);
			section.appendChild(heading);
			items.forEach(item => section.appendChild(option(item, `quickSearchOption-${count++}`)));
			list.appendChild(section);
		});
		options = [...list.querySelectorAll('.quick-search-option')];
		if (empty) {
			empty.hidden = count > 0 || !query;
		}
		// read by screen readers once the typing stops
		if (status && query) {
			status.textContent = count === 0 ? status.dataset.none : count === 1 ? status.dataset.one : status.dataset.many.replace('%v', count);
		}
		setActive(0);
	};

	const search = value => {
		const query = value.trim();
		const key = query.toLowerCase();
		if (cache.has(key)) {
			render(cache.get(key), key);
			return;
		}
		if (controller) {
			controller.abort();
		}
		controller = new AbortController();
		fetch(`/api/search?q=${encodeURIComponent(query)}`, { signal: controller.signal, headers: { Accept: 'application/json' } })
			.then(response => (response.ok ? response.json() : Promise.reject(response.status)))
			.then(results => {
				if (cache.size > 50) {
					cache.clear();
				}
				cache.set(key, results);
				// only the latest search is shown
				if (input.value.trim().toLowerCase() === key) {
					render(results, key);
				}
			})
			.catch(() => {
				// replaced by a newer search, or the server could not be reached
			});
	};

	input.addEventListener('input', () => {
		clearTimeout(timer);
		timer = setTimeout(() => search(input.value), QUICK_SEARCH_DELAY);
	});
	input.addEventListener('keydown', e => {
		if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
			e.preventDefault();
			setActive(active + (e.key === 'ArrowDown' ? 1 : -1));
		} else if (e.key === 'Enter') {
			e.preventDefault();
			const chosen = options[active];
			if (!chosen) {
				return;
			}
			if (e.ctrlKey || e.metaKey) {
				window.open(chosen.href, '_blank', 'noopener');
			} else {
				window.location.href = chosen.href;
			}
		}
	});
	list.addEventListener('mousemove', e => {
		const index = options.indexOf(e.target.closest('.quick-search-option'));
		if (index >= 0 && index !== active) {
			setActive(index);
		}
	});

	element.addEventListener('show.bs.modal', () => search(input.value));
	element.addEventListener('shown.bs.modal', () => {
		input.focus();
		input.select();
	});

	const open = () => {
		// on phones the menu may be open: it closes first
		const menu = document.querySelector('.offcanvas.show [data-bs-dismiss="offcanvas"]');
		if (menu) {
			menu.click();
		}
		modal.show();
	};
	document.querySelectorAll('[data-search-open]').forEach(button => button.addEventListener('click', open));
	document.addEventListener('keydown', e => {
		if ((e.ctrlKey || e.metaKey) && !e.altKey && !e.shiftKey && e.key.toLowerCase() === 'k') {
			e.preventDefault();
			if (element.classList.contains('show')) {
				modal.hide();
			} else {
				open();
			}
			return;
		}
		const typing = e.target.closest && e.target.closest('input, textarea, select, [contenteditable="true"]');
		if (e.key === '/' && !typing && !e.ctrlKey && !e.metaKey && !e.altKey && !document.querySelector('.modal.show')) {
			e.preventDefault();
			open();
		}
	});
}

