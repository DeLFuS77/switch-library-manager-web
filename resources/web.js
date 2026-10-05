// Bundled with Bootstrap by esbuild into resources/static/web.js (npm run build).
// Importing Bootstrap also enables its data attributes (offcanvas, dismissible alerts, ...).
import { Tooltip } from 'bootstrap';

// Translations of the texts below are provided by the server in the page language.
// "%v" placeholders are replaced by the arguments in order.
function t(text, ...args) {
	const translations = window.SLM_TRANSLATIONS || {};
	let result = translations[text] || text;
	args.forEach(arg => {
		result = result.replace('%v', arg);
	});
	return result;
}

function insertAlert(element, contextualClass, iconClass, strongMessage, message, dismissible = true, id = "") {
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

	if (document.querySelector('form[method="post"]')) {
		// do not reload pages with forms, it could discard unsaved changes
		insertAlert(mainContainer(), 'alert-success', 'bi-check-circle-fill', t('Done!'), t('The library has been updated.'));
	} else {
		window.location.reload();
	}
}

function watchSync() {
	setTimeout(() => {
		fetch('/sync', { method: 'GET', cache: 'no-store' })
			.then(response => response.json())
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
			if (jsonResponse.globalError.strongMessage || jsonResponse.globalError.message) {
				insertAlert(form, 'alert-danger', 'bi-exclamation-triangle-fill', jsonResponse.globalError.strongMessage, jsonResponse.globalError.message);
			} else if (jsonResponse.fieldErrors) {
				jsonResponse.fieldErrors.forEach(fieldError => {
					const validationFeedback = document.createElement('div');
					validationFeedback.id = `validation-feedback-${fieldError.field}`;
					validationFeedback.classList.add('invalid-feedback');
					validationFeedback.appendChild(document.createTextNode(fieldError.message));

					const field = form.querySelector(`[name="${fieldError.field}"]`);
					field.setAttribute('aria-describedby', `validation-feedback-${fieldError.field}`);
					field.classList.add('is-invalid');

					field.parentElement.appendChild(validationFeedback);
				});
			}
		}).catch(() => {
			insertAlert(form, 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), t('Unexpected server response (%v).', error.status));
		});
	}).finally(() => {
		if (submitButton) {
			submitButton.disabled = false;
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

function renderOperations(result) {
	const tbody = document.getElementById('organizeOperations');
	tbody.replaceChildren();

	result.operations.forEach(op => {
		const row = document.createElement('tr');
		if (op.error) {
			row.classList.add('table-danger');
		}

		const kind = document.createElement('td');
		kind.classList.add('text-nowrap');
		kind.textContent = OPERATION_LABELS[op.kind] || op.kind;

		const from = document.createElement('td');
		from.classList.add('text-break');
		from.textContent = op.from || op.to || '';

		const detail = document.createElement('td');
		detail.classList.add('text-break');
		detail.textContent = op.error ? t('Error: %v', op.error) : (op.kind === 'mkdir' ? '' : (op.to && op.from ? op.to : (op.reason || '')));

		row.append(kind, from, detail);
		tbody.appendChild(row);
	});
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

	const show = (response, action) => {
		result.classList.remove('d-none');
		renderOperations(response);

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

	runButton.addEventListener('click', () => {
		if (!currentAction || !window.confirm(t('Apply the changes shown in the preview? Files will be moved or deleted.'))) {
			return;
		}
		setBusy(true);
		postForm('/organize/run', { action: currentAction })
			.then(response => show(response, currentAction))
			.catch(fail)
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

// replace covers that cannot be loaded (e.g. the Nintendo servers are not reachable)
document.addEventListener('error', e => {
	const image = e.target;
	if (image.tagName === 'IMG' && !image.dataset.fallback && !image.classList.contains('title-screenshot')) {
		image.dataset.fallback = 'true';
		image.src = '/resources/static/noimage.png';
	}
}, true);

document.addEventListener('DOMContentLoaded', () => {
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

	initOrganize();
	initIgnoreButtons();
	initNotificationTest();
	initThemeSwitcher();
	initBulkActions();
}, false);
