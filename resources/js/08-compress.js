// Compress: the file lists and their selection.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

// same as formatSize on the server
function formatSize(size) {
	if (size < 1024) {
		return `${size} B`;
	}
	let unit = 1024;
	let exp = 0;
	for (let n = size / 1024; n >= 1024; n /= 1024) {
		unit *= 1024;
		exp++;
	}
	return `${(size / unit).toFixed(1)} ${'KMGTPE'[exp]}B`;
}

function initCompress() {
	// one handler for every cancel button, also those the live list of tasks draws again
	document.addEventListener('click', e => {
		const button = e.target.closest('[data-compress-cancel]');
		if (button) {
			button.disabled = true;
			fetch('/compress/cancel', { method: 'POST' });
		}
		// pause or resume: the live list of tasks draws the other button
		const pause = e.target.closest('[data-compress-pause]');
		if (pause) {
			pause.disabled = true;
			postForm('/compress/pause', { paused: pause.dataset.compressPause }).catch(() => {
				pause.disabled = false;
			});
		}
	});

	bindFileForm(document.getElementById('compressForm'), '/compress/start');
	bindFileForm(document.getElementById('decompressForm'), '/decompress/start');
	bindFileForm(document.getElementById('convertForm'), '/convert/start');
	bindFileForm(document.getElementById('splitForm'), '/split/start');
	bindFileForm(document.getElementById('packForm'), '/packs/start');
	bindFileForm(document.getElementById('spaceForm'), '/space/clean');
}

// a list of files with "select all", a summary of the selection and a start request
function bindFileForm(form, url) {
	if (!form) {
		return;
	}
	const all = form.querySelector('[data-file-all]');
	const summary = form.querySelector('[data-file-summary]');
	// rows can be replaced later (lists loaded from the server), so they are looked up each time
	const boxes = () => [...form.querySelectorAll('input[name="path"]:not(:disabled), input[name="rest"]')];
	const visible = box => !box.closest('[hidden]');
	const selection = () => {
		const selected = boxes().filter(box => box.checked);
		return {
			count: selected.reduce((sum, box) => sum + Number(box.dataset.count || 1), 0),
			size: selected.reduce((sum, box) => sum + Number(box.dataset.size || 0), 0)
		};
	};
	const update = () => {
		const { count, size } = selection();
		summary.textContent = summary.dataset.template.replace('%v', count).replace('%v', formatSize(size));
		const shown = boxes().filter(visible);
		const shownSelected = shown.filter(box => box.checked);
		all.checked = shown.length > 0 && shownSelected.length === shown.length;
		all.indeterminate = shownSelected.length > 0 && shownSelected.length < shown.length;
	};
	// "select all" changes the rows the filter shows
	all.addEventListener('change', () => {
		boxes().filter(visible).forEach(box => {
			box.checked = all.checked;
		});
		update();
	});
	form.addEventListener('change', e => {
		if (e.target.matches('input[name="path"], input[name="rest"]')) {
			update();
		}
	});

	// a list loaded from the server, e.g. thousands of NSZ files: only the rows that
	// match the filter are sent
	const list = form.querySelector('[data-file-list]');
	// only the answer to the last filter is drawn: typed fast, an older answer that arrives
	// later would show the rows of an old filter
	let loading = null;
	const load = query => {
		if (!list) {
			return Promise.resolve();
		}
		if (loading) {
			loading.abort();
		}
		const controller = new AbortController();
		loading = controller;
		return fetch(list.dataset.fileList + '?q=' + encodeURIComponent(query || ''), { signal: controller.signal })
			.then(response => {
				if (!response.ok) {
					throw new Error(String(response.status));
				}
				return response.text();
			})
			.then(html => {
				list.innerHTML = html;
				list.dataset.loaded = 'true';
				update();
			})
			.catch(() => undefined);
	};
	const details = form.closest('details');
	if (list && details) {
		details.addEventListener('toggle', () => {
			if (details.open && !list.dataset.loaded) {
				load('');
			}
		});
	}

	// the rows shown: those that match the filter by name and the kind chosen
	const filter = form.querySelector('[data-file-filter]');
	let kind = 'all';
	const applyFilters = () => {
		const text = filter ? filter.value.trim().toLowerCase() : '';
		form.querySelectorAll('[data-file-row]').forEach(row => {
			row.hidden = (text !== '' && !row.textContent.toLowerCase().includes(text)) || (kind !== 'all' && row.dataset.kind !== kind);
		});
		// a game without any file shown is hidden with its name
		form.querySelectorAll('[data-file-group]').forEach(group => {
			group.hidden = !group.querySelector('[data-file-row]:not([hidden])');
		});
		update();
	};
	const chips = form.querySelector('[data-kind-chips]');
	if (chips) {
		const rows = [...form.querySelectorAll('[data-file-row]')];
		const totals = {};
		rows.forEach(row => {
			const box = row.querySelector('input[name="path"]');
			const total = totals[row.dataset.kind] || (totals[row.dataset.kind] = { count: 0, size: 0 });
			total.count++;
			total.size += Number(box ? box.dataset.size : 0);
		});
		const icons = { all: 'bi-list-ul', game: 'bi-controller', update: 'bi-arrow-up-circle', dlc: 'bi-puzzle' };
		const buttons = [];
		['all', 'game', 'update', 'dlc'].forEach(key => {
			// "All" counts the smaller files listed together in the last row too
			const total = key === 'all' ? { count: rows.length + Number(chips.dataset.rest || 0) } : totals[key];
			if (!total || (key !== 'all' && Object.keys(totals).length < 2)) {
				return;
			}
			const button = document.createElement('button');
			button.type = 'button';
			button.className = `op-chip op-${key}`;
			button.setAttribute('aria-pressed', String(key === 'all'));
			button.innerHTML = `<span class="bi ${icons[key]}" aria-hidden="true"></span>`;
			button.append(chips.dataset[key]);
			const count = document.createElement('span');
			count.className = 'op-chip-count';
			count.textContent = total.count;
			button.append(count);
			if (total.size) {
				button.title = formatSize(total.size);
			}
			button.addEventListener('click', () => {
				kind = key;
				buttons.forEach(other => other.setAttribute('aria-pressed', String(other === button)));
				applyFilters();
			});
			buttons.push(button);
			chips.append(button);
		});
		chips.hidden = buttons.length < 2;
	}
	if (filter) {
		let timer;
		filter.addEventListener('input', () => {
			const text = filter.value.trim().toLowerCase();
			if (list) {
				clearTimeout(timer);
				timer = setTimeout(() => load(text), 300);
				return;
			}
			applyFilters();
		});
		// Enter in the filter must not submit the form
		filter.addEventListener('keydown', e => {
			if (e.key === 'Enter') {
				e.preventDefault();
			}
		});
	}
	update();

	form.addEventListener('submit', e => {
		e.preventDefault();
		form.querySelectorAll(':scope > .alert[role="alert"]').forEach(alert => alert.remove());
		const submit = form.querySelector('[type="submit"]');
		// deleting asks first, with the number and size of the files
		if (submit.dataset.confirm) {
			const { count, size } = selection();
			if (!window.confirm(submit.dataset.confirm.replace('%v', count).replace('%v', formatSize(size)))) {
				return;
			}
		}
		submit.disabled = true;
		runAsTask(() => fetch(url, {
			method: 'POST',
			body: new URLSearchParams(new FormData(form)).toString(),
			headers: { 'Content-type': 'application/x-www-form-urlencoded' }
		})).then(response => {
			if (response.ok) {
				return;
			}
			return response.json().then(json => {
				submit.disabled = false;
				insertAlert(form, 'alert-danger', 'bi-exclamation-triangle-fill', json.globalError.strongMessage, json.globalError.message);
			});
		}).catch(() => {
			submit.disabled = false;
			insertAlert(form, 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), t('Could not reach the server.'));
		});
	});
}

