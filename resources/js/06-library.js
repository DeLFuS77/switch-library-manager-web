// Bulk actions of the lists, collections, ignored types and the SD card planner.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

function initBulkActions() {
	const toolbar = document.querySelector('.bulk-toolbar');
	if (!toolbar) {
		return;
	}
	const items = [...document.querySelectorAll('[data-bulk-item]')];
	const selectAll = toolbar.querySelector('[data-bulk-select-all]');
	const button = toolbar.querySelector('[data-bulk-ignore]');
	const count = toolbar.querySelector('[data-bulk-count]');

	const update = () => {
		const selected = items.filter(item => item.checked).length;
		count.textContent = selected;
		button.disabled = selected === 0;
		selectAll.checked = selected > 0 && selected === items.length;
		selectAll.indeterminate = selected > 0 && selected < items.length;
	};

	items.forEach(item => item.addEventListener('change', update));
	selectAll.addEventListener('change', () => {
		items.forEach(item => item.checked = selectAll.checked);
		update();
	});

	button.addEventListener('click', () => {
		const params = new URLSearchParams();
		params.append('kind', toolbar.dataset.bulkKind);
		params.append('ignored', 'true');
		items.filter(item => item.checked).forEach(item => params.append('id', item.value));
		button.disabled = true;
		postForm('/ignore', params).then(() => {
			window.location.reload();
		}).catch(error => {
			update();
			insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), error.message);
		});
	});
}

// selection of games in the library, for actions on several games at once
function initLibraryBulk() {
	const toolbar = document.querySelector('.library-bulk');
	if (!toolbar) {
		return;
	}
	const items = [...document.querySelectorAll('[data-library-item]')];
	const count = toolbar.querySelector('[data-library-count]');
	const collection = toolbar.querySelector('[data-library-collection]');
	const selected = () => items.filter(item => item.checked).map(item => item.value);
	const update = () => {
		const ids = selected();
		count.textContent = ids.length;
		toolbar.hidden = ids.length === 0;
		// the boxes of the other cards show while selecting
		document.querySelector('.game-grid')?.classList.toggle('is-selecting', ids.length > 0);
		items.forEach(item => item.closest('.game-card')?.classList.toggle('is-selected', item.checked));
	};
	items.forEach(item => {
		item.addEventListener('change', update);
		// the card is a link: the checkbox must not open the game
		item.addEventListener('click', e => e.stopPropagation());
	});
	toolbar.querySelector('[data-library-all]').addEventListener('click', () => {
		items.forEach(item => item.checked = true);
		update();
	});
	toolbar.querySelector('[data-library-none]').addEventListener('click', () => {
		items.forEach(item => item.checked = false);
		update();
	});
	toolbar.querySelectorAll('[data-library-action]').forEach(button => {
		button.addEventListener('click', () => {
			const action = button.dataset.libraryAction;
			const params = new URLSearchParams();
			selected().forEach(id => params.append('id', id));
			let url = '/collections';
			if (action === 'ignore') {
				url = '/ignore';
				params.append('kind', 'update');
				params.append('ignored', 'true');
			} else {
				const name = collection.value.trim();
				if (!name) {
					collection.focus();
					return;
				}
				params.append('name', name);
				params.append('action', action);
			}
			button.disabled = true;
			postForm(url, params).then(() => {
				window.location.reload();
			}).catch(error => {
				button.disabled = false;
				insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), error.message);
			});
		});
	});
	update();
}

// the collections of a game, on its page
function initCollections() {
	const form = document.querySelector('[data-collections]');
	if (!form) {
		return;
	}
	const id = form.dataset.collections;
	const send = (name, add) => {
		const params = new URLSearchParams({ id, name, action: add ? 'add' : 'remove' });
		return postForm('/collections', params).catch(error => {
			insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), error.message);
			throw error;
		});
	};
	form.querySelectorAll('[data-collection-toggle]').forEach(toggle => {
		toggle.addEventListener('change', () => {
			send(toggle.value, toggle.checked).then(() => window.location.reload(), () => toggle.checked = !toggle.checked);
		});
	});
	form.addEventListener('submit', e => {
		e.preventDefault();
		const input = form.querySelector('[data-collection-new]');
		const name = input.value.trim();
		if (name) {
			send(name, true).then(() => window.location.reload(), () => {});
		}
	});
}

// ignore a type of file listed in Issues: the library is scanned again without them
function initIgnoreTypes() {
	document.querySelectorAll('[data-ignore-type]').forEach(button => {
		button.addEventListener('click', () => {
			button.disabled = true;
			postForm('/issues/ignore-type', { type: button.dataset.ignoreType }).then(() => {
				showSyncAlert();
				watchSync();
			}).catch(error => {
				button.disabled = false;
				insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), error.message);
			});
		});
	});
}

// the SD card planner: totals of the chosen games, download of the list and copy
function initSdPlanner() {
	const summary = document.querySelector('[data-sd-usable]');
	if (!summary) {
		return;
	}
	const usable = Number(summary.dataset.sdUsable);
	const games = [...document.querySelectorAll('[data-sd-game]')];
	const chosen = () => games.filter(game => game.checked);
	// the chosen games the page does not list count too
	const hiddenCount = Number(summary.dataset.sdHiddenCount || 0);
	const hiddenSize = Number(summary.dataset.sdHiddenSize || 0);
	const update = () => {
		const total = chosen().reduce((sum, game) => sum + Number(game.dataset.size), hiddenSize);
		summary.querySelector('[data-sd-count]').textContent = chosen().length + hiddenCount;
		summary.querySelector('[data-sd-total]').textContent = summary.dataset.sdTemplate.replace('%v', formatSize(total)).replace('%v', formatSize(usable));
		const bar = summary.querySelector('[data-sd-bar]');
		const pct = Math.min(100, usable > 0 ? total * 100 / usable : 100);
		bar.style.width = pct + '%';
		bar.parentElement.setAttribute('aria-valuenow', Math.round(pct));
		// amber when the card is nearly full, red only when the games do not fit
		bar.classList.toggle('is-near', total <= usable && pct >= 90);
		bar.classList.toggle('bg-danger', total > usable);
		summary.querySelector('[data-sd-over]').hidden = total <= usable;
	};
	games.forEach(game => game.addEventListener('change', update));
	update();

	const form = document.querySelector('[data-sd-copy]');
	if (!form) {
		return;
	}
	const params = () => {
		const values = new URLSearchParams();
		chosen().forEach(game => values.append('id', game.value));
		document.querySelectorAll('[data-sd-hidden]').forEach(hidden => values.append('id', hidden.value));
		if (form.dataset.sdDlc) {
			values.append('dlc', '1');
		}
		return values;
	};
	form.querySelector('[data-sd-download]').addEventListener('click', () => {
		// a form post, so the browser downloads the file
		const download = document.createElement('form');
		download.method = 'post';
		download.action = '/sd/list.txt';
		params().forEach((value, key) => {
			const input = document.createElement('input');
			input.type = 'hidden';
			input.name = key;
			input.value = value;
			download.appendChild(input);
		});
		document.body.appendChild(download);
		download.submit();
		download.remove();
	});
	form.addEventListener('submit', e => {
		e.preventDefault();
		const values = params();
		values.append('target', form.elements.target.value);
		const button = form.querySelector('[type=submit]');
		button.disabled = true;
		runAsTask(() => postForm('/sd/copy', values)).catch(error => {
			button.disabled = false;
			insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), error.message);
		});
	});
}


// how the game goes for the user, on its page: the state and the stars are kept at once, the
// note with its button
function initPlay() {
	const form = document.querySelector('[data-play]');
	if (!form) {
		return;
	}
	const button = form.querySelector('button[type="submit"]');
	const note = form.querySelector('textarea[name="note"]');
	let savedNote = note.value;
	const save = () => {
		const data = new FormData(form);
		const params = { id: form.dataset.play, status: data.get('status') || '', rating: data.get('rating') || '0', note: note.value };
		return postForm('/play', params).then(() => {
			savedNote = note.value;
			button.disabled = true;
			showToast('text-bg-success', 'bi-check-circle-fill', form.dataset.saved, '');
		}).catch(error => {
			insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), error.message);
		});
	};
	form.addEventListener('change', e => {
		if (e.target.name === 'status' || e.target.name === 'rating') {
			save();
		}
	});
	note.addEventListener('input', () => {
		button.disabled = note.value === savedNote;
	});
	form.addEventListener('submit', e => {
		e.preventDefault();
		save();
	});
}

// smart collections: the filters shown kept under a name, and deleted
function initSmartCollections() {
	const row = document.querySelector('[data-smart-collections]');
	if (!row) {
		return;
	}
	const fail = error => insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), error.message);
	const save = row.querySelector('[data-smart-save]');
	if (save) {
		save.addEventListener('click', () => {
			const name = window.prompt(row.dataset.askName, '');
			if (!name || !name.trim()) {
				return;
			}
			postForm('/smart-collections', { name: name.trim(), query: window.location.search })
				.then(() => window.location.reload())
				.catch(fail);
		});
	}
	row.querySelectorAll('[data-smart-delete]').forEach(button => button.addEventListener('click', () => {
		const name = button.dataset.smartDelete;
		if (!window.confirm(row.dataset.askDelete.replace('%v', name))) {
			return;
		}
		postForm('/smart-collections', { name, action: 'delete' })
			.then(() => window.location.reload())
			.catch(fail);
	}));
}
