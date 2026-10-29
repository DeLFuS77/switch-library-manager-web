// Tasks run on the server, and the synchronization of the library.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

// the tasks of the server, newest first
function fetchTasks() {
	return apiFetch('/api/tasks', { cache: 'no-store', headers: { Accept: 'application/json' } })
		.then(response => (response.ok ? readJSON(response) : []));
}

// runAsTask starts work that runs as tasks and stays on the page: a note says it is running,
// with a link to Tasks, and the page reloads with the new data when the tasks it started end.
// start returns the promise of the request; its result is passed on.
function runAsTask(start) {
	return fetchTasks().catch(() => []).then(tasks => {
		const known = Math.max(0, ...tasks.map(task => task.id));
		return start().then(result => {
			// a request that failed started nothing
			if (!(result instanceof Response)) {
				followTasks(known);
				return result;
			}
			if (!result.ok) {
				return result;
			}
			// a task put in the queue starts later, and one with nothing to do does not start: the
			// page says so instead of waiting for it
			return result.clone().json().catch(() => ({})).then(json => {
				if (!json || (!json.queued && !json.nothing)) {
					followTasks(known);
				}
				return result;
			});
		});
	});
}

// the message of a task, translated by the server in the language of the page
function taskText(task) {
	if (task.text) {
		return task.text;
	}
	return task.messageArg ? t(task.message, task.messageArg) : t(task.message || '');
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
			const text = taskText(task);
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
	spinner.classList.add('spinner-border', 'spinner-border-sm', 'text-work', 'flex-shrink-0', 'me-2');
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

	// the bar and, next to it, how far it is: the number does not squeeze inside the bar
	const row = document.createElement('div');
	row.classList.add('sync-progress-row', 'mt-2');
	const progress = document.createElement('div');
	progress.classList.add('progress', 'flex-grow-1');
	progress.setAttribute('role', 'progressbar');
	progress.setAttribute('aria-label', t('Synchronization progress'));
	const bar = document.createElement('div');
	bar.classList.add('progress-bar', 'progress-bar-striped', 'progress-bar-animated', 'is-work', 'w-100');
	progress.appendChild(bar);
	const percent = document.createElement('span');
	percent.classList.add('sync-percent');
	percent.dataset.syncPercent = '';
	row.append(progress, percent);

	alert.append(header, row);
	mainContainer().appendChild(alert);
}

function updateSyncAlert(status) {
	const alert = document.getElementById(SYNC_ALERT_ID);
	if (!alert) {
		return;
	}

	// the message, translated by the server in the language of the page
	const message = alert.querySelector('[data-sync-message]');
	if (message && (status.text || status.message)) {
		message.textContent = status.text || status.message;
	}

	const progress = alert.querySelector('.progress');
	const bar = alert.querySelector('.progress-bar');
	if (!progress || !bar) {
		return;
	}

	const label = alert.querySelector('[data-sync-percent]');
	if (status.total > 0) {
		// from red to green as it advances, like the bar of a task
		const exact = Math.min(100, status.current * 100 / status.total);
		const percent = Math.round(exact);
		// its stripes keep moving, as on the total of a task
		bar.classList.remove('w-100', 'is-work');
		bar.classList.add('task-total-bar', 'progress-bar-animated');
		bar.style.width = `${exact.toFixed(2)}%`;
		bar.style.setProperty('--pct', exact.toFixed(2));
		bar.textContent = '';
		if (label) {
			label.textContent = `${percent}%`;
		}
		progress.setAttribute('aria-valuenow', percent);
		progress.setAttribute('aria-valuemin', 0);
		progress.setAttribute('aria-valuemax', 100);
	} else {
		// unknown number of steps
		bar.classList.remove('task-total-bar');
		bar.classList.add('w-100', 'progress-bar-animated', 'is-work');
		bar.style.width = '';
		bar.style.removeProperty('--pct');
		bar.textContent = '';
		if (label) {
			label.textContent = '';
		}
		progress.removeAttribute('aria-valuenow');
	}
}

function onSyncFinished() {
	setSyncBusy(false);
	const syncAlert = document.getElementById(SYNC_ALERT_ID);
	// the bar is seen reaching its end before the notice goes
	const bar = syncAlert?.querySelector('.task-total-bar');
	if (bar && !window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
		bar.style.width = '100%';
		bar.style.setProperty('--pct', '100');
		const label = syncAlert.querySelector('[data-sync-percent]');
		if (label) {
			label.textContent = '100%';
		}
		setTimeout(finishSync, 750);
		return;
	}
	finishSync();
}

// The fields of the page the user changed (a note, the files chosen in Compress, the games for
// the card): a reload would lose them. A part of the page that saves its fields at once makes
// what it saved their default value, so they no longer count.
const editedFields = new Set();
function noteEditedField(e) {
	if (e.isTrusted && e.target instanceof Element && e.target.matches('main input, main textarea, main select')) {
		editedFields.add(e.target);
	}
}
document.addEventListener('input', noteEditedField, true);
document.addEventListener('change', noteEditedField, true);

function hasUnsavedChanges() {
	return [...editedFields].some(field => {
		if (!field.isConnected || field.disabled || field.type === 'file') {
			return false;
		}
		if (field.type === 'checkbox' || field.type === 'radio') {
			return field.checked !== field.defaultChecked;
		}
		if (field.tagName === 'SELECT') {
			const options = [...field.options];
			if (field.multiple) {
				return options.some(option => option.selected !== option.defaultSelected);
			}
			// with no option marked, the first one is the default
			const marked = options.map(option => option.defaultSelected).lastIndexOf(true);
			return field.selectedIndex !== Math.max(0, marked);
		}
		return field.value !== field.defaultValue;
	});
}

function finishSync() {
	const syncAlert = document.getElementById(SYNC_ALERT_ID);
	if (syncAlert) {
		syncAlert.remove();
	}

	// nor while games are being uploaded from the page: the reload would stop them; nor while
	// the setup wizard is open: the scan its saving starts closed its last screen at once
	const uploading = gameUploadsBusy() || document.querySelector('.wizard.show');
	if (uploading || document.querySelector('form[method="post"]:not([action="/logout"])') || hasUnsavedChanges()) {
		// do not reload pages with forms or changes, it could discard unsaved changes: the
		// notice offers the reload instead
		const toast = showToast('alert-success', 'bi-check-circle-fill', t('Done!'), t('The library has been updated.'));
		if (!uploading && !toast.querySelector('[data-sync-reload]')) {
			const reload = document.createElement('button');
			reload.type = 'button';
			reload.className = 'btn btn-link alert-link p-0 align-baseline';
			reload.dataset.syncReload = '';
			reload.textContent = t('Reload');
			reload.addEventListener('click', () => window.location.reload());
			toast.querySelector('.flex-grow-1').append(' ', reload);
		}
	} else {
		window.location.reload();
	}
}

// watchSync follows the synchronization until it ends. Called again while it follows one (saving
// the settings, ignoring a type of file...), it does not start a second loop.
let watchingSync = false;
function watchSync() {
	if (watchingSync) {
		return;
	}
	watchingSync = true;
	pollSync();
}

function pollSync() {
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
					pollSync();
				} else {
					watchingSync = false;
					onSyncFinished();
				}
			})
			// the server may be restarting, keep trying
			.catch(() => pollSync());
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

// insertQueuedNote tells that a task waits in the queue, with its place and a link to Tasks.
function insertQueuedNote(container, position) {
	insertAlert(container, 'alert-info', 'bi-hourglass-split', t('Added to the queue.'), t('It starts when the tasks before it end: number %v in the queue.', position));
}