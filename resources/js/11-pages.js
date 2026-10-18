// Small behaviors of some pages: long texts, what is new, cover transitions, settings sections.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

// a long text shows its start, with a button for the rest
function initCollapsibleText() {
	document.querySelectorAll('[data-collapsible]').forEach(wrap => {
		const button = wrap.parentElement.querySelector('[data-collapsible-toggle]');
		if (!button) {
			return;
		}
		wrap.classList.add('is-collapsed');
		// short enough: shown whole, without a button
		if (wrap.scrollHeight <= wrap.clientHeight + 24) {
			wrap.classList.remove('is-collapsed');
			return;
		}
		button.hidden = false;
		button.addEventListener('click', () => {
			const collapsed = wrap.classList.contains('is-collapsed');
			if (collapsed) {
				wrap.style.maxHeight = wrap.scrollHeight + 'px';
				wrap.classList.remove('is-collapsed');
			} else {
				wrap.style.maxHeight = '';
				wrap.classList.add('is-collapsed');
			}
			button.textContent = collapsed ? button.dataset.less : button.dataset.more;
		});
	});
}

// the window of what is new after an update: once per version and browser, unless the box to
// stop seeing it was ticked (without storage it shows each time, which is harmless)
function initWhatsNew() {
	const element = document.querySelector('[data-whats-new]');
	if (!element) {
		return;
	}
	const seenKey = 'slm-whats-new';
	const offKey = 'slm-whats-new-off';
	const read = key => {
		try {
			return localStorage.getItem(key) || '';
		} catch (e) {
			return '';
		}
	};
	const write = (key, value) => {
		try {
			localStorage.setItem(key, value);
		} catch (e) {
			// not kept: it shows again next time
		}
	};
	if (read(offKey) === '1' || read(seenKey) === element.dataset.whatsNew) {
		element.remove();
		return;
	}
	element.addEventListener('hidden.bs.modal', () => {
		write(seenKey, element.dataset.whatsNew);
		if (element.querySelector('[data-whats-new-off]').checked) {
			write(offKey, '1');
		}
		element.remove();
	});
	// after the page has appeared, and never over another window (the setup wizard)
	setTimeout(() => {
		if (!wizardLoading && !document.querySelector('.modal.show')) {
			Modal.getOrCreateInstance(element).show();
		}
	}, 700);
}

// the cover of the game that is opened flies to the cover of its page (see the view
// transitions in web.scss); only that cover takes part, so the names never repeat
function initCoverTransition() {
	if (!('startViewTransition' in document) || reducedMotion()) {
		return;
	}
	document.addEventListener('click', e => {
		const link = e.target.closest('a[href^="/title/"]');
		const card = link && link.closest('.game-card');
		if (!card || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey) {
			return;
		}
		document.querySelectorAll('.is-flying').forEach(image => image.classList.remove('is-flying'));
		const image = card.querySelector('.game-cover img');
		if (image) {
			image.classList.add('is-flying');
		}
	});
	// a transition between pages skipped (another click, the page hidden) is not an error
	const quiet = e => {
		if (e.viewTransition) {
			e.viewTransition.ready.catch(() => {});
			e.viewTransition.finished.catch(() => {});
		}
	};
	window.addEventListener('pageswap', quiet);
	window.addEventListener('pagereveal', quiet);
	// back on the list, the cover that flew is a plain cover again
	window.addEventListener('pageshow', () => {
		setTimeout(() => document.querySelectorAll('.is-flying').forEach(image => image.classList.remove('is-flying')), 600);
	});
}

// Settings: each section of the menu on its own, as a page of its own, instead of one long
// page; the address (#library, #backup...) tells which one, and Save still saves them all
function initSettingsSections() {
	const nav = document.querySelector('.settings-nav');
	const sections = [...document.querySelectorAll('.settings-section')];
	if (nav && sections.length < 2) {
		// a page of its own (Diagnostics): the row of sections shows the active one
		const active = nav.querySelector('.active');
		if (active && nav.scrollWidth > nav.clientWidth) {
			nav.scrollLeft = active.offsetLeft - nav.offsetLeft - 16;
		}
		return;
	}
	if (!nav) {
		return;
	}
	const saveBar = document.querySelector('#settingsForm .save-bar');
	const links = [...nav.querySelectorAll('a[href^="#"]')];
	const show = (id, toTop) => {
		const target = sections.find(section => section.id === id) || sections[0];
		sections.forEach(section => {
			section.hidden = section !== target;
		});
		document.documentElement.setAttribute('data-section', target.id);
		links.forEach(link => {
			const active = link.getAttribute('href') === `#${target.id}`;
			link.classList.toggle('active', active);
			if (active) {
				link.setAttribute('aria-current', 'page');
				// phones: the row of sections scrolls sideways to the active one, the page stays
				if (nav.scrollWidth > nav.clientWidth) {
					const left = link.offsetLeft - nav.offsetLeft;
					if (left < nav.scrollLeft || left + link.offsetWidth > nav.scrollLeft + nav.clientWidth) {
						nav.scrollLeft = left - 16;
					}
				}
			} else {
				link.removeAttribute('aria-current');
			}
		});
		// the backup is not part of the settings form
		if (saveBar) {
			saveBar.hidden = !target.closest('#settingsForm');
		}
		// back to the start of the section only when it is above the window, at once, so the
		// menu does not travel with the page
		const top = target.getBoundingClientRect().top + window.scrollY - 96;
		if (toTop && window.scrollY > top) {
			window.scrollTo({ top: Math.max(0, top), behavior: 'auto' });
		}
	};
	nav.addEventListener('click', e => {
		const link = e.target.closest('a[href^="#"]');
		if (!link) {
			return;
		}
		e.preventDefault();
		history.replaceState(null, '', link.getAttribute('href'));
		show(link.getAttribute('href').slice(1), true);
	});
	window.addEventListener('hashchange', () => show(window.location.hash.slice(1), true));
	// a field the browser or the server finds wrong is shown in its section
	const showField = field => {
		const section = field.closest('.settings-section');
		if (section && section.hidden) {
			show(section.id, false);
		}
		field.scrollIntoView({ block: 'center' });
	};
	document.addEventListener('invalid', e => showField(e.target), true);
	document.addEventListener('slm:show-field', e => showField(e.detail));
	document.documentElement.classList.add('has-settings-sections');
	show(window.location.hash.slice(1), false);
}

// The quick search: Ctrl+K (Cmd+K on a Mac), "/" or the magnifier in the header open it from
// any page. It finds games, series, collections and pages as you type; the arrows choose a
// result and Enter opens it.
const QUICK_SEARCH_DELAY = 120;

