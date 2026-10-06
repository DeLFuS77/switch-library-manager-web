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
import { Tooltip } from 'bootstrap';

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

// the SD card planner: totals of the chosen games, download of the list and copy
function initSdPlanner() {
	const summary = document.querySelector('[data-sd-usable]');
	if (!summary) {
		return;
	}
	const usable = Number(summary.dataset.sdUsable);
	const games = [...document.querySelectorAll('[data-sd-game]')];
	const chosen = () => games.filter(game => game.checked);
	const update = () => {
		const total = chosen().reduce((sum, game) => sum + Number(game.dataset.size), 0);
		summary.querySelector('[data-sd-count]').textContent = chosen().length;
		summary.querySelector('[data-sd-total]').textContent = summary.dataset.sdTemplate.replace('%v', formatSize(total)).replace('%v', formatSize(usable));
		const bar = summary.querySelector('[data-sd-bar]');
		bar.style.width = Math.min(100, usable > 0 ? total * 100 / usable : 100) + '%';
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
		postForm('/sd/copy', values).then(() => {
			window.location.href = '/tasks.html';
		}).catch(error => {
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
function initLiveTasks() {
	const list = document.querySelector('[data-live-tasks]');
	if (!list) {
		return;
	}

	let rendered = Number(list.dataset.version || 0);
	let loading = false;
	let pending = false;
	const refresh = () => {
		if (loading) {
			pending = true;
			return;
		}
		loading = true;
		fetch('/tasks.html?part=list', { cache: 'no-store' })
			.then(response => response.ok ? response.text() : Promise.reject(response))
			.then(html => {
				// the bars go on from where they were instead of starting again
				const widths = {};
				list.querySelectorAll('[data-task-id]').forEach(card => {
					const bar = card.querySelector('.progress-bar');
					if (bar) {
						widths[card.dataset.taskId] = bar.style.width;
					}
				});
				list.innerHTML = html;
				list.querySelectorAll('[data-task-id]').forEach(card => {
					const bar = card.querySelector('.progress-bar');
					const before = widths[card.dataset.taskId];
					if (!bar || !before || before === bar.style.width) {
						return;
					}
					const after = bar.style.width;
					bar.style.transition = 'none';
					bar.style.width = before;
					void bar.offsetWidth;
					bar.style.transition = '';
					bar.style.width = after;
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
		const source = new EventSource('/api/tasks/events');
		source.addEventListener('tasks', e => {
			const version = Number(e.data);
			if (version !== rendered) {
				rendered = version;
				refresh();
			}
		});
		window.addEventListener('pagehide', () => source.close());
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
		const url = dismiss ? `/api/tasks/${encodeURIComponent(dismiss.dataset.taskDismiss)}/dismiss` : '/api/tasks/clear';
		fetch(url, { method: 'POST' })
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
	document.querySelectorAll('[data-compress-cancel]').forEach(button => {
		button.addEventListener('click', () => {
			button.disabled = true;
			fetch('/compress/cancel', { method: 'POST' });
		});
	});
	// the cancel button of the tasks page is rendered again by the live list
	document.addEventListener('click', e => {
		const button = e.target.closest('#taskList [data-compress-cancel]');
		if (button) {
			button.disabled = true;
			fetch('/compress/cancel', { method: 'POST' });
		}
	});

	bindFileForm(document.getElementById('compressForm'), '/compress/start');
	bindFileForm(document.getElementById('decompressForm'), '/decompress/start');
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
	const load = query => {
		if (!list) {
			return Promise.resolve();
		}
		return fetch(list.dataset.fileList + '?q=' + encodeURIComponent(query || ''))
			.then(response => response.text())
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

	const filter = form.querySelector('[data-file-filter]');
	if (filter) {
		let timer;
		filter.addEventListener('input', () => {
			const text = filter.value.trim().toLowerCase();
			if (list) {
				clearTimeout(timer);
				timer = setTimeout(() => load(text), 300);
				return;
			}
			form.querySelectorAll('[data-file-row]').forEach(row => {
				row.hidden = text !== '' && !row.textContent.toLowerCase().includes(text);
			});
			update();
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
		fetch(url, {
			method: 'POST',
			body: new URLSearchParams(new FormData(form)).toString(),
			headers: { 'Content-type': 'application/x-www-form-urlencoded' }
		}).then(response => {
			if (response.ok) {
				window.location.href = '/tasks.html';
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
	initOrganize();
	initIgnoreButtons();
	initLibraryBulk();
	initCollections();
	initSdPlanner();
	initViewToggle();
	initLiveTasks();
	initCompress();

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
				.then(response => response.json().then(json => ({ ok: response.ok, json })))
				.then(({ ok, json }) => {
					button.disabled = false;
					if (ok) {
						insertAlert(mainContainer(), 'alert-success', 'bi-check-circle-fill', json.strongMessage, json.message);
						setTimeout(() => window.location.reload(), 1500);
					} else {
						insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', json.globalError.strongMessage, json.globalError.message);
					}
				})
				.catch(() => {
					button.disabled = false;
				});
		});
	}

	// installable app; browsers only allow it on HTTPS or localhost
	if ('serviceWorker' in navigator && window.isSecureContext) {
		navigator.serviceWorker.register('/sw.js').catch(() => undefined);
	}

	// copies a value, e.g. the title ID, and says so on the button for a moment
	document.querySelectorAll('[data-copy]').forEach(button => {
		const label = button.querySelector('[data-copy-label]');
		const text = label ? label.textContent : '';
		button.addEventListener('click', () => {
			if (!navigator.clipboard) {
				return;
			}
			navigator.clipboard.writeText(button.dataset.copy).then(() => {
				if (label) {
					label.textContent = button.dataset.copied;
					setTimeout(() => {
						label.textContent = text;
					}, 1500);
				}
			}).catch(() => undefined);
		});
	});

	// searches every missing cover again; the progress is shown in Tasks
	document.querySelectorAll('[data-covers-retry]').forEach(button => {
		button.addEventListener('click', () => {
			button.disabled = true;
			fetch('/covers/retry', { method: 'POST' }).then(response => {
				if (response.ok) {
					window.location.href = '/tasks.html';
				} else {
					button.disabled = false;
				}
			}).catch(() => {
				button.disabled = false;
			});
		});
	});

	// adds a game to the wishlist or removes it
	document.querySelectorAll('[data-fav]').forEach(button => {
		button.addEventListener('click', e => {
			e.preventDefault();
			e.stopPropagation();
			button.disabled = true;
			postForm('/favorites', { id: button.dataset.fav, favorite: button.dataset.favorite === 'true' ? 'false' : 'true' })
				.then(() => window.location.reload())
				.catch(() => {
					button.disabled = false;
				});
		});
	});

	document.querySelectorAll('[data-wish]').forEach(button => {
		button.addEventListener('click', e => {
			e.preventDefault();
			e.stopPropagation();
			button.disabled = true;
			postForm('/wishlist', { id: button.dataset.wish, wanted: button.dataset.wished === 'true' ? 'false' : 'true' })
				.then(() => window.location.reload())
				.catch(() => {
					button.disabled = false;
				});
		});
	});

	// selects and radio buttons that apply their form at once
	document.querySelectorAll('[data-autosubmit]').forEach(input => {
		input.addEventListener('change', () => input.form && input.form.submit());
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
			fetch('/verify/start', { method: 'POST', body }).then(response => {
				if (response.ok) {
					window.location.href = '/tasks.html';
					return;
				}
				return response.json().then(json => {
					button.disabled = false;
					insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', json.globalError.strongMessage, json.globalError.message);
				});
			});
		});
	});
	initCountUp();
	initCoverLoading();
	initCardTilt();

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
