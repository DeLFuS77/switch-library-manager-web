// Install over the network, from the web: on the page of a game, Send to console (the files go to
// a console of the user, which asks before installing them) and what of the game the consoles
// have; with the console keys (Settings and My account), what is known to be installed on each
// console, and Forget.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

function netStorageName(storage) {
	if (storage === 'nand') {
		return t('Internal memory');
	}
	return storage === 'sd' ? t('SD card') : '';
}

function netWhen(value) {
	return new Date(value).toLocaleString(document.documentElement.lang || undefined, { dateStyle: 'medium', timeStyle: 'short' });
}

function netErrorMessage(response, json) {
	return (json.globalError && json.globalError.message) || response.statusText || t('Error!');
}

// fillConsoleInventories writes, under each console key of root, what is known to be installed on
// that console, with Forget. The keys of other users stay as they are.
function fillConsoleInventories(root) {
	const lines = [...(root || document).querySelectorAll('[data-console-inventory]')];
	if (!lines.length) {
		return;
	}
	fetch('/api/install/consoles', { headers: { Accept: 'application/json' } })
		.then(response => (response.ok ? response.json() : { consoles: [] }))
		.then(answer => {
			const byId = new Map((answer.consoles || []).map(console => [console.id, console]));
			lines.forEach(line => {
				const console = byId.get(line.dataset.consoleInventory);
				const inventory = console && console.inventory;
				line.replaceChildren();
				line.hidden = !inventory;
				if (!inventory) {
					return;
				}
				const text = document.createElement('span');
				const icon = document.createElement('span');
				icon.className = 'bi bi-hdd-stack me-1';
				icon.setAttribute('aria-hidden', 'true');
				text.append(icon, document.createTextNode(t('Installed on this console: %v titles · updated %v', inventory.titles.toLocaleString(document.documentElement.lang || undefined), netWhen(inventory.updated))));
				const forget = document.createElement('button');
				forget.type = 'button';
				forget.className = 'btn btn-link btn-sm';
				forget.textContent = t('Forget');
				forget.addEventListener('click', () => {
					if (!window.confirm(t('Forget what is installed on “%v”? The console sends it again the next time it is asked.', console.name))) {
						return;
					}
					forget.disabled = true;
					apiFetch('/api/install/inventory/' + encodeURIComponent(console.id), { method: 'DELETE' })
						.then(response => readJSON(response).then(json => {
							if (!response.ok) {
								throw new Error(netErrorMessage(response, json));
							}
							line.replaceChildren();
							line.hidden = true;
						}))
						.catch(e => {
							forget.disabled = false;
							showToast('alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), e.message);
						});
				});
				line.append(text, forget);
			});
		})
		.catch(() => {});
}

function initNetSend() {
	const button = document.querySelector('[data-net-send]');
	const on = document.querySelector('[data-net-on]');
	const titleId = (button && button.dataset.netSend) || (on && on.dataset.netOn);
	if (!titleId) {
		return;
	}
	const modalElement = document.getElementById('netSend');
	const form = document.querySelector('[data-net-send-form]');
	let game = { consoles: [], files: [] };

	// what the consoles have of the game, under its actions
	const showOnConsoles = () => {
		if (!on) {
			return;
		}
		on.replaceChildren();
		game.consoles.forEach(console => {
			if (!console.installed) {
				return;
			}
			const chip = document.createElement('span');
			chip.className = 'chip chip-console';
			const icon = document.createElement('span');
			icon.className = 'bi bi-nintendo-switch';
			icon.setAttribute('aria-hidden', 'true');
			const storage = netStorageName(console.installed.storage);
			const text = storage
				? t('On %v: v%v (%v)', console.name, console.installed.version, storage)
				: t('On %v: v%v', console.name, console.installed.version);
			chip.append(icon, document.createTextNode(text));
			on.append(chip);
		});
		on.hidden = !on.children.length;
	};

	// the files ticked at first: the game and its newest update, unless the console has them
	const ticks = console => {
		const installed = console && console.installed;
		const boxes = form.querySelectorAll('[data-net-send-files] input[type=checkbox]');
		boxes.forEach(box => {
			const file = game.files.find(item => item.id === box.value);
			let checked = file.checked;
			if (checked && installed) {
				checked = file.kind === 'update' ? installed.version < file.version : false;
			}
			box.checked = checked;
		});
	};

	const fileLabel = file => {
		if (file.kind === 'game') {
			return t('Game') + (file.version ? ' v' + file.version : '');
		}
		if (file.kind === 'update') {
			return t('Update') + ' v' + file.version;
		}
		return file.name;
	};

	const fillDialog = () => {
		const select = form.querySelector('[data-net-send-console]');
		select.replaceChildren(...game.consoles.map(console => {
			const option = document.createElement('option');
			option.value = console.id;
			option.textContent = console.name;
			return option;
		}));
		const list = form.querySelector('[data-net-send-files]');
		list.replaceChildren(...game.files.map((file, i) => {
			const item = document.createElement('div');
			item.className = 'form-check';
			const box = document.createElement('input');
			box.type = 'checkbox';
			box.className = 'form-check-input';
			box.id = 'net-send-file-' + i;
			box.value = file.id;
			const label = document.createElement('label');
			label.className = 'form-check-label small';
			label.htmlFor = box.id;
			const name = document.createElement('span');
			name.className = 'text-break';
			name.textContent = fileLabel(file);
			const size = document.createElement('span');
			size.className = 'text-body-secondary';
			size.textContent = formatSize(file.size);
			label.append(name, size);
			item.append(box, label);
			return item;
		}));
		ticks(game.consoles[0]);
		select.onchange = () => ticks(game.consoles.find(console => console.id === select.value));
	};

	const error = form && form.querySelector('[data-net-send-error]');
	const showError = message => {
		error.textContent = message;
		error.hidden = !message;
	};

	fetch('/api/install/game/' + encodeURIComponent(titleId), { headers: { Accept: 'application/json' } })
		.then(response => (response.ok ? response.json() : null))
		.then(answer => {
			if (!answer) {
				return;
			}
			game = { consoles: answer.consoles || [], files: answer.files || [] };
			showOnConsoles();
			if (button && form && modalElement && answer.enabled && game.consoles.length && game.files.length) {
				button.hidden = false;
			}
		})
		.catch(() => {});

	if (!button || !form || !modalElement) {
		return;
	}
	button.addEventListener('click', () => {
		showError('');
		fillDialog();
		Modal.getOrCreateInstance(modalElement).show();
	});
	form.addEventListener('submit', e => {
		e.preventDefault();
		const keyId = form.querySelector('[data-net-send-console]').value;
		const fileIds = [...form.querySelectorAll('[data-net-send-files] input:checked')].map(box => box.value);
		if (!fileIds.length) {
			showError(t('Tick at least one file.'));
			return;
		}
		showError('');
		const send = form.querySelector('[data-net-send-go]');
		send.disabled = true;
		apiFetch('/api/install/push', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ keyId, fileIds }) })
			.then(response => readJSON(response).then(json => {
				if (!response.ok) {
					throw new Error(netErrorMessage(response, json));
				}
				Modal.getOrCreateInstance(modalElement).hide();
				showToast('alert-success', 'bi-check-circle-fill', '', t('Sent: the console asks before installing.'));
			}))
			.catch(e => showError(e.message || t('Could not reach the server.')))
			.finally(() => {
				send.disabled = false;
			});
	});
}

document.addEventListener('DOMContentLoaded', () => {
	initNetSend();
	fillConsoleInventories(document);
});
