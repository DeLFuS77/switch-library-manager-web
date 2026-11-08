// Settings > Install over the network: the consoles (API keys of the role Console of the user
// signed in), a new key shown once, the lines for DBI and for the installers of the kind of
// Tinfoil with the address of the server, and what the console will see.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

function initNetInstall() {
	const section = document.querySelector('[data-net-install]');
	if (!section) {
		return;
	}
	const master = section.querySelector('[data-net-master]');
	const fields = section.querySelector('[data-net-fields]');
	const list = section.querySelector('[data-net-consoles]');
	const nameInput = section.querySelector('#net_console_name');
	const addButton = section.querySelector('[data-net-add-button]');
	const error = section.querySelector('[data-net-error]');
	const keyBox = section.querySelector('[data-net-key]');
	const origin = section.querySelector('[data-net-origin]');
	// the key just made, only in this page: it is never sent again
	let freshKey = '';

	if (master && fields) {
		const update = () => fields.classList.toggle('is-off', !master.checked);
		master.addEventListener('change', update);
		update();
	}

	const showError = message => {
		error.textContent = message;
		error.hidden = !message;
	};
	const failure = (response, json) => new Error((json.globalError && json.globalError.message) || response.statusText);

	// the lines to copy, with the key just made or a place for it
	const lines = () => {
		const base = (origin.value.trim() || window.location.origin).replace(/\/+$/, '');
		let address;
		try {
			address = new URL(base);
		} catch (e) {
			address = new URL(window.location.origin);
		}
		const key = freshKey || 'slm_…';
		const dbi = address.protocol + '//console:' + key + '@' + address.host + address.pathname.replace(/\/+$/, '') + '/install/dbi/';
		const tinfoil = address.protocol + '//' + address.host + address.pathname.replace(/\/+$/, '') + '/install/tinfoil';
		section.querySelectorAll('[data-net-line]').forEach(code => {
			code.textContent = code.dataset.netLine === 'dbi' ? 'SLMW=ApacheHTTP|' + dbi : tinfoil;
		});
		section.querySelectorAll('[data-net-copy]').forEach(button => {
			button.dataset.copy = section.querySelector('[data-net-line="' + button.dataset.netCopy + '"]').textContent;
		});
	};
	origin.value = window.location.origin;
	origin.addEventListener('input', lines);
	lines();

	const when = value => new Date(value).toLocaleString(document.documentElement.lang || undefined, { dateStyle: 'medium', timeStyle: 'short' });
	const row = key => {
		const item = document.createElement('li');
		item.className = 'net-console';
		const icon = document.createElement('span');
		icon.className = 'bi bi-nintendo-switch';
		icon.setAttribute('aria-hidden', 'true');
		const text = document.createElement('div');
		text.className = 'flex-grow-1 min-w-0';
		const name = document.createElement('div');
		name.className = 'fw-semibold text-break';
		name.textContent = key.name;
		const detail = document.createElement('div');
		detail.className = 'small text-body-secondary';
		detail.textContent = key.hint + ' · ' + (key.lastUsed ? list.dataset.lastUsed.replace('%v', when(key.lastUsed)) : list.dataset.never);
		text.append(name, detail);
		const remove = document.createElement('button');
		remove.type = 'button';
		remove.className = 'btn btn-sm btn-outline-secondary rounded-pill text-nowrap';
		remove.textContent = list.dataset.remove;
		remove.addEventListener('click', () => {
			if (!window.confirm(list.dataset.confirm.replace('%v', key.name))) {
				return;
			}
			remove.disabled = true;
			apiFetch('/api/keys/' + encodeURIComponent(key.id), { method: 'DELETE' })
				.then(response => readJSON(response).then(json => {
					if (!response.ok) {
						throw failure(response, json);
					}
					load();
				}))
				.catch(e => {
					remove.disabled = false;
					showError(e.message);
				});
		});
		item.append(icon, text, remove);
		return item;
	};

	const load = () => apiFetch('/api/keys')
		.then(response => readJSON(response).then(json => {
			if (!response.ok) {
				throw failure(response, json);
			}
			const consoles = (Array.isArray(json) ? json : json.keys || []).filter(key => key.role === 'console');
			list.replaceChildren(...consoles.map(row));
			if (!consoles.length) {
				const empty = document.createElement('li');
				empty.className = 'small text-body-secondary';
				empty.textContent = list.dataset.empty;
				list.append(empty);
			}
		}))
		.catch(e => showError(e.message));

	const add = () => {
		const name = nameInput.value.trim();
		if (!name) {
			nameInput.focus();
			return;
		}
		showError('');
		addButton.disabled = true;
		apiFetch('/api/keys', { method: 'POST', body: new URLSearchParams({ name, role: 'console' }) })
			.then(response => readJSON(response).then(json => {
				if (!response.ok) {
					throw failure(response, json);
				}
				freshKey = json.key;
				const title = keyBox.querySelector('[data-net-key-title]');
				title.textContent = title.dataset.text.replace('%v', json.name);
				keyBox.querySelector('[data-net-key-secret]').textContent = json.key;
				keyBox.querySelector('[data-net-key-copy]').dataset.copy = json.key;
				keyBox.hidden = false;
				nameInput.value = '';
				lines();
				load();
			}))
			.catch(e => showError(e.message))
			.finally(() => {
				addButton.disabled = false;
			});
	};
	addButton.addEventListener('click', add);
	// Enter adds the console instead of saving the settings around it
	nameInput.addEventListener('keydown', e => {
		if (e.key === 'Enter') {
			e.preventDefault();
			add();
		}
	});

	load();
	apiFetch('/settings/net-install/summary')
		.then(response => response.ok ? response.json() : null)
		.then(summary => {
			if (!summary) {
				return;
			}
			section.querySelectorAll('[data-net-count]').forEach(cell => {
				const value = summary[cell.dataset.netCount];
				cell.textContent = typeof value === 'number' ? value.toLocaleString(document.documentElement.lang || undefined) : '–';
			});
		})
		.catch(() => {});
}

document.addEventListener('DOMContentLoaded', initNetInstall);
