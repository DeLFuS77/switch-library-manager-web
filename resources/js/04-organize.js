// Organize: the preview of the operations, ignoring and notification tests.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

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

// the groups of the list, in this order: what needs a look first, then the changes by kind
const OPERATION_GROUPS = ['error', 'review', 'move', 'rename', 'mkdir', 'delete', 'cleanup'];
// the rows a group shows at once, and each time more are asked
const OPERATION_ROWS_AT_ONCE = 100;

// operationGroup tells the group of a change: a file left as it is, renamed in its folder
// instead of moved, or whose title ID changes needs a look
function operationGroup(op, key) {
	if (key === 'skip' || key === 'review' || ((key === 'move' || key === 'rename') && op.reason)) {
		return 'review';
	}
	return key;
}

// operationRow is a change of the list, with its kind
function operationRow(op, key, kinds) {
	const kind = kinds[key] || { key, label: OPERATION_LABELS[op.kind] || op.kind, icon: 'bi-dot' };
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
		// why a file is renamed in its folder instead of moved
		const notes = op.reason ? [op.reason] : [];
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
	return row;
}

// renderOperations lists the changes in groups that open and close, closed at first, with their
// counts as the summary: only the problems are open, as they ask for something. The rows of a
// group are made when it is opened, a hundred at a time, so a list of thousands stays fast.
function renderOperations(result) {
	const list = document.getElementById('organizeOperations');
	list.replaceChildren();
	const kinds = Object.fromEntries(OPERATION_KINDS.map(kind => [kind.key, kind]));
	const heads = {
		error: { label: t('With problems'), icon: 'bi-x-octagon' },
		review: { label: t('Needs a look'), icon: 'bi-exclamation-triangle' }
	};
	const groups = {};
	result.operations.forEach(op => {
		const key = operationKind(op);
		const group = operationGroup(op, key);
		(groups[group] = groups[group] || []).push({ op, key });
	});

	const order = OPERATION_GROUPS.concat(Object.keys(groups).filter(key => !OPERATION_GROUPS.includes(key)));
	order.filter(key => groups[key]).forEach(key => {
		const entries = groups[key];
		const kind = heads[key] || kinds[key] || { label: key, icon: 'bi-dot' };
		const item = document.createElement('li');
		item.className = `op-group op-${key}`;
		const details = document.createElement('details');
		const head = document.createElement('summary');
		head.className = 'op-group-head';
		head.innerHTML = `<span class="bi ${kind.icon}" aria-hidden="true"></span>`;
		head.append(kind.label);
		const count = document.createElement('span');
		count.className = 'op-chip-count';
		count.textContent = entries.length;
		const chevron = document.createElement('span');
		chevron.className = 'bi bi-chevron-down op-group-chevron';
		chevron.setAttribute('aria-hidden', 'true');
		head.append(count, chevron);

		const rows = document.createElement('ul');
		rows.className = 'op-list list-unstyled';
		const more = document.createElement('button');
		more.type = 'button';
		more.className = 'btn btn-sm btn-outline-secondary rounded-pill op-group-more';
		let shown = 0;
		const showMore = () => {
			entries.slice(shown, shown + OPERATION_ROWS_AT_ONCE).forEach(entry => rows.append(operationRow(entry.op, entry.key, kinds)));
			shown = Math.min(entries.length, shown + OPERATION_ROWS_AT_ONCE);
			more.textContent = t('Show %v more', Math.min(OPERATION_ROWS_AT_ONCE, entries.length - shown));
			more.hidden = shown >= entries.length;
		};
		more.hidden = true;
		more.addEventListener('click', showMore);
		details.addEventListener('toggle', () => {
			if (details.open && shown === 0) {
				showMore();
			}
		});
		details.append(head, rows, more);
		item.append(details);
		list.append(item);
		if (key === 'error') {
			showMore();
			details.open = true;
		}
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
			message.textContent = taskText(task);
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

