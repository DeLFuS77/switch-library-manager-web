// API keys, in My account and Users: a new key is shown once, in its window, and revoking one
// asks first.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

function initApiKeys() {
	const box = document.querySelector('[data-key-create]');
	if (box) {
		initKeyCreate(box);
	}
	document.querySelectorAll('[data-key-revoke]').forEach(button => button.addEventListener('click', () => {
		if (!window.confirm(button.dataset.confirm)) {
			return;
		}
		button.disabled = true;
		apiFetch('/api/keys/' + encodeURIComponent(button.dataset.keyRevoke), { method: 'DELETE' })
			.then(response => readJSON(response).then(json => {
				if (!response.ok) {
					throw new Error((json.globalError && json.globalError.message) || t('Unexpected server response (%v).', response.status));
				}
				window.location.assign(window.location.pathname + '?done=keyrevoked#keys');
			}))
			.catch(e => {
				button.disabled = false;
				showToast('alert-danger', 'bi-exclamation-octagon-fill', t('Error!'), e.message || t('Could not reach the server.'));
			});
	}));
}

// the window that makes a key: its form, then the key and the button that copies it
function initKeyCreate(box) {
	const modal = box.closest('.modal');
	const form = box.querySelector('form');
	const error = box.querySelector('[data-key-error]');
	const secret = box.querySelector('[data-key-secret]');
	const copy = box.querySelector('[data-key-copy]');
	const title = box.querySelector('[data-key-title]');
	const firstTitle = title.textContent;
	const step = name => box.querySelectorAll('[data-key-step]').forEach(part => {
		part.hidden = part.dataset.keyStep !== name;
	});
	let made = false;

	form.addEventListener('submit', e => {
		e.preventDefault();
		const button = form.querySelector('[type=submit]');
		error.hidden = true;
		button.disabled = true;
		apiFetch('/api/keys', { method: 'POST', body: new URLSearchParams(new FormData(form)) })
			.then(response => readJSON(response).then(json => {
				if (!response.ok) {
					throw new Error((json.globalError && json.globalError.message) || t('Unexpected server response (%v).', response.status));
				}
				made = true;
				secret.textContent = json.key;
				copy.dataset.copy = json.key;
				box.querySelector('[data-key-reveal-name]').textContent = box.dataset.keyLabel.replace('%v', json.name);
				box.querySelector('[data-key-header]').textContent = 'Authorization: Bearer ' + json.hint;
				title.textContent = box.dataset.madeTitle;
				box.querySelector('[data-key-title-icon]').className = 'bi bi-check-circle me-2 text-success';
				step('reveal');
				copy.focus();
			}))
			.catch(e => {
				box.querySelector('[data-key-error-message]').textContent = e.message || t('Could not reach the server.');
				error.hidden = false;
			})
			.finally(() => {
				button.disabled = false;
			});
	});

	modal.addEventListener('shown.bs.modal', () => {
		if (!made) {
			form.querySelector('#keyName').focus();
		}
	});
	// the key leaves the page with its window; the list shows the new one
	modal.addEventListener('hidden.bs.modal', () => {
		secret.textContent = '';
		copy.dataset.copy = '';
		if (made) {
			window.location.assign(window.location.pathname + '?done=keycreated#keys');
			return;
		}
		form.reset();
		error.hidden = true;
		title.textContent = firstTitle;
		step('form');
	});
}

document.addEventListener('DOMContentLoaded', initApiKeys);
