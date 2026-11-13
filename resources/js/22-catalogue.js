// The catalogue of the games, to print.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

// The catalogue lays out the games of a list of the Library on a sheet, as covers or as a list,
// to print it or save it as PDF with the browser. The choices are remembered in this browser.
// The covers are thumbnails, loaded as they come into view; before printing every one is asked
// for and waited for, as a printed page does not load images.
const CATALOGUE_KEY = 'slm-catalogue';

function initCatalogue() {
	const page = document.querySelector('[data-catalogue]');
	if (!page) {
		return;
	}
	const sheet = page.querySelector('[data-catalogue-sheet]');
	const holder = sheet.querySelector('[data-catalogue-games]');
	let games = [];
	try {
		games = JSON.parse(document.getElementById('catalogueGames')?.textContent || '[]') || [];
	} catch (e) {
		games = [];
	}

	const options = { layout: 'grid', covers: 'small', size: true, version: true };
	try {
		const stored = JSON.parse(localStorage.getItem(CATALOGUE_KEY) || '{}');
		if (stored.layout === 'grid' || stored.layout === 'list') {
			options.layout = stored.layout;
		}
		if (stored.covers === 'small' || stored.covers === 'large') {
			options.covers = stored.covers;
		}
		if (typeof stored.size === 'boolean') {
			options.size = stored.size;
		}
		if (typeof stored.version === 'boolean') {
			options.version = stored.version;
		}
	} catch (e) {
		// not kept: the catalogue starts with its choices by default
	}
	const save = () => {
		try {
			localStorage.setItem(CATALOGUE_KEY, JSON.stringify(options));
		} catch (e) {
			// not kept for the next time
		}
	};

	const element = (tag, className, text) => {
		const node = document.createElement(tag);
		if (className) {
			node.className = className;
		}
		if (text) {
			node.textContent = text;
		}
		return node;
	};
	const cover = game => {
		const img = element('img');
		img.src = game.cover || '/resources/static/noimage.png';
		img.alt = '';
		img.loading = 'lazy';
		img.decoding = 'async';
		return img;
	};
	const labels = sheet.dataset;

	const grid = () => {
		const list = element('div', 'catalogue-grid');
		games.forEach(game => {
			const item = element('div', 'catalogue-game' + (game.missing ? ' is-missing' : ''));
			item.append(cover(game), element('span', 'catalogue-name', game.name));
			const details = element('small', 'catalogue-details');
			if (game.missing) {
				details.append(element('span', 'catalogue-missing', labels.missing));
			}
			if (game.version) {
				details.append(element('span', 'catalogue-version', 'v' + game.version));
			}
			if (game.size) {
				details.append(element('span', 'catalogue-size', game.size));
			}
			item.append(details);
			list.append(item);
		});
		return list;
	};

	const table = () => {
		const list = element('table', 'catalogue-table');
		const head = element('tr');
		head.append(element('th', 'col-cover'), element('th', '', labels.name), element('th', 'col-id', labels.id),
			element('th', 'col-version', labels.version), element('th', '', labels.region), element('th', 'col-size', labels.size));
		const thead = element('thead');
		thead.append(head);
		list.append(thead);
		const body = element('tbody');
		games.forEach(game => {
			const row = element('tr', game.missing ? 'is-missing' : '');
			const picture = element('td', 'col-cover');
			picture.append(cover(game));
			const name = element('td', 'catalogue-name', game.name);
			if (game.missing) {
				name.append(element('small', 'catalogue-missing', labels.missing));
			}
			row.append(picture, name, element('td', 'col-id catalogue-id', game.id), element('td', 'col-version', game.version ? 'v' + game.version : ''),
				element('td', '', game.region || ''), element('td', 'col-size', game.size || ''));
			body.append(row);
		});
		list.append(body);
		return list;
	};

	let drawn = '';
	const draw = () => {
		sheet.dataset.layout = options.layout;
		sheet.dataset.covers = options.covers;
		// a column no game has a value for is left out too (the Missing games have no size)
		sheet.classList.toggle('hide-size', !options.size || !games.some(game => game.size));
		sheet.classList.toggle('hide-version', !options.version || !games.some(game => game.version));
		page.querySelectorAll('[data-catalogue-layout]').forEach(button => button.setAttribute('aria-pressed', String(button.dataset.catalogueLayout === options.layout)));
		page.querySelectorAll('[data-catalogue-covers]').forEach(button => button.setAttribute('aria-pressed', String(button.dataset.catalogueCovers === options.covers)));
		// the size of the covers is of the covers only
		const sizes = page.querySelector('[data-catalogue-sizes]');
		if (sizes) {
			sizes.hidden = options.layout !== 'grid';
		}
		page.querySelectorAll('[data-catalogue-show]').forEach(box => {
			box.checked = options[box.dataset.catalogueShow];
		});
		if (holder && drawn !== options.layout) {
			drawn = options.layout;
			holder.replaceChildren(options.layout === 'grid' ? grid() : table());
		}
	};

	page.querySelectorAll('[data-catalogue-layout]').forEach(button => button.addEventListener('click', () => {
		options.layout = button.dataset.catalogueLayout;
		save();
		draw();
	}));
	page.querySelectorAll('[data-catalogue-covers]').forEach(button => button.addEventListener('click', () => {
		options.covers = button.dataset.catalogueCovers;
		save();
		draw();
	}));
	page.querySelectorAll('[data-catalogue-show]').forEach(box => box.addEventListener('change', () => {
		options[box.dataset.catalogueShow] = box.checked;
		save();
		draw();
	}));
	draw();

	// printing: every cover is asked for at once, and the print waits for them; "Print now"
	// does not wait for the last ones
	const wait = page.querySelector('[data-catalogue-wait]');
	const waitText = page.querySelector('[data-catalogue-wait-text]');
	let timer = 0;
	const loadAll = () => {
		const images = [...sheet.querySelectorAll('img')];
		images.forEach(img => {
			img.loading = 'eager';
		});
		return images;
	};
	const print = () => {
		window.clearInterval(timer);
		timer = 0;
		if (wait) {
			wait.hidden = true;
		}
		window.print();
	};
	page.querySelector('[data-catalogue-print]')?.addEventListener('click', () => {
		if (timer) {
			return;
		}
		const images = loadAll();
		const check = () => {
			const ready = images.filter(img => img.complete).length;
			if (ready === images.length) {
				print();
				return;
			}
			if (wait && waitText) {
				waitText.textContent = t(page.dataset.wait, ready.toLocaleString(), images.length.toLocaleString());
				wait.hidden = false;
			}
		};
		timer = window.setInterval(check, 250);
		check();
	});
	page.querySelector('[data-catalogue-print-now]')?.addEventListener('click', print);
	// the print of the browser's own menu (Ctrl P) cannot wait: it asks for the covers still to load
	window.addEventListener('beforeprint', loadAll);
}
