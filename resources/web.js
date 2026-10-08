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
import { Dropdown, Modal, Tooltip } from 'bootstrap';

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
			const running = tasks.filter(task => task.status === 'running').length;
			const failed = tasks.filter(task => task.status === 'failed').length;
			taskLinks().forEach(link => {
				const icon = link.querySelector('.bi');
				if (icon) {
					icon.classList.toggle('bi-arrow-repeat', running > 0);
					icon.classList.toggle('nav-busy', running > 0);
					icon.classList.toggle('bi-list-task', running === 0);
				}
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
	if (document.querySelector('[data-live-tasks]') || !window.EventSource) {
		return;
	}
	const busy = taskLinks().some(link => link.querySelector('.nav-busy'));
	if (!busy) {
		return;
	}
	const source = new EventSource('/api/tasks/events');
	source.addEventListener('tasks', () => {
		updateTaskIndicator().then(running => {
			if (running === 0) {
				source.close();
			}
		});
	});
	window.addEventListener('pagehide', () => source.close());
}

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
				updateTaskIndicator();
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
	const modal = Modal.getOrCreateInstance(element);
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
			postForm('/title/pack', { id: form.dataset.pack, delete_originals: choice })
				.then(() => {
					window.location.href = '/tasks.html';
				})
				.catch(error => {
					submit.disabled = false;
					insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), error.message);
				});
		});
	});
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
	initOrganize();
	initIgnoreButtons();
	initLibraryBulk();
	initCollections();
	initSdPlanner();
	initIgnoreTypes();
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

	// copies of the configuration kept by the app: make one now, or restore one
	document.querySelectorAll('[data-auto-backup], [data-auto-restore]').forEach(button => {
		button.addEventListener('click', () => {
			const name = button.dataset.autoRestore;
			if (name && !window.confirm(button.dataset.confirm)) {
				return;
			}
			button.disabled = true;
			fetch(name ? `/backup/auto/${encodeURIComponent(name)}/restore` : '/backup/auto', { method: 'POST' })
				.then(response => response.json().then(json => ({ ok: response.ok, json })))
				.then(({ ok, json }) => {
					if (ok) {
						insertAlert(mainContainer(), 'alert-success', 'bi-check-circle-fill', json.strongMessage, json.message);
						setTimeout(() => window.location.reload(), 1500);
					} else {
						button.disabled = false;
						insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', json.globalError.strongMessage, json.globalError.message);
					}
				})
				.catch(() => {
					button.disabled = false;
				});
		});
	});

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

	initMarks();

	// selects and radio buttons that apply their form at once
	document.querySelectorAll('[data-autosubmit]').forEach(input => {
		input.addEventListener('change', () => input.form && input.form.submit());
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
	initSettingsSections();
	initCoverTransition();
	initQuickSearch();
	initTaskIndicator();
	initYearReview();
	initAutomationSettings();
	initWizard();
	initRegionRemove();
	initPack();
	initHeroMenus();
	initVaultSettings();
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
