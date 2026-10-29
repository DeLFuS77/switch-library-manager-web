// Compress: the file lists and their selection.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

// the languages of the page that write decimals with a comma, as decimalSeparators on the server
const DECIMAL_COMMA_LANGUAGES = ['es', 'pt', 'it', 'de', 'nl', 'fr', 'ru'];

// same as formatSizeIn on the server, in the language of the page
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
	const number = (size / unit).toFixed(1);
	return `${DECIMAL_COMMA_LANGUAGES.includes(document.documentElement.lang) ? number.replace('.', ',') : number} ${'KMGTPE'[exp]}B`;
}

function initCompress() {
	// one handler for every cancel button, also those the live list of tasks draws again
	document.addEventListener('click', e => {
		const button = e.target.closest('[data-compress-cancel]');
		if (button) {
			button.disabled = true;
			// a cancel that did not reach the server can be asked again
			postForm('/compress/cancel', {}).catch(() => {
				button.disabled = false;
			});
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
// followRemoval asks every second which files the cleanup has deleted so far and takes their
// rows off the list, and a section with no row left; stop() asks once more and ends.
function followRemoval(form, update) {
	let known = 0;
	let timer = 0;
	const take = paths => {
		paths.forEach(path => {
			const box = [...form.querySelectorAll('input[name="path"]')].find(input => input.value === path);
			const row = box && box.closest('li');
			if (!row) {
				return;
			}
			const list = row.parentElement;
			row.remove();
			if (list && !list.querySelector('li')) {
				const section = list.closest('section');
				if (section) {
					section.remove();
				}
			}
		});
		update();
	};
	const ask = () => fetch('/space/clean/progress?from=' + known)
		.then(response => (response.ok ? response.json() : { paths: [] }))
		.then(json => {
			const paths = json.paths || [];
			known += paths.length;
			take(paths);
		})
		.catch(() => undefined);
	const tick = () => {
		ask().then(() => {
			if (timer) {
				timer = setTimeout(tick, 1000);
			}
		});
	};
	timer = setTimeout(tick, 1000);
	return {
		stop() {
			clearTimeout(timer);
			timer = 0;
			ask();
		}
	};
}

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
		// "1 file selected", not "1 files"
		const template = count === 1 && summary.dataset.templateOne ? summary.dataset.templateOne : summary.dataset.template;
		summary.textContent = template.replace('%v', count).replace('%v', formatSize(size));
		const shown = boxes().filter(visible);
		const shownSelected = shown.filter(box => box.checked);
		all.checked = shown.length > 0 && shownSelected.length === shown.length;
		all.indeterminate = shownSelected.length > 0 && shownSelected.length < shown.length;
	};
	// "select all" changes the rows the filter shows
	// "select all" leaves out the files that ask first: they are chosen one by one
	all.addEventListener('change', () => {
		boxes().filter(visible).filter(box => !box.dataset.ask).forEach(box => {
			box.checked = all.checked;
		});
		update();
	});
	form.addEventListener('change', e => {
		if (e.target.matches('input[name="path"], input[name="rest"]')) {
			if (e.target.checked && e.target.dataset.ask && !window.confirm(e.target.dataset.ask)) {
				e.target.checked = false;
			}
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
		return apiFetch(list.dataset.fileList + '?q=' + encodeURIComponent(query || ''), { signal: controller.signal })
			.then(response => {
				if (!response.ok) {
					throw new Error(t('Unexpected server response (%v).', response.status));
				}
				return response.text();
			})
			.then(html => {
				list.innerHTML = html;
				list.dataset.loaded = 'true';
				update();
			})
			.catch(error => {
				// a newer filter took its place, or the rows of an earlier answer stay
				if (controller.signal.aborted || list.dataset.loaded) {
					return;
				}
				// instead of "Loading...", why the list is not there: the tab shown again asks again
				const note = list.querySelector('p');
				if (note) {
					note.textContent = error instanceof TypeError ? t('Could not reach the server.') : error.message;
				}
			});
	};
	// a list is loaded when its tab is first shown
	const panel = form.closest('[role="tabpanel"]');
	if (list && panel) {
		const loadOnce = () => {
			if (!panel.hidden && !list.dataset.loaded) {
				load('');
			}
		};
		panel.addEventListener('slm:tab-shown', loadOnce);
		loadOnce();
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
		// Space: the files deleted leave the list as they go, not all at the end
		const following = url === '/space/clean' ? followRemoval(form, update) : null;
		const send = spaceOk => runAsTask(() => apiFetch(url, {
			method: 'POST',
			body: new URLSearchParams([...new FormData(form), ...(spaceOk ? [['space_ok', 'true']] : [])]).toString(),
			headers: { 'Content-type': 'application/x-www-form-urlencoded' }
		}).finally(() => following && following.stop())).then(response => {
			if (response.ok) {
				return response.json().catch(() => ({})).then(json => {
					if (json && json.queued) {
						submit.disabled = false;
						insertQueuedNote(form, json.queued);
					}
				});
			}
			return readJSON(response).then(json => {
				// the new files may not fit: asked first, with where and how much
				if (response.status === 409 && json.space) {
					const lines = json.space.map(drive => '• ' + drive.folder + ': ' + t('up to %v needed, %v free').replace('%v', formatSize(drive.need)).replace('%v', formatSize(drive.free)));
					if (window.confirm(t('The new files may not fit. A file that fills its disk fails, and its original stays as it was.') + '\n\n' + lines.join('\n') + '\n\n' + t('Start anyway?'))) {
						return send(true);
					}
					submit.disabled = false;
					return;
				}
				submit.disabled = false;
				// an answer without a message (a proxy's) says its status
				const error = json.globalError || {};
				insertAlert(form, 'alert-danger', 'bi-exclamation-triangle-fill', error.strongMessage || t('Error!'), error.message || t('Unexpected server response (%v).', response.status));
			});
		});
		send(false).catch(() => {
			submit.disabled = false;
			insertAlert(form, 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), t('Could not reach the server.'));
		});
	});
}

