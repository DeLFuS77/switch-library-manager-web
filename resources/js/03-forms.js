// The forms of the preferences: saving them, and their save buttons.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

// The save buttons of the preferences (Settings, Organize, My account) are on only when something
// changed since the page was opened or last saved: the bar of Settings slides away, the other
// buttons go dim. A field filled by the app itself (the folder picker...) is noticed too, as the
// form is looked at again every second.
function formState(form) {
	const values = [];
	for (const field of form.elements) {
		if (!field.name || field.disabled || field.type === 'submit' || field.type === 'button' || field.type === 'file') {
			continue;
		}
		values.push(field.name + '=' + ((field.type === 'checkbox' || field.type === 'radio') ? field.checked : field.value));
	}
	return JSON.stringify(values);
}

// Settings > Library: the prod.keys (and title.keys) of the console are sent to the server of the
// app, which checks them and keeps them in its data folder; the page then shows the keys found.
function initKeysUpload() {
	const box = document.querySelector('[data-keys-upload]');
	if (!box) {
		return;
	}
	const chosen = box.querySelector('[data-keys-chosen]');
	const send = box.querySelector('[data-keys-send]');
	const inputs = [...box.querySelectorAll('[data-keys-file]')];
	const update = () => {
		const names = inputs.filter(input => input.files.length).map(input => input.files[0].name);
		chosen.textContent = names.join(' · ');
		send.hidden = !box.querySelector('#prodKeysFile').files.length;
	};
	box.querySelectorAll('[data-keys-pick]').forEach(button => {
		button.addEventListener('click', () => document.getElementById(button.dataset.keysPick).click());
	});
	inputs.forEach(input => input.addEventListener('change', update));
	send.addEventListener('click', () => {
		const data = new FormData();
		inputs.filter(input => input.files.length).forEach(input => data.append(input.dataset.keysFile, input.files[0]));
		send.disabled = true;
		fetch('/settings/keys', { method: 'POST', body: data })
			.then(response => response.json().then(json => ({ ok: response.ok, json })))
			.then(({ ok, json }) => {
				if (!ok) {
					const error = json.globalError || {};
					showToast('alert-danger', 'bi-exclamation-triangle-fill', error.strongMessage || t('Error!'), error.message || t('Could not reach the server.'));
					send.disabled = false;
					return;
				}
				showToast('alert-success', 'bi-check-circle-fill', json.strongMessage, json.message);
				setTimeout(() => window.location.reload(), 1500);
			})
			.catch(() => {
				showToast('alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), t('Could not reach the server.'));
				send.disabled = false;
			});
	});
}

function initSaveWhenChanged() {
	document.querySelectorAll('form[data-save-when-changed]').forEach(form => {
		let saved = formState(form);
		const buttons = [...form.querySelectorAll('button[type="submit"]')];
		const bar = form.querySelector('.save-bar');
		const update = () => {
			const changed = formState(form) !== saved;
			form.classList.toggle('is-unchanged', !changed);
			if (bar) {
				bar.classList.toggle('is-unchanged', !changed);
				bar.inert = !changed;
			} else {
				buttons.forEach(button => {
					button.disabled = !changed;
				});
			}
		};
		form.addEventListener('input', update);
		form.addEventListener('change', update);
		form.addEventListener('slm:recheck', update);
		form.addEventListener('slm:saved', () => {
			saved = formState(form);
			update();
		});
		setInterval(() => {
			if (!document.hidden) {
				update();
			}
		}, 1000);
		update();
	});
}

function onSubmit(form) {
	const feedbackAlert = form.querySelector('.alert');
	if (feedbackAlert) {
		form.removeChild(feedbackAlert);
	}

	form.querySelectorAll('.is-invalid').forEach(e => e.classList.remove('is-invalid'));
	form.querySelectorAll('.invalid-feedback').forEach(e => e.parentNode.removeChild(e));

	const submitButton = form.querySelector('[type="submit"]');
	if (submitButton) {
		submitButton.disabled = true;
	}

	const data = new FormData(form);
	fetch(form.action || window.location.href, {
		method: 'POST',
		body: new URLSearchParams(data).toString(),
		headers: {
			'Content-type': 'application/x-www-form-urlencoded'
		}
	}).then(response => {
		if (!response.ok) {
			throw response;
		}

		return response.json().then(jsonResponse => {
			insertAlert(form, 'alert-success', 'bi-check-circle-fill', jsonResponse.strongMessage, jsonResponse.message);
			// what is saved is the new starting point: the save button goes off until the next change
			form.dispatchEvent(new CustomEvent('slm:saved'));
			if (form.dataset.rescan) {
				// saving the settings rescans the library
				showSyncAlert();
				watchSync();
			}
		});
	}).catch(error => {
		if (!(error instanceof Response)) {
			insertAlert(form, 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), t('Could not reach the server.'));
			return;
		}

		error.json().then(jsonResponse => {
			const globalError = jsonResponse.globalError || {};
			if (globalError.strongMessage || globalError.message) {
				insertAlert(form, 'alert-danger', 'bi-exclamation-triangle-fill', globalError.strongMessage, globalError.message);
			} else if (jsonResponse.fieldErrors && jsonResponse.fieldErrors.length) {
				jsonResponse.fieldErrors.forEach(fieldError => {
					const field = form.querySelector(`[name="${CSS.escape(fieldError.field)}"]`);
					// a field the page does not have: the message is still shown
					if (!field) {
						insertAlert(form, 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), fieldError.message);
						return;
					}
					const validationFeedback = document.createElement('div');
					validationFeedback.id = `validation-feedback-${fieldError.field}`;
					validationFeedback.classList.add('invalid-feedback');
					validationFeedback.appendChild(document.createTextNode(fieldError.message));

					field.setAttribute('aria-describedby', `validation-feedback-${fieldError.field}`);
					field.classList.add('is-invalid');

					field.parentElement.appendChild(validationFeedback);
				});
				// the first field with an error is shown, also in another section of Settings
				const first = form.querySelector('.is-invalid');
				if (first) {
					document.dispatchEvent(new CustomEvent('slm:show-field', { detail: first }));
				}
			}
		}).catch(() => {
			insertAlert(form, 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), t('Unexpected server response (%v).', error.status));
		});
	}).finally(() => {
		if (submitButton) {
			submitButton.disabled = false;
			form.dispatchEvent(new CustomEvent('slm:recheck'));
		}
	});
}

const OPERATION_LABELS = {
	mkdir: t('Create folder'),
	move: t('Move'),
	delete: t('Delete'),
	skip: t('Skip'),
	cleanup: t('Clean up')
};

function postForm(url, params) {
	return fetch(url, {
		method: 'POST',
		body: new URLSearchParams(params).toString(),
		headers: {
			'Content-type': 'application/x-www-form-urlencoded'
		}
	}).then(response => response.json().catch(() => ({})).then(json => {
		if (!response.ok) {
			const message = (json.globalError && json.globalError.message) || t('Unexpected server response (%v).', response.status);
			throw new Error(message);
		}
		return json;
	}));
}

// the kinds of changes of the preview, in the order of the summary: what needs a look first
const OPERATION_KINDS = [
	{ key: 'error', label: t('Error'), icon: 'bi-x-octagon' },
	{ key: 'review', label: t('Review'), icon: 'bi-exclamation-triangle' },
	{ key: 'move', label: t('Move'), icon: 'bi-arrow-right-circle' },
	{ key: 'rename', label: t('Rename'), icon: 'bi-input-cursor-text' },
	{ key: 'mkdir', label: t('Create folder'), icon: 'bi-folder-plus' },
	{ key: 'delete', label: t('Delete'), icon: 'bi-trash3' },
	{ key: 'skip', label: t('Skip'), icon: 'bi-skip-forward-circle' },
	{ key: 'cleanup', label: t('Empty folders'), icon: 'bi-folder-x' }
];

const splitPath = path => {
	const index = Math.max(path.lastIndexOf('/'), path.lastIndexOf('\\'));
	return index < 0 ? ['', path] : [path.slice(0, index), path.slice(index + 1)];
};
const nameId = name => (name.match(/\[([0-9A-Fa-f]{16})\]/) || [])[1];
const nameVersion = name => (name.match(/\[v(\d+)\]/) || [])[1];

