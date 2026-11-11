// The page Saves: the saves NXDock uploaded from the consoles of the user, by game, with Note (a
// note written in place), Download, Delete (asked first) and the pin that keeps a save from being
// deleted by itself when a game has more than ten.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

function netSaveError(response, json) {
	return (json.globalError && json.globalError.message) || response.statusText || t('Error!');
}

// netSaveChange sends a change of a save and gives the save as it is now.
function netSaveChange(row, change) {
	return apiFetch(row.dataset.netSave, {
		method: 'PATCH',
		headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
		body: JSON.stringify(change),
	}).then(response => readJSON(response).then(json => {
		if (!response.ok) {
			throw new Error(netSaveError(response, json));
		}
		return json;
	}));
}

function netSaveFailed(e) {
	showToast('alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), e.message);
}

function netSaveShowPin(row, pinned) {
	const button = row.querySelector('[data-net-save-pin]');
	const label = pinned ? t('Unpin') : t('Pin');
	row.classList.toggle('is-pinned', pinned);
	button.setAttribute('aria-pressed', pinned ? 'true' : 'false');
	button.title = label;
	button.querySelector('.bi').className = 'bi ' + (pinned ? 'bi-pin-angle-fill' : 'bi-pin-angle');
	button.querySelector('.visually-hidden').textContent = label;
}

function netSaveShowNote(row, note) {
	const text = row.querySelector('[data-net-save-note]');
	text.textContent = note || t('No note');
	text.classList.toggle('is-empty', !note);
}

// netSaveEdit writes the note of a save in place: Enter or leaving the field keeps it, Esc does not.
function netSaveEdit(row) {
	const text = row.querySelector('[data-net-save-note]');
	if (row.querySelector('[data-net-save-input]')) {
		row.querySelector('[data-net-save-input]').focus();
		return;
	}
	const before = text.classList.contains('is-empty') ? '' : text.textContent;
	const input = document.createElement('input');
	input.type = 'text';
	input.className = 'form-control form-control-sm net-save-input';
	input.maxLength = 200;
	input.value = before;
	input.dataset.netSaveInput = '';
	input.setAttribute('aria-label', t('Note of the copy'));
	text.hidden = true;
	text.after(input);
	input.focus();
	let done = false;
	const finish = keep => {
		if (done) {
			return;
		}
		done = true;
		const note = input.value.trim();
		input.remove();
		text.hidden = false;
		if (!keep || note === before) {
			return;
		}
		netSaveShowNote(row, note);
		netSaveChange(row, { note })
			.then(save => netSaveShowNote(row, save.note))
			.catch(e => {
				netSaveShowNote(row, before);
				netSaveFailed(e);
			});
	};
	input.addEventListener('keydown', event => {
		if (event.key === 'Enter') {
			event.preventDefault();
			finish(true);
		} else if (event.key === 'Escape') {
			event.preventDefault();
			finish(false);
		}
	});
	input.addEventListener('blur', () => finish(true));
}

// netSaveCounted writes how many saves a game and the page have left.
function netSaveCounted(root, group) {
	const left = group.querySelectorAll('[data-net-save]').length;
	if (!left) {
		group.remove();
	} else {
		group.querySelector('[data-net-save-count]').textContent = left === 1 ? t('1 copy') : t('%v copies', left);
	}
	const all = root.querySelectorAll('[data-net-save]').length;
	const count = document.querySelector('.page-header .page-count');
	if (count) {
		count.textContent = all.toLocaleString(document.documentElement.lang || undefined);
	}
	if (!all) {
		// the page tells there is nothing left, as it does when it opens
		window.location.reload();
	}
}

function netSaveDelete(root, row, button) {
	if (!window.confirm(t('Delete this copy of %v (%v)? It cannot be undone.', row.dataset.netSaveGame, row.dataset.netSaveWhen))) {
		return;
	}
	button.disabled = true;
	apiFetch(row.dataset.netSave, { method: 'DELETE', headers: { Accept: 'application/json' } })
		.then(response => readJSON(response).then(json => {
			if (!response.ok) {
				throw new Error(netSaveError(response, json));
			}
			const group = row.closest('[data-net-save-group]');
			row.remove();
			netSaveCounted(root, group);
		}))
		.catch(e => {
			button.disabled = false;
			netSaveFailed(e);
		});
}

function initNetSaves() {
	const root = document.querySelector('[data-net-saves]');
	if (!root) {
		return;
	}
	root.addEventListener('click', event => {
		const row = event.target.closest('[data-net-save]');
		if (!row) {
			return;
		}
		const pin = event.target.closest('[data-net-save-pin]');
		if (pin) {
			const pinned = pin.getAttribute('aria-pressed') !== 'true';
			pin.disabled = true;
			netSaveShowPin(row, pinned);
			netSaveChange(row, { pinned })
				.then(save => netSaveShowPin(row, save.pinned))
				.catch(e => {
					netSaveShowPin(row, !pinned);
					netSaveFailed(e);
				})
				.finally(() => {
					pin.disabled = false;
				});
			return;
		}
		if (event.target.closest('[data-net-save-edit]')) {
			netSaveEdit(row);
			return;
		}
		const remove = event.target.closest('[data-net-save-delete]');
		if (remove) {
			netSaveDelete(root, row, remove);
		}
	});
}

document.addEventListener('DOMContentLoaded', initNetSaves);
