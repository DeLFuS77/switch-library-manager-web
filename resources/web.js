// the translations of the page, as JSON data (inline scripts are not allowed)
function readTranslations() {
	try {
		const data = document.getElementById('slm-translations');
		return data ? JSON.parse(data.textContent) : {};
	} catch (e) {
		return {};
	}
}

// Bundled with Bootstrap by esbuild into resources/static/web.js (npm run build).
// Importing Bootstrap also enables its data attributes (offcanvas, dismissible alerts, ...).
import { Dropdown, Modal, Tab, Tooltip } from 'bootstrap';
import qrcode from 'qrcode-generator';

// Translations of the texts below are provided by the server in the page language.
// "%v" placeholders are replaced by the arguments in order.
function t(text, ...args) {
	const translations = window.SLM_TRANSLATIONS || readTranslations();
	let result = translations[text] || text;
	args.forEach(arg => {
		result = result.replace('%v', arg);
	});
	return result;
}

// Messages appear as notifications in a corner of the screen and go away by themselves:
// successes after a few seconds, errors after a little longer. Hovering keeps them, and the
// same message is shown once.
const TOAST_SUCCESS_MS = 5000;
const TOAST_ERROR_MS = 10000;
const MAX_TOASTS = 4;

function toastStack() {
	let stack = document.getElementById('toastStack');
	if (!stack) {
		stack = document.createElement('div');
		stack.id = 'toastStack';
		stack.className = 'toast-stack';
		stack.setAttribute('aria-live', 'polite');
		document.body.appendChild(stack);
	}
	return stack;
}

function closeToast(toast) {
	clearTimeout(toast.toastTimer);
	toast.classList.remove('show');
	setTimeout(() => toast.remove(), 200);
}

function startToastTimer(toast) {
	clearTimeout(toast.toastTimer);
	toast.toastTimer = setTimeout(() => closeToast(toast), toast.toastDuration);
}

function showToast(contextualClass, iconClass, strongMessage, message) {
	const stack = toastStack();
	const key = contextualClass + '|' + strongMessage + '|' + message;
	const existing = [...stack.children].find(toast => toast.dataset.toastKey === key);
	if (existing) {
		startToastTimer(existing);
		return existing;
	}

	const toast = document.createElement('div');
	toast.className = 'alert toast-note d-flex align-items-start gap-2 fade ' + contextualClass;
	if (contextualClass === 'alert-success') {
		playSound('done');
	}
	toast.setAttribute('role', contextualClass === 'alert-danger' ? 'alert' : 'status');
	toast.dataset.toastKey = key;
	toast.toastDuration = contextualClass === 'alert-danger' ? TOAST_ERROR_MS : TOAST_SUCCESS_MS;

	const icon = document.createElement('span');
	icon.classList.add('bi', iconClass, 'flex-shrink-0', 'mt-1');
	icon.setAttribute('aria-hidden', 'true');
	toast.appendChild(icon);

	const text = document.createElement('div');
	text.className = 'flex-grow-1';
	if (strongMessage) {
		const strong = document.createElement('strong');
		strong.textContent = strongMessage;
		text.appendChild(strong);
		text.appendChild(document.createTextNode(' '));
	}
	text.appendChild(document.createTextNode(message));
	toast.appendChild(text);

	const close = document.createElement('button');
	close.type = 'button';
	close.className = 'btn-close flex-shrink-0';
	close.setAttribute('aria-label', t('Close'));
	close.addEventListener('click', () => closeToast(toast));
	toast.appendChild(close);

	toast.addEventListener('mouseenter', () => clearTimeout(toast.toastTimer));
	toast.addEventListener('mouseleave', () => startToastTimer(toast));
	toast.addEventListener('focusin', () => clearTimeout(toast.toastTimer));
	toast.addEventListener('focusout', () => startToastTimer(toast));

	stack.appendChild(toast);
	while (stack.children.length > MAX_TOASTS) {
		closeToast(stack.firstElementChild);
		stack.firstElementChild.remove();
	}
	// reading the layout first makes the fade-in run, also in a tab in the background
	void toast.offsetWidth;
	toast.classList.add('show');
	startToastTimer(toast);
	return toast;
}

// the messages the server put in the page (after saving a form) become notifications
function initFlashMessages() {
	document.querySelectorAll('[data-flash]').forEach(alert => {
		const danger = alert.classList.contains('alert-danger');
		showToast(danger ? 'alert-danger' : 'alert-success', danger ? 'bi-exclamation-triangle-fill' : 'bi-check-circle-fill', '', alert.textContent.trim());
		alert.remove();
	});
}

function insertAlert(element, contextualClass, iconClass, strongMessage, message, dismissible = true, id = "") {
	// alerts with an id stay in the page and are updated (the synchronization progress)
	if (id == "") {
		showToast(contextualClass, iconClass, strongMessage, message);
		return;
	}
	const alert = document.createElement('div');
	alert.classList.add('alert', contextualClass, 'd-flex', 'align-items-center', 'fade', 'show');
	if (dismissible) {
		alert.classList.add('alert-dismissible');
	}
	alert.setAttribute('role', 'alert');
	if (id != "") {
		alert.id = id;
	}

	const icon = document.createElement('span');
	icon.classList.add('bi', iconClass, 'flex-shrink-0', 'me-2');
	alert.appendChild(icon);

	const fullMessageWrapper = document.createElement('div');

	if (strongMessage) {
		const strongMessageWrapper = document.createElement('strong');
		strongMessageWrapper.appendChild(document.createTextNode(strongMessage));
		fullMessageWrapper.appendChild(strongMessageWrapper);
		fullMessageWrapper.appendChild(document.createTextNode(' '));
	}

	fullMessageWrapper.appendChild(document.createTextNode(message));
	alert.appendChild(fullMessageWrapper);

	if (dismissible) {
		const closeBtn = document.createElement('button');
		closeBtn.setAttribute('aria-label', t('Close'));
		closeBtn.setAttribute('type', 'button');
		closeBtn.classList.add('btn-close');
		closeBtn.dataset.bsDismiss = 'alert';
		alert.appendChild(closeBtn);
	}

	element.insertBefore(alert, element.firstChild);
	if (element.tagName === 'FORM') {
		// long forms: bring the feedback into view
		alert.scrollIntoView({ behavior: 'smooth', block: 'center' });
	}
}

const SYNC_ALERT_ID = 'alert_sync';
const SYNC_POLL_INTERVAL = 1000;

// the tasks of the server, newest first
function fetchTasks() {
	return fetch('/api/tasks', { cache: 'no-store', headers: { Accept: 'application/json' } })
		.then(response => (response.ok ? response.json() : []));
}

// runAsTask starts work that runs as tasks and stays on the page: a note says it is running,
// with a link to Tasks, and the page reloads with the new data when the tasks it started end.
// start returns the promise of the request; its result is passed on.
function runAsTask(start) {
	return fetchTasks().catch(() => []).then(tasks => {
		const known = Math.max(0, ...tasks.map(task => task.id));
		return start().then(result => {
			// a request that failed started nothing
			if (!(result instanceof Response) || result.ok) {
				followTasks(known);
			}
			return result;
		});
	});
}

const TASK_NOTE_ID = 'taskRunning';

function followTasks(known) {
	let note = document.getElementById(TASK_NOTE_ID);
	if (!note) {
		note = document.createElement('div');
		note.id = TASK_NOTE_ID;
		note.className = 'alert alert-info d-flex flex-wrap align-items-center gap-2 mt-3';
		note.setAttribute('role', 'status');
		const spinner = document.createElement('span');
		spinner.className = 'spinner-border spinner-border-sm flex-shrink-0';
		spinner.setAttribute('aria-hidden', 'true');
		const text = document.createElement('div');
		text.className = 'flex-grow-1 min-w-0';
		const strong = document.createElement('strong');
		strong.textContent = t('Running.');
		const message = document.createElement('span');
		message.dataset.taskMessage = '';
		message.textContent = t('It goes on in the background; this page updates by itself when it ends.');
		text.append(strong, ' ', message);
		const link = document.createElement('a');
		link.className = 'alert-link text-nowrap';
		link.href = '/tasks.html';
		link.textContent = t('See it in Tasks');
		note.append(spinner, text, link);
		mainContainer().prepend(note);
	}
	const message = note.querySelector('[data-task-message]');
	let waited = 0;
	const poll = () => fetchTasks().then(tasks => {
		const started = tasks.filter(task => task.id > known);
		const running = started.filter(task => task.status === 'running');
		if (running.length > 0) {
			const task = running[0];
			const text = task.messageArg ? t(task.message, task.messageArg) : t(task.message || '');
			const percent = task.total > 0 && task.current >= 0 ? ` · ${Math.min(100, Math.round(task.current * 100 / task.total))}%` : '';
			if (text) {
				message.textContent = text + percent;
			}
		} else if (started.length > 0) {
			window.location.reload();
			return;
		} else if ((waited += 1) > 10) {
			// nothing was started, e.g. nothing to do
			note.remove();
			return;
		}
		setTimeout(poll, 2000);
	}).catch(() => setTimeout(poll, 4000));
	setTimeout(poll, 1000);
}

function mainContainer() {
	return document.querySelector('main > .container-xxl');
}

// the synchronize button spins while a synchronization is running
function setSyncBusy(busy) {
	const sync = document.getElementById('sync');
	if (!sync) {
		return;
	}
	sync.classList.toggle('is-syncing', busy);
	sync.setAttribute('aria-disabled', busy ? 'true' : 'false');
	if (busy) {
		sync.setAttribute('aria-busy', 'true');
	} else {
		sync.removeAttribute('aria-busy');
	}
}

function showSyncAlert() {
	setSyncBusy(true);
	if (document.getElementById(SYNC_ALERT_ID)) {
		return;
	}

	// same markup as the alert rendered by layout.html
	const alert = document.createElement('div');
	alert.classList.add('alert');
	alert.setAttribute('role', 'status');
	alert.id = SYNC_ALERT_ID;

	const header = document.createElement('div');
	header.classList.add('d-flex', 'align-items-center');
	const spinner = document.createElement('span');
	spinner.classList.add('spinner-border', 'spinner-border-sm', 'text-primary', 'flex-shrink-0', 'me-2');
	spinner.setAttribute('aria-hidden', 'true');
	const text = document.createElement('div');
	text.classList.add('text-truncate');
	const strong = document.createElement('strong');
	strong.textContent = t('Synchronizing!');
	const message = document.createElement('span');
	message.dataset.syncMessage = '';
	message.textContent = t('The library is being updated.');
	text.append(strong, ' ', message);
	header.append(spinner, text);

	const progress = document.createElement('div');
	progress.classList.add('progress', 'mt-2');
	progress.setAttribute('role', 'progressbar');
	progress.setAttribute('aria-label', t('Synchronization progress'));
	const bar = document.createElement('div');
	bar.classList.add('progress-bar', 'progress-bar-striped', 'progress-bar-animated', 'w-100');
	progress.appendChild(bar);

	alert.append(header, progress);
	mainContainer().appendChild(alert);
}

function updateSyncAlert(status) {
	const alert = document.getElementById(SYNC_ALERT_ID);
	if (!alert) {
		return;
	}

	const message = alert.querySelector('[data-sync-message]');
	if (message && status.message) {
		message.textContent = status.message;
	}

	const progress = alert.querySelector('.progress');
	const bar = alert.querySelector('.progress-bar');
	if (!progress || !bar) {
		return;
	}

	if (status.total > 0) {
		const percent = Math.min(100, Math.round(status.current * 100 / status.total));
		bar.classList.remove('w-100', 'progress-bar-animated');
		bar.style.width = `${percent}%`;
		bar.textContent = `${percent}%`;
		progress.setAttribute('aria-valuenow', percent);
		progress.setAttribute('aria-valuemin', 0);
		progress.setAttribute('aria-valuemax', 100);
	} else {
		// unknown number of steps
		bar.classList.add('w-100', 'progress-bar-animated');
		bar.style.width = '';
		bar.textContent = '';
		progress.removeAttribute('aria-valuenow');
	}
}

function onSyncFinished() {
	setSyncBusy(false);
	const syncAlert = document.getElementById(SYNC_ALERT_ID);
	if (syncAlert) {
		syncAlert.remove();
	}

	if (document.querySelector('form[method="post"]:not([action="/logout"])')) {
		// do not reload pages with forms, it could discard unsaved changes
		insertAlert(mainContainer(), 'alert-success', 'bi-check-circle-fill', t('Done!'), t('The library has been updated.'));
	} else {
		window.location.reload();
	}
}

function watchSync() {
	setTimeout(() => {
		fetch('/sync', { method: 'GET', cache: 'no-store' })
			.then(response => {
				// the session ended: the page is opened again, through the login
				if (response.status === 401 || response.status === 403) {
					window.location.reload();
					return new Promise(() => {});
				}
				return response.json();
			})
			.then(status => {
				if (status.synchronizing) {
					updateSyncAlert(status);
					watchSync();
				} else {
					onSyncFinished();
				}
			})
			// the server may be restarting, keep trying
			.catch(() => watchSync());
	}, SYNC_POLL_INTERVAL);
}

function startSync(url) {
	if (document.getElementById(SYNC_ALERT_ID)) {
		return;
	}
	setSyncBusy(true);
	fetch(url, { method: 'POST' }).then(response => {
		if (!response.ok) {
			throw new Error(response.statusText);
		}
		showSyncAlert();
		watchSync();
	}).catch(() => {
		setSyncBusy(false);
		insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), t('Synchronization could not be started.'));
	});
}

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

// operationKind tells the kind of a change: a move within its folder renames, and a move
// that changes the title ID of the name needs a look
function operationKind(op) {
	if (op.error) {
		return 'error';
	}
	if (op.kind !== 'move' || !op.from || !op.to) {
		return op.kind;
	}
	const [fromFolder, fromName] = splitPath(op.from);
	const [toFolder, toName] = splitPath(op.to);
	const fromId = nameId(fromName);
	const toId = nameId(toName);
	if (fromId && toId && fromId.toUpperCase() !== toId.toUpperCase()) {
		return 'review';
	}
	return fromFolder === toFolder ? 'rename' : 'move';
}

// a file as its name, with its folder below
function pathCell(path, className) {
	const [folder, name] = splitPath(path || '');
	const cell = document.createElement('div');
	cell.className = `op-path ${className || ''}`;
	const strong = document.createElement('div');
	strong.className = 'op-name';
	strong.textContent = name;
	cell.append(strong);
	if (folder) {
		const small = document.createElement('div');
		small.className = 'op-folder';
		small.innerHTML = '<span class="bi bi-folder2 me-1" aria-hidden="true"></span>';
		small.append(folder);
		cell.append(small);
	}
	return cell;
}

function renderOperations(result) {
	const list = document.getElementById('organizeOperations');
	const summary = document.getElementById('organizeSummary');
	list.replaceChildren();
	summary.replaceChildren();
	const kinds = Object.fromEntries(OPERATION_KINDS.map(kind => [kind.key, kind]));
	const counts = {};

	result.operations.forEach(op => {
		const key = operationKind(op);
		const kind = kinds[key] || { key, label: OPERATION_LABELS[op.kind] || op.kind, icon: 'bi-dot' };
		counts[key] = (counts[key] || 0) + 1;

		const row = document.createElement('li');
		row.className = `op-row op-${key}`;
		row.dataset.opKind = key;

		const badge = document.createElement('span');
		badge.className = 'op-badge';
		badge.innerHTML = `<span class="bi ${kind.icon}" aria-hidden="true"></span>`;
		badge.append(kind.label);

		const body = document.createElement('div');
		body.className = 'op-body';
		if (op.kind === 'cleanup') {
			// the folders that are left empty, found when the changes are applied
			const note = document.createElement('div');
			note.className = 'op-name';
			note.textContent = op.reason || '';
			body.append(note);
		} else if (op.kind === 'mkdir') {
			body.append(pathCell(op.to || op.from));
		} else {
			body.append(pathCell(op.from || op.to));
		}
		if ((key === 'move' || key === 'rename' || key === 'review') && op.to) {
			const [fromFolder, fromName] = splitPath(op.from);
			const [toFolder, toName] = splitPath(op.to);
			const target = document.createElement('div');
			target.className = 'op-target';
			target.innerHTML = '<span class="bi bi-arrow-return-right" aria-hidden="true"></span>';
			const name = document.createElement('div');
			name.className = 'op-path';
			const strong = document.createElement('div');
			strong.className = 'op-name';
			strong.textContent = toName === fromName ? t('Same name') : toName;
			name.append(strong);
			if (toFolder !== fromFolder && toFolder) {
				const small = document.createElement('div');
				small.className = 'op-folder';
				small.innerHTML = '<span class="bi bi-folder2-open me-1" aria-hidden="true"></span>';
				small.append(toFolder);
				name.append(small);
			}
			target.append(name);
			body.append(target);
			const notes = [];
			if (key === 'review') {
				notes.push(t('The title ID of the name changes (%v → %v): the file holds several contents or had a wrong name. Check it before applying.', nameId(fromName), nameId(toName)));
			} else if (nameVersion(fromName) && nameVersion(toName) && nameVersion(fromName) !== nameVersion(toName)) {
				notes.push(t('The version of the name changes: v%v → v%v (the update the file holds).', nameVersion(fromName), nameVersion(toName)));
			}
			notes.forEach(text => {
				const note = document.createElement('div');
				note.className = 'op-note';
				note.textContent = text;
				body.append(note);
			});
		} else if (op.kind !== 'cleanup' && (op.error || op.reason)) {
			const note = document.createElement('div');
			note.className = 'op-note';
			note.textContent = op.error ? t('Error: %v', op.error) : op.reason;
			body.append(note);
		}

		row.append(badge, body);
		list.appendChild(row);
	});

	// the summary filters the list by kind
	const chips = [];
	const filter = key => {
		chips.forEach(chip => chip.setAttribute('aria-pressed', String(chip.dataset.opFilter === key)));
		list.querySelectorAll('.op-row').forEach(row => {
			row.hidden = key !== 'all' && row.dataset.opKind !== key;
		});
	};
	const chip = (key, label, icon, count) => {
		const button = document.createElement('button');
		button.type = 'button';
		button.className = `op-chip op-${key}`;
		button.dataset.opFilter = key;
		button.setAttribute('aria-pressed', String(key === 'all'));
		button.innerHTML = `<span class="bi ${icon}" aria-hidden="true"></span>`;
		button.append(label);
		const number = document.createElement('span');
		number.className = 'op-chip-count';
		number.textContent = count;
		button.append(number);
		button.addEventListener('click', () => filter(key));
		chips.push(button);
		summary.append(button);
	};
	if (result.operations.length > 0) {
		chip('all', t('All'), 'bi-list-ul', result.operations.length);
		OPERATION_KINDS.forEach(kind => {
			if (counts[kind.key]) {
				chip(kind.key, kind.label, kind.icon, counts[kind.key]);
			}
		});
	}
	summary.hidden = result.operations.length === 0;
}

function initOrganize() {
	const result = document.getElementById('organizeResult');
	if (!result) {
		return;
	}

	const title = document.getElementById('organizeResultTitle');
	const runButton = document.getElementById('organizeRun');
	const actionButtons = document.querySelectorAll('[data-organize-action]');
	let currentAction = null;

	const setBusy = busy => {
		actionButtons.forEach(button => button.disabled = busy);
		runButton.disabled = busy;
	};

	const verifyNote = document.querySelector('[data-organize-verify]');
	const show = (response, action) => {
		result.classList.remove('d-none');
		renderOperations(response);
		if (verifyNote) {
			verifyNote.hidden = !response.needsVerify;
			verifyNote.classList.toggle('d-none', !response.needsVerify);
			const text = verifyNote.querySelector('[data-organize-verify-text]');
			text.textContent = text.dataset.text.replace('%v', response.needsVerify || 0);
		}

		if (response.dryRun) {
			title.textContent = response.changes === 0 ? t('Preview: nothing to change') : t('Preview: %v change(s)', response.changes) + (response.errors ? t(', %v problem(s)', response.errors) : '');
			currentAction = action;
			runButton.textContent = t('Apply %v change(s)', response.changes);
			runButton.classList.toggle('d-none', response.changes === 0);
		} else {
			title.textContent = t('Done: %v change(s)', response.changes) + (response.errors ? t(', %v failed', response.errors) : '');
			runButton.classList.add('d-none');
			currentAction = null;
			showSyncAlert();
			watchSync();
		}
		result.scrollIntoView({ behavior: 'smooth', block: 'start' });
	};

	const fail = error => {
		insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), error.message);
	};

	actionButtons.forEach(button => button.addEventListener('click', () => {
		setBusy(true);
		postForm('/organize/preview', { action: button.dataset.organizeAction })
			.then(response => show(response, button.dataset.organizeAction))
			.catch(fail)
			.finally(() => setBusy(false));
	}));

	// while the changes are applied, the progress of the task: the file or game it is on and
	// how many are done
	const progress = document.getElementById('organizeProgress');
	const bar = progress.querySelector('[data-organize-bar]');
	const message = progress.querySelector('[data-organize-message]');
	const count = progress.querySelector('[data-organize-count]');
	let polling = null;
	const follow = () => fetch('/api/tasks', { cache: 'no-store', headers: { Accept: 'application/json' } })
		.then(response => (response.ok ? response.json() : []))
		.then(tasks => {
			const task = tasks.find(candidate => candidate.status === 'running' && (candidate.kind === 'organize' || candidate.kind === 'cleanup'));
			if (!task || task.total <= 0 || task.current < 0) {
				return;
			}
			const percent = Math.min(100, Math.round(task.current * 100 / task.total));
			bar.classList.remove('w-100');
			bar.style.width = `${percent}%`;
			bar.style.setProperty('--pct', percent);
			message.textContent = task.messageArg ? t(task.message, task.messageArg) : t(task.message);
			count.textContent = `${task.current} / ${task.total} \u00b7 ${percent}%`;
		})
		.catch(() => {});
	const startProgress = () => {
		bar.classList.add('progress-bar-striped', 'progress-bar-animated', 'w-100');
		bar.style.width = '';
		message.textContent = '';
		count.textContent = '';
		progress.hidden = false;
		result.classList.add('d-none');
		progress.scrollIntoView({ behavior: 'smooth', block: 'start' });
		polling = setInterval(follow, 700);
	};
	const stopProgress = () => {
		clearInterval(polling);
		progress.hidden = true;
		result.classList.remove('d-none');
	};

	runButton.addEventListener('click', () => {
		if (!currentAction || !window.confirm(t('Apply the changes shown in the preview? Files will be moved or deleted.'))) {
			return;
		}
		setBusy(true);
		startProgress();
		postForm('/organize/run', { action: currentAction })
			.then(response => {
				stopProgress();
				show(response, currentAction);
			})
			.catch(error => {
				stopProgress();
				fail(error);
			})
			.finally(() => setBusy(false));
	});
}

function initIgnoreButtons() {
	document.querySelectorAll('[data-ignore-kind]').forEach(button => button.addEventListener('click', e => {
		e.preventDefault();
		e.stopPropagation();
		button.disabled = true;
		postForm('/ignore', {
			kind: button.dataset.ignoreKind,
			id: button.dataset.ignoreId,
			ignored: button.dataset.ignored
		}).then(() => {
			// the lists and counters are computed on the server
			window.location.reload();
		}).catch(error => {
			button.disabled = false;
			insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), error.message);
		});
	}));
}

function initNotificationTest() {
	const button = document.getElementById('notificationTest');
	if (!button) {
		return;
	}
	button.addEventListener('click', () => {
		const form = document.getElementById('settingsForm');
		button.disabled = true;
		postForm('/notifications/test', {}).then(response => {
			insertAlert(form, 'alert-success', 'bi-check-circle-fill', response.strongMessage, response.message);
		}).catch(error => {
			insertAlert(form, 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), error.message);
		}).finally(() => {
			button.disabled = false;
		});
	});
}

const THEME_KEY = 'slm-theme';

function storedTheme() {
	try {
		return localStorage.getItem(THEME_KEY) || 'dark';
	} catch (e) {
		return 'dark';
	}
}

function applyTheme(theme) {
	const dark = theme === 'dark' || (theme === 'auto' && window.matchMedia('(prefers-color-scheme: dark)').matches);
	document.documentElement.setAttribute('data-bs-theme', dark ? 'dark' : 'light');
	document.querySelectorAll('[data-theme-value]').forEach(item => {
		const active = item.dataset.themeValue === theme;
		item.classList.toggle('active', active);
		item.setAttribute('aria-pressed', active);
	});
}

function initThemeSwitcher() {
	applyTheme(storedTheme());
	document.querySelectorAll('[data-theme-value]').forEach(item => item.addEventListener('click', () => {
		try {
			localStorage.setItem(THEME_KEY, item.dataset.themeValue);
		} catch (e) {
			// the theme is still applied to this page
		}
		applyTheme(item.dataset.themeValue);
	}));
	// follow the system when "automatic" is selected
	window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
		if (storedTheme() === 'auto') {
			applyTheme('auto');
		}
	});
}

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

// replace covers that cannot be loaded (e.g. the Nintendo servers are not reachable)
document.addEventListener('error', e => {
	const image = e.target;
	if (image.tagName === 'IMG' && !image.dataset.fallback && !image.classList.contains('title-screenshot')) {
		image.dataset.fallback = 'true';
		image.src = '/resources/static/noimage.png';
	}
}, true);

// the Tasks page follows the tasks live: the server sends an event after every change
// and the list is rendered again by the server
// The Tasks link of the menu turns while a task runs and shows the failed ones. The page draws
// it once, so it follows the tasks live: on the Tasks page with its list, elsewhere only while
// a task runs, until the last one ends (also when it is cancelled).
function taskLinks() {
	return [...document.querySelectorAll('.app-nav a.nav-link[href="/tasks.html"]')];
}

function updateTaskIndicator() {
	if (taskLinks().length === 0) {
		return Promise.resolve(0);
	}
	return fetch('/api/tasks', { cache: 'no-store', headers: { Accept: 'application/json' } })
		.then(response => (response.ok ? response.json() : Promise.reject(response)))
		.then(tasks => {
			const active = tasks.filter(task => task.status === 'running');
			const running = active.length;
			const failed = tasks.filter(task => task.status === 'failed').length;
			// the progress of the first task that knows it, for the ring around the icon
			const measured = active.find(task => task.total > 0 && task.current >= 0);
			const percent = measured ? Math.min(100, Math.round(measured.current * 100 / measured.total)) : -1;
			taskLinks().forEach(link => {
				const icon = link.querySelector('.bi');
				if (icon) {
					icon.classList.toggle('bi-fan', running > 0);
					icon.classList.toggle('nav-busy', running > 0);
					icon.classList.toggle('bi-list-task', running === 0);
				}
				link.classList.toggle('is-working', running > 0);
				link.classList.toggle('is-measured', percent >= 0);
				link.style.setProperty('--task-pct', String(Math.max(percent, 0)));
				let badge = link.querySelector('.nav-count');
				if (failed > 0) {
					if (!badge) {
						badge = document.createElement('span');
						badge.className = 'badge rounded-pill nav-count text-bg-danger ms-auto ms-xxl-0';
						link.appendChild(badge);
					}
					badge.textContent = String(failed);
				} else if (badge) {
					badge.remove();
				}
			});
			return running;
		})
		.catch(() => -1);
}

function initTaskIndicator() {
	// the Tasks page updates it with its list (initLiveTasks)
	if (document.querySelector('[data-live-tasks]') || !window.EventSource || taskLinks().length === 0) {
		return;
	}
	// always followed: a task started after the page was drawn (a button of the page, a
	// synchronization, a scheduled or automatic one) shows too. The events come with every
	// change of a task, several a second during a compression: the icon follows them at most
	// every second and a half.
	let waiting = null;
	let again = false;
	const update = () => {
		if (waiting) {
			again = true;
			return;
		}
		updateTaskIndicator();
		waiting = setTimeout(() => {
			waiting = null;
			if (again) {
				again = false;
				update();
			}
		}, 1500);
	};
	let source = null;
	const open = () => {
		source = new EventSource('/api/tasks/events');
		source.addEventListener('tasks', update);
	};
	if (taskLinks().some(link => link.querySelector('.nav-busy'))) {
		update();
	}
	open();
	window.addEventListener('pagehide', () => source.close());
	// back to the page from the history of the browser: the connection was closed on leaving
	window.addEventListener('pageshow', e => {
		if (e.persisted) {
			open();
		}
	});
}

// keepAnimationsInStep makes the spinners and striped bars just drawn go on where the ones
// they replace were, as they all follow the clock of the page, instead of starting again
function keepAnimationsInStep(root) {
	const now = document.timeline ? document.timeline.currentTime : performance.now();
	root.querySelectorAll('.spinner-border').forEach(element => {
		const duration = parseFloat(getComputedStyle(element).animationDuration) * 1000;
		if (duration > 0) {
			element.style.animationDelay = `-${now % duration}ms`;
		}
	});
	// the stripes move on a layer of their own, one second per stripe
	root.querySelectorAll('.progress-bar-animated').forEach(element => {
		element.style.setProperty('--stripe-delay', `-${now % 1000}ms`);
	});
}

function initLiveTasks() {
	const list = document.querySelector('[data-live-tasks]');
	if (!list) {
		return;
	}

	keepAnimationsInStep(list);
	let rendered = Number(list.dataset.version || 0);
	let loading = false;
	let pending = false;
	// while a list of files is being scrolled (the bar dragged, a finger sliding) it is not
	// drawn again: the new list would end the gesture every second
	let scrolledAt = 0;
	let holding = false;
	let waiting = null;
	// the scroll the page puts back after drawing the list is not the user's
	let restoredAt = 0;
	list.addEventListener('scroll', () => {
		if (Date.now() - restoredAt > 250) {
			scrolledAt = Date.now();
		}
	}, true);
	list.addEventListener('pointerdown', e => {
		holding = Boolean(e.target.closest('details[data-task-results]'));
	});
	list.addEventListener('touchstart', e => {
		holding = Boolean(e.target.closest('details[data-task-results]'));
	}, { passive: true });
	['pointerup', 'pointercancel', 'touchend', 'touchcancel'].forEach(name => window.addEventListener(name, () => {
		holding = false;
	}, { passive: true }));
	const refresh = () => {
		if (loading) {
			pending = true;
			return;
		}
		if (holding || Date.now() - scrolledAt < 1500) {
			if (!waiting) {
				waiting = setTimeout(() => {
					waiting = null;
					refresh();
				}, 700);
			}
			return;
		}
		loading = true;
		fetch('/tasks.html?part=list', { cache: 'no-store' })
			.then(response => response.ok ? response.text() : Promise.reject(response))
			.then(html => {
				// every bar (the total and the one of each file) goes on from where it was
				// instead of jumping: by task, the total first, then the files by name
				const barKey = (card, bar) => card.dataset.taskId + '|' + (bar.closest('.task-lane')?.querySelector('[title]')?.title || '');
				const widths = {};
				list.querySelectorAll('[data-task-id]').forEach(card => {
					card.querySelectorAll('.progress-bar').forEach(bar => {
						widths[barKey(card, bar)] = bar.style.width;
					});
				});
				// a list of files opened or folded stays as it was
				// the bars of what a compression saved grow once, when they appear
				const shown = new Set([...list.querySelectorAll('[data-task-saving]')].map(saving => saving.dataset.taskSaving));
				const folded = {};
				// and so does where a long list was scrolled to
				const scrolled = {};
				const scroller = details => [...details.querySelectorAll('*')].find(element => element.scrollHeight > element.clientHeight + 1 && getComputedStyle(element).overflowY !== 'visible');
				list.querySelectorAll('details[data-task-results]').forEach(details => {
					folded[details.dataset.taskResults] = details.open;
					const box = scroller(details);
					if (box && box.scrollTop > 0) {
						scrolled[details.dataset.taskResults] = box.scrollTop;
					}
				});
				list.innerHTML = html;
				keepAnimationsInStep(list);
				list.querySelectorAll('[data-task-saving]').forEach(saving => {
					if (shown.has(saving.dataset.taskSaving)) {
						saving.closest('li, article').querySelectorAll('.saving-bar .progress').forEach(part => {
							part.style.animation = 'none';
						});
					}
				});
				list.querySelectorAll('details[data-task-results]').forEach(details => {
					if (details.dataset.taskResults in folded) {
						details.open = folded[details.dataset.taskResults];
					}
					if (details.dataset.taskResults in scrolled) {
						const box = scroller(details);
						if (box) {
							restoredAt = Date.now();
							box.scrollTop = scrolled[details.dataset.taskResults];
						}
					}
				});
				updateTaskIndicator();
				list.querySelectorAll('[data-task-id]').forEach(card => {
					card.querySelectorAll('.progress-bar').forEach(bar => {
						const before = widths[barKey(card, bar)];
						if (!before || before === bar.style.width) {
							return;
						}
						const after = bar.style.width;
						bar.style.transition = 'none';
						bar.style.width = before;
						void bar.offsetWidth;
						bar.style.transition = '';
						bar.style.width = after;
					});
				});
			})
			.catch(() => {
				// the next event tries again
			})
			.finally(() => {
				loading = false;
				if (pending) {
					pending = false;
					refresh();
				}
			});
	};

	if (window.EventSource) {
		let source = null;
		const open = () => {
			source = new EventSource('/api/tasks/events');
			source.addEventListener('tasks', e => {
				const version = Number(e.data);
				if (version !== rendered) {
					rendered = version;
					refresh();
				}
			});
		};
		open();
		window.addEventListener('pagehide', () => source.close());
		// back to the page from the history of the browser: the connection was closed on
		// leaving, and the tasks went on meanwhile
		window.addEventListener('pageshow', e => {
			if (e.persisted) {
				open();
				refresh();
			}
		});
	} else {
		setInterval(refresh, 3000);
	}

	list.addEventListener('click', e => {
		const dismiss = e.target.closest('[data-task-dismiss]');
		const clear = e.target.closest('[data-task-clear]');
		if (!dismiss && !clear) {
			return;
		}
		const button = dismiss || clear;
		button.disabled = true;
		// a group of tasks shown once is dismissed together
		const urls = dismiss ? dismiss.dataset.taskDismiss.split(',').map(id => `/api/tasks/${encodeURIComponent(id)}/dismiss`) : ['/api/tasks/clear'];
		Promise.all(urls.map(url => fetch(url, { method: 'POST' })))
			.then(refresh)
			.catch(() => {
				button.disabled = false;
			});
	});
}

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
		const feedback = form.querySelector('.alert');
		if (feedback) {
			feedback.remove();
		}
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

const reducedMotion = () => window.matchMedia('(prefers-reduced-motion: reduce)').matches;

// numbers count up from zero when the page opens
function initCountUp() {
	if (reducedMotion()) {
		return;
	}
	document.querySelectorAll('[data-count]').forEach(element => {
		const target = Number(element.dataset.count);
		if (!Number.isFinite(target) || target <= 0) {
			return;
		}
		const duration = 700;
		const start = performance.now();
		const format = new Intl.NumberFormat(document.documentElement.lang || undefined);
		const step = now => {
			const progress = Math.min(1, (now - start) / duration);
			// ease out
			const value = Math.round(target * (1 - Math.pow(1 - progress, 3)));
			element.textContent = format.format(value);
			if (progress < 1) {
				requestAnimationFrame(step);
			}
		};
		element.textContent = '0';
		requestAnimationFrame(step);
		// hidden tabs pause animation frames: the right number is always shown in the end
		setTimeout(() => {
			element.textContent = format.format(target);
		}, duration + 100);
	});
}

// covers fade in once loaded, with a shimmer meanwhile
function initCoverLoading() {
	document.querySelectorAll('.game-cover img').forEach(image => {
		if (image.complete) {
			return;
		}
		image.classList.add('is-loading');
		const done = () => image.classList.remove('is-loading');
		image.addEventListener('load', done, { once: true });
		image.addEventListener('error', done, { once: true });
	});
}

// The star of the favorites and the heart of the wishlist change at once, with a little pop,
// and the server is told; if it fails they go back. The buttons with a label (the game page)
// reload the page, which shows the change in more places.
const MARKS = {
	fav: { url: '/favorites', field: 'favorite', state: 'favorite', on: 'bi-star-fill', off: 'bi-star', active: 'is-favorite', add: 'Add to favorites', remove: 'Remove from favorites' },
	wish: { url: '/wishlist', field: 'wanted', state: 'wished', on: 'bi-heart-fill', off: 'bi-heart', active: 'is-wished', add: 'Add to the wishlist', remove: 'Remove from the wishlist' },
};

function setMark(button, mark, marked) {
	button.dataset[mark.state] = String(marked);
	button.setAttribute('aria-pressed', String(marked));
	button.classList.toggle(mark.active, marked);
	// the card of a favorite, or of a game on the wishlist, has its aura
	const card = button.closest('.game-card');
	if (card) {
		card.classList.toggle(mark.state === 'favorite' ? 'is-fav-card' : 'is-wish-card', marked);
	}
	button.title = t(marked ? mark.remove : mark.add);
	const icon = button.querySelector('.bi');
	if (icon) {
		icon.classList.toggle(mark.on, marked);
		icon.classList.toggle(mark.off, !marked);
	}
}

function initMarks() {
	Object.entries(MARKS).forEach(([key, mark]) => {
		document.querySelectorAll(`[data-${key}]`).forEach(button => {
			button.addEventListener('click', e => {
				e.preventDefault();
				e.stopPropagation();
				const id = button.dataset[key];
				const marked = button.dataset[mark.state] !== 'true';
				const labelled = button.textContent.trim() !== '' && !button.querySelector('.visually-hidden');
				if (labelled) {
					button.disabled = true;
					postForm(mark.url, { id, [mark.field]: String(marked) })
						.then(() => window.location.reload())
						.catch(() => {
							button.disabled = false;
						});
					return;
				}
				// every button of the same game on the page
				const buttons = [...document.querySelectorAll(`[data-${key}="${CSS.escape(id)}"]`)];
				buttons.forEach(other => setMark(other, mark, marked));
				playSound(marked ? 'on' : 'off');
				if (!reducedMotion()) {
					button.classList.remove('is-popping');
					void button.offsetWidth;
					button.classList.add('is-popping');
				}
				postForm(mark.url, { id, [mark.field]: String(marked) })
					.catch(() => buttons.forEach(other => setMark(other, mark, !marked)));
			});
		});
	});
}

// what is below the screen at the load waits until it scrolls into view: its animations (bars,
// rings, cards that come up) start then, instead of running unseen
function initRevealOnScroll() {
	if (!('IntersectionObserver' in window) || reducedMotion()) {
		return;
	}
	const below = window.innerHeight;
	const items = [...document.querySelectorAll('main .surface, main .saga-card')]
		.filter(element => !element.closest('.is-waiting') && element.getBoundingClientRect().top > below);
	if (!items.length) {
		return;
	}
	const observer = new IntersectionObserver(entries => {
		entries.forEach(entry => {
			if (entry.isIntersecting) {
				entry.target.classList.remove('is-waiting');
				observer.unobserve(entry.target);
			}
		});
	}, { rootMargin: '0px 0px -8% 0px' });
	items.forEach(element => {
		element.classList.add('is-waiting');
		observer.observe(element);
	});
}

// Short sounds of a console menu, made by the browser (no sound file): a tick to move, two
// notes up to open, two down to go back, a chime when a task ends. Off unless turned on, on
// each device, from the menu of the header.
const SOUND_KEY = 'slm-sounds';
const SOUNDS = {
	move: [[1320, 0, .045, 'triangle', .03]],
	open: [[660, 0, .07, 'triangle', .045], [990, .06, .09, 'triangle', .045]],
	back: [[880, 0, .07, 'triangle', .04], [587, .06, .09, 'triangle', .04]],
	on: [[784, 0, .06, 'sine', .05], [1175, .05, .09, 'sine', .05]],
	off: [[1175, 0, .06, 'sine', .04], [784, .05, .09, 'sine', .04]],
	done: [[784, 0, .12, 'sine', .045], [988, .1, .12, 'sine', .045], [1319, .2, .22, 'sine', .045]],
};
let soundContext = null;

function soundsOn() {
	try {
		return localStorage.getItem(SOUND_KEY) === '1';
	} catch (e) {
		return false;
	}
}

function playSound(name, force = false) {
	if (!force && !soundsOn()) {
		return;
	}
	try {
		soundContext = soundContext || new (window.AudioContext || window.webkitAudioContext)();
		if (soundContext.state === 'suspended') {
			soundContext.resume();
		}
		const now = soundContext.currentTime;
		SOUNDS[name].forEach(([frequency, start, length, type, volume]) => {
			const oscillator = soundContext.createOscillator();
			const gain = soundContext.createGain();
			oscillator.type = type;
			oscillator.frequency.setValueAtTime(frequency, now + start);
			gain.gain.setValueAtTime(.0001, now + start);
			gain.gain.exponentialRampToValueAtTime(volume, now + start + .01);
			gain.gain.exponentialRampToValueAtTime(.0001, now + start + length);
			oscillator.connect(gain).connect(soundContext.destination);
			oscillator.start(now + start);
			oscillator.stop(now + start + length + .02);
		});
	} catch (e) {
		// no sound in this browser
	}
}

function initSounds() {
	const toggles = [...document.querySelectorAll('[data-sound-toggle]')];
	const show = on => toggles.forEach(toggle => {
		toggle.setAttribute('aria-checked', String(on));
		const icon = toggle.querySelector('.bi');
		if (icon) {
			icon.classList.toggle('bi-volume-up', on);
			icon.classList.toggle('bi-volume-mute', !on);
		}
	});
	show(soundsOn());
	toggles.forEach(toggle => toggle.addEventListener('click', e => {
		// the menu stays open, to see the switch change
		e.stopPropagation();
		const on = !soundsOn();
		try {
			localStorage.setItem(SOUND_KEY, on ? '1' : '0');
		} catch (err) {
			// not kept on this device
		}
		show(on);
		playSound(on ? 'on' : 'off', true);
	}));
	// the keyboard moving from card to card ticks, like the controller
	document.addEventListener('focusin', e => {
		if (e.target.matches('.game-card .stretched-link:focus-visible')) {
			playSound('move');
		}
	});
	document.addEventListener('show.bs.modal', () => playSound('open'));
	document.addEventListener('hide.bs.modal', () => playSound('back'));
}

// The couch mode is the app for a TV, seen from afar and used with a controller: everything is
// bigger, the covers take the room, what is secondary goes and the page fills the screen. It is
// turned on from the menu of the header or with the Select button of a controller, and left
// with Select, Esc or the pill it shows.
const COUCH_KEY = 'slm-couch';

function couchOn() {
	return document.documentElement.classList.contains('is-couch');
}

function setCouch(on, fullscreen = true) {
	document.documentElement.classList.toggle('is-couch', on);
	try {
		localStorage.setItem(COUCH_KEY, on ? '1' : '0');
	} catch (e) {
		// only for this page
	}
	document.querySelectorAll('[data-couch-toggle]').forEach(toggle => toggle.setAttribute('aria-checked', String(on)));
	let pill = document.getElementById('couchPill');
	if (on && !pill) {
		pill = document.createElement('button');
		pill.type = 'button';
		pill.id = 'couchPill';
		pill.className = 'couch-pill';
		pill.innerHTML = '<span class="bi bi-tv" aria-hidden="true"></span>';
		pill.append(t('Couch mode: Select or Esc to leave'));
		// still being tried: it may change
		const testing = document.createElement('span');
		testing.className = 'chip chip-info';
		testing.textContent = t('In testing');
		pill.append(testing);
		pill.addEventListener('click', () => setCouch(false));
		document.body.append(pill);
	} else if (!on && pill) {
		pill.remove();
	}
	// the whole screen, when the browser lets it (it needs a click or a button of the controller)
	try {
		if (!fullscreen) {
			// back from another page: the screen stays as the browser left it
		} else if (on && !document.fullscreenElement && document.documentElement.requestFullscreen) {
			document.documentElement.requestFullscreen().catch(() => undefined);
		} else if (!on && document.fullscreenElement) {
			document.exitFullscreen().catch(() => undefined);
		}
	} catch (e) {
		// not in this browser
	}
}

function initCouchMode() {
	if (couchOn()) {
		// back from another page: the pill and the switches again
		setCouch(true, false);
	}
	document.querySelectorAll('[data-couch-toggle]').forEach(toggle => toggle.addEventListener('click', () => {
		playSound(couchOn() ? 'back' : 'open');
		setCouch(!couchOn());
	}));
	document.addEventListener('keydown', e => {
		if (e.key === 'Escape' && couchOn() && !document.querySelector('.modal.show, .offcanvas.show, .dropdown-menu.show')) {
			setCouch(false);
		}
	});
}

// A game controller moves through the page like on a console: the D-pad or the left stick go to
// the nearest control in that direction, A opens it, B closes a window or goes back, Start opens
// the quick search. The page is only read while a controller is connected and the page is seen.
const GAMEPAD_HINT_KEY = 'slm-gamepad-hint';
const PAD = { a: 0, b: 1, select: 8, start: 9, up: 12, down: 13, left: 14, right: 15 };

function gamepadTargets() {
	const scope = document.querySelector('.modal.show, .offcanvas.show') || document;
	return [...scope.querySelectorAll('a[href], button, input, select, textarea, [tabindex]')].filter(element => {
		if (element.disabled || element.tabIndex < 0 || element.closest('[hidden], [aria-hidden="true"], .d-none')) {
			return false;
		}
		const rect = element.getBoundingClientRect();
		return rect.width > 0 && rect.height > 0 && getComputedStyle(element).visibility !== 'hidden';
	});
}

// the box a control stands for: a game card for the link that covers it
function gamepadBox(element) {
	return (element.closest('.game-card, .saga-card') || element).getBoundingClientRect();
}

function gamepadMove(direction) {
	const targets = gamepadTargets();
	const current = document.activeElement;
	if (!targets.includes(current)) {
		// the first control in sight, or the first one of the page
		const inSight = targets.find(element => {
			const rect = gamepadBox(element);
			return rect.top >= 0 && rect.bottom <= window.innerHeight;
		});
		gamepadFocus(inSight || targets[0]);
		return;
	}
	const from = gamepadBox(current);
	const fromX = from.left + from.width / 2;
	const fromY = from.top + from.height / 2;
	const card = current.closest('.game-card, .saga-card');
	// the bars fixed at the bottom of the screen are only chosen when nothing else lies that way
	const bars = '.bottom-nav, .save-bar';
	const inBar = element => Boolean(element.closest(bars));
	const forward = direction === 'down' || direction === 'right';
	let best = null;
	let bestScore = Infinity;
	let bar = null;
	let barScore = Infinity;
	targets.forEach(element => {
		if (element === current || (card && card.contains(element))) {
			return;
		}
		// what comes after in the page for down and right, before for up and left: the sticky
		// bars move on the screen, the order of the page does not
		const following = Boolean(current.compareDocumentPosition(element) & Node.DOCUMENT_POSITION_FOLLOWING);
		if (following !== forward) {
			return;
		}
		const rect = gamepadBox(element);
		const x = rect.left + rect.width / 2;
		const y = rect.top + rect.height / 2;
		const dx = x - fromX;
		const dy = y - fromY;
		// only what lies in the direction asked, the nearest first and the most in line
		const along = { up: -dy, down: dy, left: -dx, right: dx }[direction];
		const across = direction === 'up' || direction === 'down' ? Math.abs(dx) : Math.abs(dy);
		if (along <= 1) {
			return;
		}
		const score = along + across * 2;
		if (inBar(element) && !inBar(current)) {
			if (score < barScore) {
				barScore = score;
				bar = element;
			}
		} else if (score < bestScore) {
			bestScore = score;
			best = element;
		}
	});
	best = best || bar;
	if (best) {
		// a card is entered by its link, not by the buttons drawn on it
		const target = best.closest('.game-card')?.querySelector('.stretched-link') || best;
		gamepadFocus(target);
		// a card ticks by itself when it takes the focus
		if (!target.closest('.game-card')) {
			playSound('move');
		}
	} else if (direction === 'up' || direction === 'down') {
		window.scrollBy({ top: (direction === 'up' ? -1 : 1) * window.innerHeight * .6, behavior: reducedMotion() ? 'auto' : 'smooth' });
	}
}

function gamepadFocus(element) {
	if (!element) {
		return;
	}
	element.focus({ preventScroll: true, focusVisible: true });
	(element.closest('.game-card, .saga-card') || element).scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: reducedMotion() ? 'auto' : 'smooth' });
}

function gamepadBack() {
	playSound('back');
	const open = document.querySelector('.modal.show, .offcanvas.show');
	if (open) {
		const close = open.querySelector('[data-bs-dismiss]');
		if (close) {
			close.click();
			return;
		}
	}
	if (history.length > 1) {
		history.back();
	}
}

function initGamepad() {
	if (!('getGamepads' in navigator)) {
		return;
	}
	let frame = 0;
	const held = {};
	const press = (name, pressed, action, now) => {
		const state = held[name] || (held[name] = { down: false, next: 0 });
		if (!pressed) {
			state.down = false;
			return;
		}
		// a held direction repeats, slowly first
		if (!state.down) {
			state.down = true;
			state.next = now + 380;
			action();
		} else if (now >= state.next && ['up', 'down', 'left', 'right'].includes(name)) {
			state.next = now + 130;
			action();
		}
	};
	const read = now => {
		frame = 0;
		const pad = [...navigator.getGamepads()].find(Boolean);
		if (!pad || document.hidden) {
			return;
		}
		const button = index => Boolean(pad.buttons[index] && pad.buttons[index].pressed);
		const [x = 0, y = 0] = pad.axes;
		const using = pad.buttons.some(b => b.pressed) || Math.abs(x) > .5 || Math.abs(y) > .5;
		if (using) {
			document.documentElement.classList.add('is-gamepad');
		}
		press('up', button(PAD.up) || y < -.5, () => gamepadMove('up'), now);
		press('down', button(PAD.down) || y > .5, () => gamepadMove('down'), now);
		press('left', button(PAD.left) || x < -.5, () => gamepadMove('left'), now);
		press('right', button(PAD.right) || x > .5, () => gamepadMove('right'), now);
		press('a', button(PAD.a), () => {
			if (document.activeElement && document.activeElement !== document.body) {
				playSound('open');
				document.activeElement.click();
			}
		}, now);
		press('b', button(PAD.b), gamepadBack, now);
		press('start', button(PAD.start), () => document.querySelector('.search-trigger')?.click(), now);
		press('select', button(PAD.select), () => {
			playSound(couchOn() ? 'back' : 'open');
			setCouch(!couchOn());
		}, now);
		frame = requestAnimationFrame(read);
	};
	const start = () => {
		if (!frame) {
			frame = requestAnimationFrame(read);
		}
	};
	window.addEventListener('gamepadconnected', () => {
		start();
		let shown = false;
		try {
			shown = localStorage.getItem(GAMEPAD_HINT_KEY) === '1';
			localStorage.setItem(GAMEPAD_HINT_KEY, '1');
		} catch (e) {
			// the hint shows again next time
		}
		if (!shown) {
			showToast('alert-info', 'bi-controller', t('Controller connected'), t('D-pad to move, A to open, B to go back, Start to search.'));
		}
	});
	document.addEventListener('visibilitychange', () => {
		if (!document.hidden && [...navigator.getGamepads()].some(Boolean)) {
			start();
		}
	});
	// the mouse or the keyboard take over again
	['pointerdown', 'keydown'].forEach(type => document.addEventListener(type, () => document.documentElement.classList.remove('is-gamepad'), { passive: true }));
	if ([...navigator.getGamepads()].some(Boolean)) {
		start();
	}
}

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

function initQuickSearch() {
	const element = document.getElementById('quickSearch');
	const input = document.getElementById('quickSearchInput');
	const list = document.getElementById('quickSearchResults');
	if (!element || !input || !list) {
		return;
	}
	const empty = element.querySelector('.quick-search-empty');
	const status = document.getElementById('quickSearchStatus');
	const modal = Modal.getOrCreateInstance(element);
	// the focus goes back where it was when the window closes (opened with Ctrl+K too)
	let opener = null;
	element.addEventListener('show.bs.modal', () => {
		opener = document.activeElement;
	});
	element.addEventListener('hidden.bs.modal', () => {
		if (opener && document.contains(opener) && typeof opener.focus === 'function') {
			opener.focus();
		}
		opener = null;
	});
	if (/Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent)) {
		document.querySelectorAll('[data-search-kbd]').forEach(kbd => {
			kbd.textContent = '⌘K';
		});
	}

	let options = [];
	let active = -1;
	let timer = 0;
	let controller = null;
	const cache = new Map();

	const setActive = index => {
		if (options.length === 0) {
			active = -1;
			input.removeAttribute('aria-activedescendant');
			return;
		}
		active = (index + options.length) % options.length;
		options.forEach((option, i) => {
			const selected = i === active;
			option.classList.toggle('active', selected);
			option.setAttribute('aria-selected', selected ? 'true' : 'false');
		});
		input.setAttribute('aria-activedescendant', options[active].id);
		options[active].scrollIntoView({ block: 'nearest' });
	};

	const option = (item, id) => {
		const link = document.createElement('a');
		link.className = 'quick-search-option';
		link.href = item.href;
		link.id = id;
		link.setAttribute('role', 'option');
		link.setAttribute('aria-selected', 'false');
		link.tabIndex = -1;
		const media = document.createElement('span');
		media.className = 'quick-search-media';
		media.setAttribute('aria-hidden', 'true');
		const icon = () => media.classList.add('bi', item.icon || 'bi-arrow-right');
		if (item.image) {
			media.classList.add('has-cover');
			const img = document.createElement('img');
			img.src = item.image;
			img.alt = '';
			img.decoding = 'async';
			img.addEventListener('error', () => {
				img.remove();
				media.classList.remove('has-cover');
				icon();
			}, { once: true });
			media.appendChild(img);
		} else {
			icon();
		}
		const text = document.createElement('span');
		text.className = 'quick-search-text';
		const label = document.createElement('span');
		label.className = 'quick-search-label';
		label.textContent = item.label;
		text.appendChild(label);
		if (item.meta) {
			const meta = document.createElement('span');
			meta.className = 'quick-search-meta';
			meta.textContent = item.meta;
			text.appendChild(meta);
		}
		const go = document.createElement('span');
		go.className = 'quick-search-go bi bi-arrow-return-left';
		go.setAttribute('aria-hidden', 'true');
		link.append(media, text, go);
		return link;
	};

	const render = (results, query) => {
		list.replaceChildren();
		const groups = query ? ['games', 'series', 'collections', 'pages'] : ['pages'];
		let count = 0;
		groups.forEach(group => {
			const items = results[group] || [];
			if (items.length === 0) {
				return;
			}
			const section = document.createElement('div');
			section.className = 'quick-search-group';
			section.setAttribute('role', 'group');
			const heading = document.createElement('div');
			heading.className = 'quick-search-heading';
			heading.id = `quickSearchGroup-${group}`;
			heading.textContent = list.dataset[`label${group[0].toUpperCase()}${group.slice(1)}`] || group;
			section.setAttribute('aria-labelledby', heading.id);
			section.appendChild(heading);
			items.forEach(item => section.appendChild(option(item, `quickSearchOption-${count++}`)));
			list.appendChild(section);
		});
		options = [...list.querySelectorAll('.quick-search-option')];
		if (empty) {
			empty.hidden = count > 0 || !query;
		}
		// read by screen readers once the typing stops
		if (status && query) {
			status.textContent = count === 0 ? status.dataset.none : count === 1 ? status.dataset.one : status.dataset.many.replace('%v', count);
		}
		setActive(0);
	};

	const search = value => {
		const query = value.trim();
		const key = query.toLowerCase();
		if (cache.has(key)) {
			render(cache.get(key), key);
			return;
		}
		if (controller) {
			controller.abort();
		}
		controller = new AbortController();
		fetch(`/api/search?q=${encodeURIComponent(query)}`, { signal: controller.signal, headers: { Accept: 'application/json' } })
			.then(response => (response.ok ? response.json() : Promise.reject(response.status)))
			.then(results => {
				if (cache.size > 50) {
					cache.clear();
				}
				cache.set(key, results);
				// only the latest search is shown
				if (input.value.trim().toLowerCase() === key) {
					render(results, key);
				}
			})
			.catch(() => {
				// replaced by a newer search, or the server could not be reached
			});
	};

	input.addEventListener('input', () => {
		clearTimeout(timer);
		timer = setTimeout(() => search(input.value), QUICK_SEARCH_DELAY);
	});
	input.addEventListener('keydown', e => {
		if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
			e.preventDefault();
			setActive(active + (e.key === 'ArrowDown' ? 1 : -1));
		} else if (e.key === 'Enter') {
			e.preventDefault();
			const chosen = options[active];
			if (!chosen) {
				return;
			}
			if (e.ctrlKey || e.metaKey) {
				window.open(chosen.href, '_blank', 'noopener');
			} else {
				window.location.href = chosen.href;
			}
		}
	});
	list.addEventListener('mousemove', e => {
		const index = options.indexOf(e.target.closest('.quick-search-option'));
		if (index >= 0 && index !== active) {
			setActive(index);
		}
	});

	element.addEventListener('show.bs.modal', () => search(input.value));
	element.addEventListener('shown.bs.modal', () => {
		input.focus();
		input.select();
	});

	const open = () => {
		// on phones the menu may be open: it closes first
		const menu = document.querySelector('.offcanvas.show [data-bs-dismiss="offcanvas"]');
		if (menu) {
			menu.click();
		}
		modal.show();
	};
	document.querySelectorAll('[data-search-open]').forEach(button => button.addEventListener('click', open));
	document.addEventListener('keydown', e => {
		if ((e.ctrlKey || e.metaKey) && !e.altKey && !e.shiftKey && e.key.toLowerCase() === 'k') {
			e.preventDefault();
			if (element.classList.contains('show')) {
				modal.hide();
			} else {
				open();
			}
			return;
		}
		const typing = e.target.closest && e.target.closest('input, textarea, select, [contenteditable="true"]');
		if (e.key === '/' && !typing && !e.ctrlKey && !e.metaKey && !e.altKey && !document.querySelector('.modal.show')) {
			e.preventDefault();
			open();
		}
	});
}

// Your year on Switch: the summary is drawn as an image (1080 x 1350, the size social networks
// show best) and saved, to share it. Only covers of the app itself are drawn, so the picture
// can always be saved.
function initYearReview() {
	const hero = document.querySelector('[data-year-review]');
	const button = document.querySelector('[data-year-download]');
	if (!hero || !button) {
		return;
	}
	const data = hero.dataset;
	const text = selector => (document.querySelector(selector)?.textContent || '').trim();

	const loadImage = src => new Promise(resolve => {
		const img = new Image();
		img.onload = () => resolve(img);
		img.onerror = () => resolve(null);
		img.src = src;
	});

	const roundRect = (ctx, x, y, w, h, r) => {
		ctx.beginPath();
		ctx.moveTo(x + r, y);
		ctx.arcTo(x + w, y, x + w, y + h, r);
		ctx.arcTo(x + w, y + h, x, y + h, r);
		ctx.arcTo(x, y + h, x, y, r);
		ctx.arcTo(x, y, x + w, y, r);
		ctx.closePath();
	};

	const fit = (ctx, value, maxWidth) => {
		let result = value;
		while (result.length > 1 && ctx.measureText(result).width > maxWidth) {
			result = result.slice(0, -2) + '…';
		}
		return result;
	};

	const draw = async () => {
		const width = 1080;
		const height = 1350;
		const canvas = document.createElement('canvas');
		canvas.width = width;
		canvas.height = height;
		const ctx = canvas.getContext('2d');
		if (document.fonts && document.fonts.ready) {
			await document.fonts.ready;
		}
		const font = (weight, size) => `${weight} ${size}px "Inter Variable", system-ui, sans-serif`;

		// background: the colors of the page
		const background = ctx.createLinearGradient(0, 0, width, height);
		background.addColorStop(0, '#d62839');
		background.addColorStop(0.55, '#8e1d6b');
		background.addColorStop(1, '#1d4f91');
		ctx.fillStyle = background;
		ctx.fillRect(0, 0, width, height);
		const glow = ctx.createRadialGradient(width * 0.85, height * 0.1, 0, width * 0.85, height * 0.1, width * 0.6);
		glow.addColorStop(0, 'rgba(15, 181, 216, 0.55)');
		glow.addColorStop(1, 'rgba(15, 181, 216, 0)');
		ctx.fillStyle = glow;
		ctx.fillRect(0, 0, width, height);

		const margin = 80;
		ctx.fillStyle = 'rgba(255, 255, 255, 0.85)';
		ctx.font = font(700, 30);
		ctx.fillText(text('.year-kicker').toUpperCase(), margin, 130);
		ctx.fillStyle = '#fff';
		ctx.font = font(800, 92);
		ctx.fillText(fit(ctx, text('[data-year-title]'), width - margin * 2), margin, 240);

		// the four numbers
		const stats = [
			[data.games, data.labelGames], [data.updates, data.labelUpdates],
			[data.dlc, data.labelDlc], [data.space, data.labelSpace],
		];
		const boxWidth = (width - margin * 2 - 30) / 2;
		stats.forEach(([value, label], i) => {
			const x = margin + (i % 2) * (boxWidth + 30);
			const y = 300 + Math.floor(i / 2) * 200;
			ctx.fillStyle = 'rgba(255, 255, 255, 0.14)';
			roundRect(ctx, x, y, boxWidth, 170, 32);
			ctx.fill();
			ctx.fillStyle = '#fff';
			ctx.font = font(800, 72);
			ctx.fillText(fit(ctx, String(value), boxWidth - 60), x + 36, y + 92);
			ctx.fillStyle = 'rgba(255, 255, 255, 0.8)';
			ctx.font = font(600, 28);
			ctx.fillText(fit(ctx, label, boxWidth - 60), x + 36, y + 138);
		});

		// the highlights
		const facts = [
			[data.labelGenre, text('[data-year-genre]')],
			[data.labelMonth, data.month],
			[data.labelBiggest, text('[data-year-biggest]')],
		].filter(([, value]) => value);
		let y = 760;
		facts.forEach(([label, value]) => {
			ctx.fillStyle = 'rgba(255, 255, 255, 0.75)';
			ctx.font = font(700, 24);
			ctx.fillText(label.toUpperCase(), margin, y);
			ctx.fillStyle = '#fff';
			ctx.font = font(700, 40);
			ctx.fillText(fit(ctx, value, width - margin * 2), margin, y + 48);
			y += 104;
		});

		// up to six covers of the new games
		const covers = [...document.querySelectorAll('[data-year-games] img')]
			.map(img => img.currentSrc || img.src)
			.filter(src => src.startsWith(window.location.origin))
			.slice(0, 6);
		const images = (await Promise.all(covers.map(loadImage))).filter(Boolean);
		const size = 130;
		const gap = (width - margin * 2 - size * 6) / 5;
		images.forEach((img, i) => {
			const x = margin + i * (size + gap);
			ctx.save();
			roundRect(ctx, x, 1100, size, size, 22);
			ctx.clip();
			ctx.drawImage(img, x, 1100, size, size);
			ctx.restore();
		});

		ctx.fillStyle = 'rgba(255, 255, 255, 0.7)';
		ctx.font = font(600, 24);
		ctx.fillText('Switch Library Manager Web', margin, height - 50);
		return canvas;
	};

	button.addEventListener('click', async () => {
		button.disabled = true;
		try {
			const canvas = await draw();
			const blob = await new Promise(resolve => canvas.toBlob(resolve, 'image/png'));
			if (!blob) {
				return;
			}
			const link = document.createElement('a');
			link.href = URL.createObjectURL(blob);
			link.download = `switch-${(text('[data-year-title]').match(/\d{4}/) || ['year'])[0]}.png`;
			document.body.appendChild(link);
			link.click();
			link.remove();
			setTimeout(() => URL.revokeObjectURL(link.href), 1000);
		} finally {
			button.disabled = false;
		}
	});
}

// Settings > Automations: the steps look off while the main switch is off (they keep their
// choice, so turning it on again brings them back).
function initAutomationSettings() {
	const master = document.querySelector('[data-automation-master]');
	const steps = document.querySelector('[data-automation-steps]');
	if (!master || !steps) {
		return;
	}
	const update = () => steps.classList.toggle('is-off', !master.checked);
	master.addEventListener('change', update);
	update();
}

// The setup wizard: loaded from the server when it opens (by itself the first time, or from
// Settings and the quick search), one step at a time, saved with the Settings form at the end.
let wizardLoading = null;

function openWizard(auto) {
	const existing = document.getElementById('setupWizard');
	if (existing) {
		Modal.getOrCreateInstance(existing).show();
		return Promise.resolve();
	}
	if (wizardLoading) {
		return wizardLoading;
	}
	wizardLoading = fetch(`/wizard${auto ? '?auto=1' : ''}`, { cache: 'no-store' })
		.then(response => (response.ok ? response.text() : Promise.reject(response.status)))
		.then(html => {
			const holder = document.createElement('div');
			holder.innerHTML = html;
			const element = holder.querySelector('#setupWizard');
			document.body.appendChild(element);
			setupWizard(element);
			Modal.getOrCreateInstance(element).show();
		})
		.catch(() => {
			// not an administrator, or the server could not be reached
		})
		.finally(() => {
			wizardLoading = null;
		});
	return wizardLoading;
}

function setupWizard(element) {
	const form = element.querySelector('#wizardForm');
	const steps = [...element.querySelectorAll('.wizard-step')];
	const done = steps.findIndex(step => step.dataset.step === 'done');
	const last = done - 1;
	const auto = element.dataset.auto === 'true';
	const authEnabled = element.dataset.auth === 'true';
	let texts = {};
	try {
		texts = JSON.parse(element.querySelector('[data-wizard-texts]').textContent);
	} catch (e) {
		// the English texts below
	}
	const say = (key, ...args) => {
		let text = texts[key] || key;
		args.forEach(arg => {
			text = text.replace('%v', arg);
		});
		return text;
	};
	const icon = element.querySelector('[data-wizard-icon]');
	const caption = element.querySelector('[data-wizard-caption]');
	const dots = element.querySelector('[data-wizard-dots]');
	const counter = element.querySelector('[data-wizard-counter]');
	const bar = element.querySelector('[data-wizard-bar]');
	const alert = element.querySelector('[data-wizard-alert]');
	const back = element.querySelector('[data-wizard-back]');
	const skip = element.querySelector('[data-wizard-skip]');
	const next = element.querySelector('[data-wizard-next]');
	const finish = element.querySelector('[data-wizard-finish]');
	const footer = element.querySelector('[data-wizard-footer]');
	const modal = Modal.getOrCreateInstance(element);
	let current = 0;
	let saved = false;

	for (let i = 0; i <= last; i++) {
		const dot = document.createElement('li');
		dot.className = 'wizard-dot';
		dots.appendChild(dot);
	}

	const showAlert = message => {
		alert.textContent = message || '';
		alert.hidden = !message;
	};

	const show = (index, direction) => {
		const previous = steps[current];
		current = Math.max(0, Math.min(index, steps.length - 1));
		const step = steps[current];
		steps.forEach(item => {
			item.hidden = item !== step;
			item.classList.remove('is-forward', 'is-backward');
		});
		step.classList.add(direction < 0 ? 'is-backward' : 'is-forward');
		// the picture of the step, with a little bounce
		icon.className = `bi ${step.dataset.icon}`;
		icon.parentElement.classList.remove('is-changing');
		void icon.parentElement.offsetWidth;
		icon.parentElement.classList.add('is-changing');
		caption.textContent = step.dataset.caption || '';
		// the picture of the step plays its animation again
		element.querySelectorAll('[data-scene]').forEach(scene => {
			const shown = scene.dataset.scene === step.dataset.step;
			// an SVG element has no hidden property: the attribute itself
			scene.toggleAttribute('hidden', !shown);
			scene.classList.remove('is-playing');
			if (shown) {
				void scene.getBoundingClientRect();
				scene.classList.add('is-playing');
			}
		});
		element.dataset.step = step.dataset.step;
		[...dots.children].forEach((dot, i) => {
			dot.classList.toggle('is-done', i < current);
			dot.classList.toggle('is-current', i === current);
		});
		const isDone = current === done;
		counter.textContent = isDone ? '' : say('step', current + 1, last + 1);
		bar.style.width = `${Math.round((isDone ? 1 : current / last) * 100)}%`;
		back.hidden = current === 0 || isDone;
		skip.hidden = current === 0 || current >= last;
		next.hidden = current >= last;
		finish.hidden = current !== last;
		footer.hidden = isDone;
		if (step.dataset.step === 'summary') {
			buildSummary();
		}
		showAlert('');
		if (previous !== step) {
			const focusable = step.querySelector('input:not([type=hidden]), select, textarea');
			if (focusable && window.matchMedia('(min-width: 768px)').matches) {
				focusable.focus({ preventScroll: true });
			}
		}
	};

	// the theme is chosen in the browser, like the menu of the header
	const themeButtons = [...element.querySelectorAll('[data-wizard-theme]')];
	const markTheme = () => {
		let theme = 'dark';
		try {
			theme = localStorage.getItem('slm-theme') || 'dark';
		} catch (e) {
			// the default
		}
		themeButtons.forEach(button => button.classList.toggle('is-active', button.dataset.wizardTheme === theme));
	};
	themeButtons.forEach(button => button.addEventListener('click', () => {
		applyTheme(button.dataset.wizardTheme);
		try {
			localStorage.setItem('slm-theme', button.dataset.wizardTheme);
		} catch (e) {
			// for this visit only
		}
		markTheme();
	}));
	markTheme();

	// live checks of the folders and the keys
	const check = (kind, value) => fetch(`/wizard/check?kind=${kind}&value=${encodeURIComponent(value)}`, { cache: 'no-store' })
		.then(response => (response.ok ? response.json() : Promise.reject(response.status)));
	const checkLine = (target, result) => {
		target.className = `wizard-check-line ${result.ok ? 'is-ok' : 'is-bad'}`;
		target.replaceChildren();
		const mark = document.createElement('span');
		mark.className = `bi ${result.ok ? 'bi-check-circle-fill' : 'bi-x-circle-fill'}`;
		mark.setAttribute('aria-hidden', 'true');
		target.append(mark, document.createTextNode(` ${result.message}`));
	};
	const folderField = form.elements.scan_folders;
	const folderResults = element.querySelector('[data-wizard-folder-results]');
	const checkFolders = () => {
		const folders = folderField.value.split('\n').map(line => line.trim()).filter(Boolean);
		folderResults.replaceChildren();
		if (folders.length === 0) {
			return Promise.resolve(false);
		}
		return Promise.all(folders.map(folder => {
			const line = document.createElement('li');
			line.className = 'wizard-check-line';
			line.textContent = `${folder} · ${say('checking')}`;
			folderResults.appendChild(line);
			return check('folder', folder).then(result => {
				checkLine(line, result);
				line.prepend(Object.assign(document.createElement('code'), { textContent: folder }), document.createTextNode(' '));
				return result.ok;
			}).catch(() => false);
		})).then(results => results.every(Boolean));
	};
	element.querySelector('[data-wizard-check-folders]').addEventListener('click', checkFolders);
	const keysResult = element.querySelector('[data-wizard-keys-result]');
	const checkKeys = () => check('keys', form.elements.prod_keys.value).then(result => checkLine(keysResult, result)).catch(() => {});
	element.querySelector('[data-wizard-check-keys]').addEventListener('click', checkKeys);

	// the automations look off while their main switch is off
	const master = element.querySelector('[data-automation-master]');
	const flow = element.querySelector('[data-automation-steps]');
	const updateFlow = () => flow.classList.toggle('is-off', !master.checked);
	master.addEventListener('change', updateFlow);
	updateFlow();

	// a step goes on only when what it asks is right
	const validate = () => {
		const step = steps[current].dataset.step;
		if (step === 'folders') {
			return checkFolders().then(ok => {
				if (!ok) {
					showAlert(folderResults.querySelector('.is-bad')?.textContent.trim() || say('folders'));
				}
				return ok;
			});
		}
		if (step === 'keys') {
			checkKeys();
		}
		return Promise.resolve(true);
	};

	const choice = name => {
		const field = form.elements[name];
		if (!field) {
			return '';
		}
		if (field instanceof RadioNodeList) {
			const checked = [...field].find(input => input.checked);
			return checked ? checked.closest('label').textContent.trim() : '';
		}
		if (field.tagName === 'SELECT') {
			return field.options[field.selectedIndex]?.textContent.trim() || '';
		}
		return field.value.trim();
	};
	const buildSummary = () => {
		const list = element.querySelector('[data-wizard-summary]');
		const folders = folderField.value.split('\n').map(line => line.trim()).filter(Boolean);
		const automation = ['automation_verify', 'automation_compress', 'automation_cleanup', 'automation_organize', 'automation_notify']
			.filter(name => form.elements[name].checked).length;
		const notifications = ['discord_webhook_url', 'webhook_url', 'telegram_bot_token'].some(name => form.elements[name].value.trim());
		const rows = [
			['bi-translate', say('language'), choice('language')],
			['bi-folder2-open', say('folders'), folders.join(', ') || say('none')],
			['bi-key', say('keys'), form.elements.prod_keys.value.trim() || say('default')],
			['bi-nintendo-switch', say('firmware'), form.elements.console_firmware.value.trim() || '—'],
			['bi-moon-stars', say('background'), choice('background_hours')],
			['bi-file-zip', say('compress'), choice('auto_compress')],
			['bi-magic', say('automation'), master.checked && automation ? say('stepsOn', automation) : say('off')],
			['bi-bell', say('notifications'), notifications ? say('set') : say('none')],
			['bi-hourglass-split', say('igdb'), form.elements.igdb_client_id.value.trim() ? say('set') : say('none')],
		];
		if (!authEnabled) {
			rows.push(['bi-shield-lock', say('admin'), form.elements.wizard_admin_name.value.trim() || say('none')]);
		}
		list.replaceChildren(...rows.map(([iconName, label, value], i) => {
			const row = document.createElement('li');
			row.style.setProperty('--i', i);
			const mark = document.createElement('span');
			mark.className = `wizard-summary-icon bi ${iconName}`;
			mark.setAttribute('aria-hidden', 'true');
			const name = document.createElement('span');
			name.className = 'wizard-summary-label';
			name.textContent = label;
			const text = document.createElement('span');
			text.className = 'wizard-summary-value';
			text.textContent = value;
			row.append(mark, name, text);
			return row;
		}));
	};

	back.addEventListener('click', () => show(current - 1, -1));
	skip.addEventListener('click', () => show(current + 1, 1));
	next.addEventListener('click', () => {
		next.disabled = true;
		validate().then(ok => {
			next.disabled = false;
			if (ok) {
				show(current + 1, 1);
			}
		});
	});
	form.addEventListener('keydown', e => {
		if (e.key === 'Enter' && e.target.tagName !== 'TEXTAREA' && current < last) {
			e.preventDefault();
			next.click();
		}
	});

	// a field the server finds wrong is shown on its step
	const stepOf = field => {
		const input = form.querySelector(`[name="${field}"]`);
		const step = input ? input.closest('.wizard-step') : null;
		return step ? steps.indexOf(step) : -1;
	};

	form.addEventListener('submit', e => {
		e.preventDefault();
		finish.disabled = true;
		finish.dataset.label = finish.dataset.label || finish.innerHTML;
		finish.textContent = say('saving');
		const body = new FormData(form);
		body.delete('wizard_admin_name');
		body.delete('wizard_admin_password');
		fetch('/settings.html', { method: 'POST', body: new URLSearchParams(body) })
			.then(response => response.json().then(json => ({ ok: response.ok, json })))
			.then(({ ok, json }) => {
				if (!ok) {
					const error = (json.fieldErrors || [])[0];
					if (error) {
						const index = stepOf(error.field);
						if (index >= 0) {
							show(index, -1);
						}
						showAlert(error.message);
					} else if (json.globalError) {
						showAlert(json.globalError.message || json.globalError.strongMessage);
					}
					return false;
				}
				// the first administrator, when asked
				const name = form.elements.wizard_admin_name ? form.elements.wizard_admin_name.value.trim() : '';
				const password = form.elements.wizard_admin_password ? form.elements.wizard_admin_password.value : '';
				if (!authEnabled && name && password) {
					return fetch('/users/create', { method: 'POST', body: new URLSearchParams({ name, password, role: 'admin' }), redirect: 'follow' })
						.then(response => {
							const failed = !response.ok || /[?&]error=/.test(response.url);
							if (failed) {
								show(stepOf('wizard_admin_name'), -1);
								showAlert(say('admin'));
								return false;
							}
							return true;
						});
				}
				return true;
			})
			.then(ok => {
				if (!ok) {
					return;
				}
				saved = true;
				// the covers of the games found are downloaded at once
				fetch('/wizard/state', { method: 'POST', body: new URLSearchParams({ state: 'done', covers: '1' }) });
				show(done, 1);
			})
			.catch(() => showAlert(t('The server could not be reached.')))
			.finally(() => {
				finish.disabled = false;
				finish.innerHTML = finish.dataset.label;
			});
	});

	const setState = state => fetch('/wizard/state', { method: 'POST', body: new URLSearchParams({ state }) }).catch(() => {});
	element.querySelector('[data-wizard-close]').addEventListener('click', () => {
		// closed the first time without choosing: asked again another day
		if (auto && !saved) {
			setState('later');
		}
		modal.hide();
	});
	element.querySelector('[data-wizard-later]')?.addEventListener('click', () => {
		setState('later');
		modal.hide();
	});
	element.querySelector('[data-wizard-never]')?.addEventListener('click', () => {
		setState('done');
		modal.hide();
	});
	element.addEventListener('hidden.bs.modal', () => {
		if (saved) {
			window.location.reload();
			return;
		}
		// opened again, it starts from the beginning with the saved settings
		element.remove();
	});

	show(0, 1);
}

function initWizard() {
	document.querySelectorAll('[data-wizard-open]').forEach(button => button.addEventListener('click', e => {
		e.preventDefault();
		openWizard(false);
	}));
	if (document.body.dataset.wizardAuto === 'true' && !window.location.pathname.startsWith('/users')) {
		openWizard(true);
	}
	// from the quick search: /settings.html#wizard
	if (window.location.hash === '#wizard') {
		history.replaceState(null, '', window.location.pathname);
		openWizard(false);
	}
}

// Space: a copy of a game from another region is deleted, with its updates and DLC, after a
// confirmation; the page shows the library again once it is rescanned.
// The header of the game page clips what goes out of it (its blurred background), so its
// menus (collections, pack) are placed fixed on the screen, over the rest of the page.
function initHeroMenus() {
	document.querySelectorAll('.title-hero [data-bs-toggle="dropdown"]').forEach(toggle => {
		Dropdown.getOrCreateInstance(toggle, { popperConfig: config => ({ ...config, strategy: 'fixed' }) });
	});
}

// Game page > Complete pack: the pack is made as a task, followed on the Tasks page.
function initPack() {
	document.querySelectorAll('[data-pack]').forEach(form => {
		const submit = form.querySelector('[type="submit"]');
		// keeping or deleting the separate files is always chosen, nothing is chosen first
		form.addEventListener('change', () => {
			submit.disabled = !form.elements.delete_originals.value;
		});
		form.addEventListener('submit', e => {
			e.preventDefault();
			const choice = form.elements.delete_originals.value;
			if (!choice) {
				return;
			}
			submit.disabled = true;
			runAsTask(() => postForm('/title/pack', { id: form.dataset.pack, delete_originals: choice }))
				.catch(error => {
					submit.disabled = false;
					insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), error.message);
				});
		});
	});
}

// the measure of the server fills the compression settings with what compresses fastest here
function initMeasure() {
	const box = document.querySelector('[data-measure]');
	if (!box) {
		return;
	}
	const button = box.querySelector('[data-measure-start]');
	const result = box.querySelector('[data-measure-result]');
	const format = (text, ...args) => args.reduce((value, arg) => value.replace('%v', arg), text);
	const line = (icon, text) => {
		const row = document.createElement('div');
		const glyph = document.createElement('span');
		glyph.className = `bi ${icon} me-1`;
		glyph.setAttribute('aria-hidden', 'true');
		row.append(glyph, text);
		return row;
	};
	button.addEventListener('click', () => {
		button.disabled = true;
		result.hidden = false;
		result.classList.remove('text-danger-emphasis');
		result.replaceChildren(line('bi-hourglass-split', result.dataset.textRunning));
		fetch('/settings/measure', { method: 'POST' })
			.then(response => response.json().catch(() => ({})).then(json => {
				if (!response.ok) {
					throw new Error(json.error || (json.globalError && json.globalError.message) || t('Unexpected server response (%v).', response.status));
				}
				return json;
			}))
			.then(measure => {
				const speeds = measure.results
					.map(trial => `${trial.files}×${trial.threads} → ${trial.speed} MB/s`).join(' · ');
				result.replaceChildren(
					line('bi-speedometer2', `${result.dataset.textTrials}: ${speeds}`),
					line('bi-check2-circle', format(result.dataset.textChosen, measure.files_at_once, measure.threads)));
				[['compress_threads', measure.threads], ['compress_files_at_once', measure.files_at_once]].forEach(([id, value]) => {
					const select = document.getElementById(id);
					if (select && [...select.options].some(option => option.value === String(value))) {
						select.value = String(value);
						select.dispatchEvent(new Event('input', { bubbles: true }));
						select.dispatchEvent(new Event('change', { bubbles: true }));
					}
				});
			})
			.catch(error => {
				result.classList.add('text-danger-emphasis');
				result.replaceChildren(line('bi-exclamation-triangle', error.message));
			})
			.finally(() => {
				button.disabled = false;
			});
	});
}

// while the Space page is prepared on the server, it asks until it is ready and opens again
function initSpacePreparing() {
	if (!document.querySelector('[data-space-preparing]')) {
		return;
	}
	const ask = () => fetch('/space/ready', { cache: 'no-store', headers: { Accept: 'application/json' } })
		.then(response => (response.ok ? response.json() : { ready: false }))
		.then(state => {
			if (state.ready) {
				window.location.reload();
			} else {
				setTimeout(ask, 1000);
			}
		})
		.catch(() => setTimeout(ask, 3000));
	setTimeout(ask, 800);
}

// screenshots that cannot be loaded are hidden; when none can, a note says why
function initScreenshots() {
	const strip = document.querySelector('[data-screenshots]');
	if (!strip) {
		return;
	}
	const images = [...strip.querySelectorAll('img')];
	let failed = 0;
	const fail = image => {
		image.closest('a').hidden = true;
		failed++;
		if (failed === images.length) {
			strip.hidden = true;
			document.querySelector('[data-screenshot-note]').hidden = false;
		}
	};
	images.forEach(image => {
		if (image.complete && image.naturalWidth === 0) {
			fail(image);
		} else {
			image.addEventListener('error', () => fail(image), { once: true });
		}
	});
}

// covers and pictures that cannot be loaded (a network that blocks Nintendo, a cover gone)
// show the placeholder instead of a broken image
function initImageFallbacks() {
	document.addEventListener('error', e => {
		const image = e.target;
		if (!(image instanceof HTMLImageElement) || image.dataset.fallback || image.classList.contains('title-screenshot')) {
			return;
		}
		if (image.closest('.game-cover, .dlc-tile-image, .title-hero, .dlc-game, .mini-card, .td-img') || image.classList.contains('td-img')) {
			image.dataset.fallback = 'true';
			image.src = '/resources/static/noimage.png';
		}
	}, true);
}

// pages of cards tell the server how many cards fit in a row, which rounds the cards per
// page to whole rows; the first time, or when the width changed, the page is drawn again
function initFullRows() {
	const grid = document.querySelector('.game-grid');
	if (!grid) {
		return;
	}
	const columns = () => getComputedStyle(grid).gridTemplateColumns.split(' ').filter(Boolean).length;
	const stored = () => {
		const match = document.cookie.match(/(?:^|;\s*)slm-columns=(\d+)/);
		return match ? Number(match[1]) : 0;
	};
	const remember = count => {
		document.cookie = `slm-columns=${count}; path=/; max-age=31536000; SameSite=Lax`;
	};
	const count = columns();
	if (count < 2 || count === stored()) {
		return;
	}
	remember(count);
	// once per width, so a page that cannot fill its rows is never drawn again and again
	const key = `slm-columns-reloaded-${count}`;
	let reloaded = false;
	try {
		reloaded = sessionStorage.getItem(key) === '1';
		sessionStorage.setItem(key, '1');
	} catch (e) {
		reloaded = true;
	}
	if (!reloaded && document.querySelector('.pagination')) {
		window.location.reload();
	}
	let timer = null;
	window.addEventListener('resize', () => {
		clearTimeout(timer);
		timer = setTimeout(() => {
			const now = columns();
			if (now >= 2 && now !== stored()) {
				remember(now);
			}
		}, 300);
	});
}

// emptying a search of a page, with the cross of the field or by deleting the text, shows
// everything again at once
function initSearchClear() {
	document.addEventListener('input', e => {
		const input = e.target;
		if (!input.matches || !input.matches('form input[type="search"][name="q"]') || input.value !== '' || input.defaultValue === '') {
			return;
		}
		if (input.form.requestSubmit) {
			input.form.requestSubmit();
		} else {
			input.form.submit();
		}
	});
}

// buttons that copy a text, such as the ID of a game
// copyText puts a text in the clipboard; without HTTPS (a NAS opened by its address) the
// browser has no clipboard API, and a selected field is copied instead
function copyText(text) {
	if (navigator.clipboard && window.isSecureContext) {
		return navigator.clipboard.writeText(text);
	}
	return new Promise((resolve, reject) => {
		const field = document.createElement('textarea');
		field.value = text;
		field.setAttribute('readonly', '');
		field.style.position = 'fixed';
		field.style.opacity = '0';
		document.body.appendChild(field);
		field.select();
		const copied = document.execCommand('copy');
		field.remove();
		if (copied) {
			resolve();
		} else {
			reject(new Error('not copied'));
		}
	});
}

// buttons that copy a text, such as the ID of a game, and say so for a moment
function initCopyButtons() {
	document.addEventListener('click', e => {
		const button = e.target.closest('[data-copy]');
		if (!button) {
			return;
		}
		copyText(button.dataset.copy).then(() => {
			button.classList.add('is-copied');
			setTimeout(() => button.classList.remove('is-copied'), 1200);
			const label = button.querySelector('[data-copy-label]');
			if (label && button.dataset.copied) {
				const text = label.textContent;
				label.textContent = button.dataset.copied;
				setTimeout(() => {
					label.textContent = text;
				}, 1500);
			}
		}).catch(() => {});
	});
}

// Issues: delete the files of a kind of issue that are of no use, after a confirmation
function initIssueDelete() {
	document.querySelectorAll('[data-issue-delete]').forEach(button => button.addEventListener('click', () => {
		if (!window.confirm(button.dataset.confirm)) {
			return;
		}
		button.disabled = true;
		runAsTask(() => postForm('/issues/delete', { kind: button.dataset.issueDelete }))
			.catch(error => {
				button.disabled = false;
				insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), error.message);
			});
	}));
}

// the charts of the history: the values of the day under the pointer
function initHistoryCharts() {
	document.querySelectorAll('.history-charts [data-bs-toggle="pill"]').forEach((button) => {
		button.addEventListener('click', (event) => {
			event.preventDefault();
			Tab.getOrCreateInstance(button).show();
		});
	});
	document.querySelectorAll('[data-history-chart]').forEach((box) => {
		let days;
		try {
			days = JSON.parse(box.dataset.historyChart);
		} catch {
			return;
		}
		const svg = box.querySelector('svg');
		const cursor = box.querySelector('.history-chart-cursor');
		const tip = box.querySelector('.history-chart-tip');
		const labels = Array.from(box.closest('figure').querySelectorAll('[data-series-label]'));
		if (!svg || !cursor || !tip || !days.length) {
			return;
		}
		const show = (event) => {
			const rect = svg.getBoundingClientRect();
			const x = ((event.clientX - rect.left) / rect.width) * 600;
			let day = days[0];
			for (const candidate of days) {
				if (Math.abs(candidate.x - x) < Math.abs(day.x - x)) {
					day = candidate;
				}
			}
			cursor.setAttribute('x1', day.x);
			cursor.setAttribute('x2', day.x);
			cursor.removeAttribute('hidden');
			tip.replaceChildren();
			const date = document.createElement('div');
			date.className = 'fw-semibold';
			date.textContent = day.d;
			tip.append(date);
			day.v.forEach((value, i) => {
				const row = document.createElement('div');
				const dot = labels[i] ? labels[i].querySelector('.chart-dot') : null;
				if (dot) {
					row.append(dot.cloneNode());
					row.append(' ');
				}
				row.append(value);
				tip.append(row);
			});
			tip.hidden = false;
			const left = (day.x / 600) * rect.width;
			const width = tip.offsetWidth;
			tip.style.left = `${Math.min(Math.max(left - width / 2, 0), rect.width - width)}px`;
		};
		const hide = () => {
			cursor.setAttribute('hidden', '');
			tip.hidden = true;
		};
		svg.addEventListener('pointermove', show);
		svg.addEventListener('pointerdown', show);
		svg.addEventListener('pointerleave', hide);
	});
}

// the tune-up: "Review again" scans the library, and while steps are worked out the page
// asks again by itself
function initMaintenance() {
	const button = document.querySelector('[data-maintenance-review]');
	if (button) {
		button.addEventListener('click', () => {
			button.disabled = true;
			fetch('/maintenance/review', { method: 'POST' })
				.then(() => setTimeout(() => window.location.reload(), 1500))
				.catch(() => {
					button.disabled = false;
				});
		});
	}
	if (document.querySelector('[data-maintenance-preparing]')) {
		setTimeout(() => {
			if (document.visibilityState === 'visible') {
				window.location.reload();
			} else {
				document.addEventListener('visibilitychange', () => window.location.reload(), { once: true });
			}
		}, 3000);
	}
}

function initRegionRemove() {
	document.querySelectorAll('[data-region-remove]').forEach(button => {
		button.addEventListener('click', () => {
			if (!window.confirm(button.dataset.confirmText)) {
				return;
			}
			button.disabled = true;
			postForm('/space/regions/remove', { id: button.dataset.regionRemove })
				.then(() => {
					button.closest('.item-row').classList.add('is-removed');
					setTimeout(() => window.location.reload(), 1200);
				})
				.catch(error => {
					button.disabled = false;
					insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), error.message);
				});
		});
	});
}

// Settings > App on your phone: the install prompt of the browser when it offers one
// (Chrome, Edge and Samsung Internet, over HTTPS), the code to open the server on the phone,
// and the guide of the device first. The prompt is caught as soon as the page loads.
let installPrompt = null;
window.addEventListener('beforeinstallprompt', event => {
	event.preventDefault();
	installPrompt = event;
	showMobileStatus();
});
window.addEventListener('appinstalled', () => {
	installPrompt = null;
	showMobileStatus(true);
});

function showMobileStatus(justInstalled = false) {
	const section = document.querySelector('[data-mobile-app]');
	if (!section) {
		return;
	}
	const installed = justInstalled || window.matchMedia('(display-mode: standalone)').matches || window.navigator.standalone === true;
	const toggle = (selector, hidden) => {
		const element = section.querySelector(selector);
		if (element) {
			element.hidden = hidden;
		}
	};
	toggle('[data-mobile-installed]', !installed);
	toggle('[data-mobile-install]', installed || !installPrompt);
	toggle('[data-mobile-insecure]', installed || window.isSecureContext);
}

function initMobileApp() {
	const section = document.querySelector('[data-mobile-app]');
	if (!section) {
		return;
	}
	showMobileStatus();
	section.querySelector('[data-mobile-install-button]')?.addEventListener('click', () => {
		// the prompt can be shown once: a second tap does nothing
		const prompt = installPrompt;
		installPrompt = null;
		if (!prompt) {
			return;
		}
		prompt.prompt();
		prompt.userChoice.finally(() => showMobileStatus());
	});

	const address = window.location.origin + '/';
	const addressText = section.querySelector('[data-mobile-address]');
	if (addressText) {
		addressText.textContent = address;
	}
	const localNote = section.querySelector('[data-mobile-local]');
	if (localNote) {
		localNote.hidden = !['localhost', '127.0.0.1', '[::1]'].includes(window.location.hostname);
	}
	const qr = section.querySelector('[data-mobile-qr]');
	if (qr) {
		const code = qrcode(0, 'M');
		code.addData(address);
		code.make();
		qr.innerHTML = code.createSvgTag({ cellSize: 4, margin: 2, scalable: true });
	}

	// the guide of this device open, and first
	const agent = navigator.userAgent;
	const device = /iPhone|iPad|iPod/.test(agent) || (/Macintosh/.test(agent) && navigator.maxTouchPoints > 1) ? 'ios'
		: /Android/.test(agent) ? 'android' : 'desktop';
	const guide = section.querySelector('[data-mobile-guide="' + device + '"]');
	if (guide) {
		guide.open = true;
		guide.parentElement.prepend(guide);
	}
}

// Settings > Save vault: the webdav.json file of JKSV, made in the browser with the address of
// the server and the user and password written above (the password is never sent back by the
// server, so it has to be written to be put in the file).
function initVaultSettings() {
	const origin = document.querySelector('[data-vault-origin]');
	const download = document.querySelector('[data-vault-download]');
	const master = document.querySelector('[data-vault-master]');
	const fields = document.querySelector('[data-vault-fields]');
	if (master && fields) {
		const update = () => fields.classList.toggle('is-off', !master.checked);
		master.addEventListener('change', update);
		update();
	}
	if (!origin || !download) {
		return;
	}
	origin.value = window.location.origin;
	download.addEventListener('click', () => {
		const user = document.getElementById('vault_user').value.trim();
		const password = document.getElementById('vault_password').value;
		if (!password) {
			insertAlert(download.closest('.vault-setup'), 'alert-warning', 'bi-key-fill', '', download.dataset.missingPassword);
			document.getElementById('vault_password').focus();
			return;
		}
		const config = { origin: origin.value.trim().replace(/\/+$/, '') + '/dav', basepath: 'JKSV', username: user, password };
		const blob = new Blob([JSON.stringify(config, null, 2) + '\n'], { type: 'application/json' });
		const link = document.createElement('a');
		link.href = URL.createObjectURL(blob);
		link.download = 'webdav.json';
		document.body.appendChild(link);
		link.click();
		link.remove();
		setTimeout(() => URL.revokeObjectURL(link.href), 1000);
	});
}

// Save backups: a backup is deleted after a confirmation.
function initSaveDelete() {
	document.querySelectorAll('[data-save-delete]').forEach(button => {
		button.addEventListener('click', () => {
			if (!window.confirm(button.dataset.confirmText)) {
				return;
			}
			button.disabled = true;
			postForm('/saves/delete', { path: button.dataset.saveDelete })
				.then(() => {
					const row = button.closest('.item-row');
					row.classList.add('is-removed');
					setTimeout(() => row.remove(), 400);
				})
				.catch(error => {
					button.disabled = false;
					insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), error.message);
				});
		});
	});
}

// cards tilt slightly towards the pointer
function initCardTilt() {
	if (reducedMotion() || !window.matchMedia('(hover: hover) and (pointer: fine)').matches) {
		return;
	}
	document.querySelectorAll('.game-card').forEach(card => {
		card.addEventListener('pointermove', e => {
			const box = card.getBoundingClientRect();
			const x = (e.clientX - box.left) / box.width - .5;
			const y = (e.clientY - box.top) / box.height - .5;
			card.style.setProperty('--tilt-x', `${(-y * 6).toFixed(2)}deg`);
			card.style.setProperty('--tilt-y', `${(x * 6).toFixed(2)}deg`);
			card.classList.add('is-tilting');
		});
		card.addEventListener('pointerleave', () => {
			card.classList.remove('is-tilting');
			card.style.removeProperty('--tilt-x');
			card.style.removeProperty('--tilt-y');
		});
	});
}

const VIEW_STORAGE_KEY = 'slm-view';

// large or small covers, remembered by the browser
function initViewToggle() {
	const buttons = document.querySelectorAll('[data-view]');
	if (!buttons.length) {
		return;
	}

	const apply = view => {
		document.querySelectorAll('.game-grid').forEach(grid => grid.classList.toggle('is-compact', view === 'compact'));
		buttons.forEach(button => {
			const active = button.dataset.view === view;
			button.classList.toggle('active', active);
			button.setAttribute('aria-pressed', active ? 'true' : 'false');
		});
	};

	let view = 'cards';
	try {
		view = localStorage.getItem(VIEW_STORAGE_KEY) || 'cards';
	} catch (e) {
		// storage may be blocked, the default view is used
	}
	apply(view);

	buttons.forEach(button => button.addEventListener('click', () => {
		apply(button.dataset.view);
		try {
			localStorage.setItem(VIEW_STORAGE_KEY, button.dataset.view);
		} catch (e) {
			// not remembered
		}
	}));
}

// The filters of each list are remembered by the browser: coming back to a list from another
// page (the menu, a game page) shows it as it was left. On the list itself, removing the
// filters shows everything.
const FILTER_PAGES = ['/index.html', '/updates.html', '/dlc.html', '/missing.html'];

function listPath(pathname) {
	return pathname === '/' ? '/index.html' : pathname;
}

// restoreFilters returns true when the page is replaced by the remembered filters.
function restoreFilters() {
	const path = listPath(window.location.pathname);
	if (!FILTER_PAGES.includes(path)) {
		return false;
	}
	const key = 'slm-filters:' + path;
	let from = '';
	try {
		const referrer = new URL(document.referrer);
		if (referrer.origin === window.location.origin) {
			from = listPath(referrer.pathname);
		}
	} catch (e) {
		// no referrer
	}
	try {
		const query = window.location.search;
		if (!query && from !== path) {
			const stored = localStorage.getItem(key);
			if (stored) {
				window.location.replace(path + stored);
				return true;
			}
		}
		localStorage.setItem(key, query);
	} catch (e) {
		// storage is not available: the lists start without filters
	}
	return false;
}

document.addEventListener('DOMContentLoaded', () => {
	if (restoreFilters()) {
		return;
	}
	const tooltipTriggerList = document.querySelectorAll('[data-bs-toggle="tooltip"]');
	[...tooltipTriggerList].map(tooltipTriggerEl => new Tooltip(tooltipTriggerEl));

	const sync = document.getElementById('sync');
	document.querySelectorAll('#sync, [data-sync-action]').forEach(link => {
		link.addEventListener('click', e => {
			e.preventDefault();
			startSync(sync.href);
		});
	});

	// a synchronization was already running when the page was rendered
	if (document.getElementById(SYNC_ALERT_ID)) {
		setSyncBusy(true);
		watchSync();
	}

	document.querySelectorAll('#settingsForm, #organizeForm').forEach(form => {
		form.addEventListener('submit', e => {
			e.preventDefault();
			onSubmit(form);
		});
	});

	initFlashMessages();
	initSaveWhenChanged();
	initOrganize();
	initIgnoreButtons();
	initLibraryBulk();
	initCollections();
	initSdPlanner();
	initIgnoreTypes();
	initViewToggle();
	initLiveTasks();
	initCompress();

	// the answer of a backup request; one that is not JSON (a file too large for the server,
	// a proxy in between) is still an answer, with its status
	const readBackupAnswer = response => response.json()
		.catch(() => ({ globalError: { message: t('Unexpected server response (%v).', response.status) } }))
		.then(json => ({ ok: response.ok, json }));
	const backupFailed = json => {
		const error = (json && json.globalError) || {};
		insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', error.strongMessage || t('Error!'), error.message || t('Could not reach the server.'));
	};

	const restoreForm = document.getElementById('restoreForm');
	if (restoreForm) {
		restoreForm.addEventListener('submit', e => {
			e.preventDefault();
			const button = restoreForm.querySelector('[type=submit]');
			if (!window.confirm(button.dataset.confirm)) {
				return;
			}
			button.disabled = true;
			fetch('/backup/restore', { method: 'POST', body: new FormData(restoreForm) })
				.then(readBackupAnswer)
				.then(({ ok, json }) => {
					button.disabled = false;
					if (ok) {
						insertAlert(mainContainer(), 'alert-success', 'bi-check-circle-fill', json.strongMessage, json.message);
						setTimeout(() => window.location.reload(), 1500);
					} else {
						backupFailed(json);
					}
				})
				.catch(() => {
					button.disabled = false;
					backupFailed();
				});
		});
	}

	// copies of the configuration kept by the app: make one now, or restore one
	document.querySelectorAll('[data-auto-backup], [data-auto-restore]').forEach(button => {
		button.addEventListener('click', () => {
			const name = button.dataset.autoRestore;
			if (name && !window.confirm(button.dataset.confirm)) {
				return;
			}
			button.disabled = true;
			fetch(name ? `/backup/auto/${encodeURIComponent(name)}/restore` : '/backup/auto', { method: 'POST' })
				.then(readBackupAnswer)
				.then(({ ok, json }) => {
					if (ok) {
						insertAlert(mainContainer(), 'alert-success', 'bi-check-circle-fill', json.strongMessage, json.message);
						setTimeout(() => window.location.reload(), 1500);
					} else {
						button.disabled = false;
						backupFailed(json);
					}
				})
				.catch(() => {
					button.disabled = false;
					backupFailed();
				});
		});
	});

	// installable app; browsers only allow it on HTTPS or localhost
	if ('serviceWorker' in navigator && window.isSecureContext) {
		navigator.serviceWorker.register('/sw.js').catch(() => undefined);
	}

	// searches every missing cover again; the progress is shown in Tasks
	document.querySelectorAll('[data-covers-retry]').forEach(button => {
		button.addEventListener('click', () => {
			button.disabled = true;
			runAsTask(() => fetch('/covers/retry', { method: 'POST' })).then(response => {
				if (!response.ok) {
					button.disabled = false;
				}
			}).catch(() => {
				button.disabled = false;
			});
		});
	});

	initMarks();
	initWhatsNew();
	initGamepad();
	initSounds();
	initCouchMode();
	initRevealOnScroll();
	initCollapsibleText();

	// selects and radio buttons that apply their form at once. With the keyboard the arrows of a
	// list only choose (each one would load the page again and leave the focus nowhere): Enter
	// or leaving the list applies the choice. The control has the focus again once the page is
	// loaded again.
	const REFOCUS_KEY = 'slm-refocus';
	try {
		const id = sessionStorage.getItem(REFOCUS_KEY);
		sessionStorage.removeItem(REFOCUS_KEY);
		const control = id && document.getElementById(id);
		if (control) {
			control.focus({ preventScroll: true });
		}
	} catch (e) {
		// not kept: the focus starts at the top
	}
	const apply = input => {
		if (!input.form) {
			return;
		}
		try {
			if (input.id) {
				sessionStorage.setItem(REFOCUS_KEY, input.id);
			}
		} catch (e) {
			// the focus is not given back
		}
		input.form.submit();
	};
	document.querySelectorAll('[data-autosubmit]').forEach(input => {
		if (input.tagName !== 'SELECT') {
			input.addEventListener('change', () => apply(input));
			return;
		}
		let byKeyboard = false;
		let chosen = input.value;
		input.addEventListener('keydown', e => {
			byKeyboard = true;
			if (e.key === 'Enter' && input.value !== chosen) {
				e.preventDefault();
				apply(input);
			}
		});
		input.addEventListener('pointerdown', () => {
			byKeyboard = false;
		});
		input.addEventListener('change', () => {
			if (!byKeyboard) {
				apply(input);
			}
		});
		input.addEventListener('blur', () => {
			if (byKeyboard && input.value !== chosen) {
				apply(input);
			}
			chosen = input.value;
		});
	});

	// notices that stay closed once closed (for a week, so they are not forgotten for good)
	document.querySelectorAll('[data-remember-close]').forEach(notice => {
		const key = notice.dataset.rememberClose;
		let closed = 0;
		try {
			closed = Number(localStorage.getItem(key) || 0);
		} catch (e) {
			// not remembered
		}
		if (Date.now() - closed > 7 * 24 * 3600 * 1000) {
			notice.hidden = false;
		} else {
			notice.hidden = true;
		}
		notice.addEventListener('closed.bs.alert', () => {
			try {
				localStorage.setItem(key, String(Date.now()));
			} catch (e) {
				// not remembered
			}
		});
	});

	const updateNotice = document.querySelector('[data-update-version]');
	if (updateNotice) {
		const key = 'slm-update-dismissed';
		let dismissed = '';
		try {
			dismissed = localStorage.getItem(key) || '';
		} catch (e) {
			// not remembered
		}
		if (dismissed !== updateNotice.dataset.updateVersion) {
			updateNotice.hidden = false;
		}
		updateNotice.addEventListener('closed.bs.alert', () => {
			try {
				localStorage.setItem(key, updateNotice.dataset.updateVersion);
			} catch (e) {
				// not remembered
			}
		});
	}

	document.querySelectorAll('[data-verify]').forEach(button => {
		button.addEventListener('click', () => {
			button.disabled = true;
			const body = new URLSearchParams({ all: button.dataset.verify === 'all' ? 'true' : 'false', scope: button.dataset.verify });
			runAsTask(() => fetch('/verify/start', { method: 'POST', body })).then(response => {
				if (response.ok) {
					return;
				}
				return response.json().catch(() => ({})).then(json => {
					button.disabled = false;
					const error = json.globalError || {};
					insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', error.strongMessage || t('Error!'), error.message || t('Unexpected server response (%v).', response.status));
				});
			}).catch(() => {
				button.disabled = false;
				insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), t('Could not reach the server.'));
			});
		});
	});
	initCountUp();
	initCoverLoading();
	initCardTilt();
	initSettingsSections();
	initCoverTransition();
	initQuickSearch();
	initTaskIndicator();
	initYearReview();
	initAutomationSettings();
	initWizard();
	initRegionRemove();
	initMaintenance();
	initHistoryCharts();
	initIssueDelete();
	initSearchClear();
	initCopyButtons();
	initFullRows();
	initImageFallbacks();
	initScreenshots();
	initSpacePreparing();
	initPack();
	initMeasure();
	initHeroMenus();
	initVaultSettings();
	initMobileApp();
	initSaveDelete();

	// forms that delete something ask first
	document.querySelectorAll('form[data-confirm]').forEach(form => {
		form.addEventListener('submit', e => {
			if (!window.confirm(form.dataset.confirm)) {
				e.preventDefault();
			}
		});
	});
	initNotificationTest();
	initThemeSwitcher();
	initBulkActions();
}, false);
