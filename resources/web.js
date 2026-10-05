/*jshint esversion: 9 */
/*globals bootstrap */

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
		closeBtn.setAttribute('aria-label', 'Close');
		closeBtn.setAttribute('type', 'button');
		closeBtn.classList.add('btn-close');
		closeBtn.dataset.bsDismiss = 'alert';
		alert.appendChild(closeBtn);
	}

	element.insertBefore(alert, element.firstChild);
}

const SYNC_ALERT_ID = 'alert_sync';
const SYNC_POLL_INTERVAL = 3000;

function mainContainer() {
	return document.querySelector('main > .container-fluid');
}

function showSyncAlert() {
	if (!document.getElementById(SYNC_ALERT_ID)) {
		insertAlert(mainContainer(), 'alert-info', 'bi-arrow-repeat', 'Synchronizing!', 'The library is being updated.', false, SYNC_ALERT_ID);
	}
}

function onSyncFinished() {
	const syncAlert = document.getElementById(SYNC_ALERT_ID);
	if (syncAlert) {
		syncAlert.remove();
	}

	if (document.querySelector('form[method="post"]')) {
		// do not reload pages with forms, it could discard unsaved changes
		insertAlert(mainContainer(), 'alert-success', 'bi-check-circle-fill', 'Done!', 'The library has been updated.');
	} else {
		window.location.reload();
	}
}

function watchSync() {
	setTimeout(() => {
		fetch('/sync', { method: 'GET', cache: 'no-store' })
			.then(response => response.json())
			.then(isSynchronizing => {
				if (isSynchronizing) {
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
	fetch(url, { method: 'POST' }).then(response => {
		if (!response.ok) {
			throw new Error(response.statusText);
		}
		showSyncAlert();
		watchSync();
	}).catch(() => {
		insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', 'Error!', 'Synchronization could not be started.');
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
			insertAlert(form, 'alert-danger', 'bi-exclamation-triangle-fill', 'Error!', 'Could not reach the server.');
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
			insertAlert(form, 'alert-danger', 'bi-exclamation-triangle-fill', 'Error!', `Unexpected server response (${error.status}).`);
		});
	}).finally(() => {
		if (submitButton) {
			submitButton.disabled = false;
		}
	});
}

const OPERATION_LABELS = {
	mkdir: 'Create folder',
	move: 'Move',
	delete: 'Delete',
	skip: 'Skip',
	cleanup: 'Clean up'
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
			const message = (json.globalError && json.globalError.message) || `Unexpected server response (${response.status}).`;
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
		detail.textContent = op.error ? `Error: ${op.error}` : (op.kind === 'mkdir' ? '' : (op.to && op.from ? op.to : (op.reason || '')));

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
			title.textContent = response.changes === 0 ? 'Preview: nothing to change' : `Preview: ${response.changes} change(s)` + (response.errors ? `, ${response.errors} problem(s)` : '');
			currentAction = action;
			runButton.textContent = `Apply ${response.changes} change(s)`;
			runButton.classList.toggle('d-none', response.changes === 0);
		} else {
			title.textContent = `Done: ${response.changes} change(s)` + (response.errors ? `, ${response.errors} failed` : '');
			runButton.classList.add('d-none');
			currentAction = null;
			showSyncAlert();
			watchSync();
		}
		result.scrollIntoView({ behavior: 'smooth', block: 'start' });
	};

	const fail = error => {
		insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', 'Error!', error.message);
	};

	actionButtons.forEach(button => button.addEventListener('click', () => {
		setBusy(true);
		postForm('/organize/preview', { action: button.dataset.organizeAction })
			.then(response => show(response, button.dataset.organizeAction))
			.catch(fail)
			.finally(() => setBusy(false));
	}));

	runButton.addEventListener('click', () => {
		if (!currentAction || !window.confirm('Apply the changes shown in the preview? Files will be moved or deleted.')) {
			return;
		}
		setBusy(true);
		postForm('/organize/run', { action: currentAction })
			.then(response => show(response, currentAction))
			.catch(fail)
			.finally(() => setBusy(false));
	});
}

document.addEventListener('DOMContentLoaded', () => {
	const tooltipTriggerList = document.querySelectorAll('[data-bs-toggle="tooltip"]');
	[...tooltipTriggerList].map(tooltipTriggerEl => new bootstrap.Tooltip(tooltipTriggerEl));

	const sync = document.getElementById('sync');
	sync.addEventListener('click', e => {
		e.preventDefault();
		startSync(sync.href);
	});

	// a synchronization was already running when the page was rendered
	if (document.getElementById(SYNC_ALERT_ID)) {
		watchSync();
	}

	document.querySelectorAll('#settingsForm, #organizeForm').forEach(form => {
		form.addEventListener('submit', e => {
			e.preventDefault();
			onSubmit(form);
		});
	});

	initOrganize();
}, false);
