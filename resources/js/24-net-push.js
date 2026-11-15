// Install over the network, from the web: on the page of a game, Send to console (the files go to
// a console of the user, which asks before installing them) and what of the game the consoles
// have; with the console keys (Settings and My account), what is known to be installed on each
// console, and Forget; under each console, what it is sent now or got lately.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

function netStorageName(storage) {
	if (storage === 'nand') {
		return t('Internal memory');
	}
	return storage === 'sd' ? t('SD card') : '';
}

function netWhen(value) {
	return new Date(value).toLocaleString(document.documentElement.lang || undefined, { dateStyle: 'medium', timeStyle: 'short' });
}

function netErrorMessage(response, json) {
	return (json.globalError && json.globalError.message) || response.statusText || t('Error!');
}

// fillConsoleInventories writes, under each console key of root, what is known to be installed on
// that console, with Forget. The keys of other users stay as they are.
function fillConsoleInventories(root) {
	const lines = [...(root || document).querySelectorAll('[data-console-inventory]')];
	if (!lines.length) {
		return;
	}
	fetch('/api/install/consoles', { headers: { Accept: 'application/json' } })
		.then(response => (response.ok ? response.json() : { consoles: [] }))
		.then(answer => {
			const byId = new Map((answer.consoles || []).map(console => [console.id, console]));
			lines.forEach(line => {
				const console = byId.get(line.dataset.consoleInventory);
				const inventory = console && console.inventory;
				line.replaceChildren();
				line.hidden = !inventory;
				if (!inventory) {
					return;
				}
				const text = document.createElement('span');
				const icon = document.createElement('span');
				icon.className = 'bi bi-hdd-stack me-1';
				icon.setAttribute('aria-hidden', 'true');
				text.append(icon, document.createTextNode(t('Installed on this console: %v titles · updated %v', inventory.titles.toLocaleString(document.documentElement.lang || undefined), netWhen(inventory.updated))));
				const forget = document.createElement('button');
				forget.type = 'button';
				forget.className = 'btn btn-link btn-sm';
				forget.textContent = t('Forget');
				forget.addEventListener('click', () => {
					if (!window.confirm(t('Forget what is installed on “%v”? The console sends it again the next time it is asked.', console.name))) {
						return;
					}
					forget.disabled = true;
					apiFetch('/api/install/inventory/' + encodeURIComponent(console.id), { method: 'DELETE' })
						.then(response => readJSON(response).then(json => {
							if (!response.ok) {
								throw new Error(netErrorMessage(response, json));
							}
							line.replaceChildren();
							line.hidden = true;
						}))
						.catch(e => {
							forget.disabled = false;
							showToast('alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), e.message);
						});
				});
				line.append(text, forget);
			});
		})
		.catch(() => {});
}

// fillConsoleQueues shows, under each console of root, what it is sent now: with DBI and Tinfoil
// what the app counts of the files it serves, with NXDock what its queue tells (the stage, what
// waits, and Pause, Go on and Cancel; the console asks on its screen before cancelling). Once it
// ended, what it got stays there for half an hour, with the average speed of each file.
const netQueueStages = { copy: 'Copying the save', network: 'Downloading', decompress: 'Decompressing', write: 'Writing', check: 'Checking', register: 'Registering' };
let netQueueTimer = 0;

function netQueueAsk(consoleId, action, job, game) {
	if (action === 'cancel' && !window.confirm(t('Cancel the install of %v? The console asks before cancelling.', game))) {
		return;
	}
	apiFetch('/api/install/queue/' + encodeURIComponent(consoleId), {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ action, job }),
	})
		.then(response => readJSON(response).then(json => {
			if (!response.ok) {
				throw new Error(netErrorMessage(response, json));
			}
			showToast('alert-success', 'bi-check-circle-fill', '', action === 'cancel' ? t('Asked: the console asks before cancelling.') : t('Asked: the console does it in a moment.'));
		}))
		.catch(e => showToast('alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), e.message));
}

// Pause, Go on and Cancel of NXDock, also on the Tasks page, whose list is drawn again often
document.addEventListener('click', e => {
	const button = e.target.closest('[data-net-ask]');
	if (button) {
		netQueueAsk(button.dataset.netConsole, button.dataset.netAsk, Number(button.dataset.netJob || 0), button.dataset.netGame || '');
	}
});

function netSpeed(value) {
	return t('%v MB/s', (value || 0).toLocaleString(document.documentElement.lang || undefined, { minimumFractionDigits: 1, maximumFractionDigits: 1 }));
}

// netShortDuration is a time as "1h 5m", "2m 08s" or "40s", as the Tasks page writes it
function netShortDuration(seconds) {
	const total = Math.round(seconds || 0);
	const hours = Math.floor(total / 3600);
	const minutes = Math.floor(total / 60) % 60;
	if (hours) {
		return hours + 'h ' + minutes + 'm';
	}
	return minutes ? minutes + 'm ' + String(total % 60).padStart(2, '0') + 's' : total + 's';
}

function netFileName(file) {
	if (file.kind === 'update') {
		return file.name + ' · ' + t('Update') + ' ' + file.label;
	}
	if (file.kind === 'game') {
		return file.name + ' · ' + t('Game');
	}
	return file.name + (file.label ? ' · ' + file.label : '');
}

function netPercent(file) {
	return file.size > 0 ? Math.min(100, file.done / file.size * 100) : (file.state === 'done' ? 100 : 0);
}

// what a run that ended did, in a few words, as the Tasks page says it
function netCounts(send) {
	const parts = [send.via === 'NXDock' ? t('%v installed', send.ok) : t('%v sent whole', send.ok)];
	if (send.cut) {
		parts.push(t('%v cut', send.cut));
	}
	if (send.failed) {
		parts.push(t('%v with an error', send.failed));
	}
	if (send.cancelled) {
		parts.push(t('%v cancelled', send.cancelled));
	}
	return parts.join(', ');
}

function netLine(className, text) {
	const line = document.createElement('div');
	line.className = className;
	line.textContent = text;
	return line;
}

function netSendRunning(send) {
	const box = document.createElement('div');
	box.className = 'net-queue-box is-work';
	const running = send.files.find(file => file.state === 'running');
	const waiting = send.files.filter(file => file.state === 'waiting');
	const head = document.createElement('div');
	head.className = 'small fw-semibold';
	const icon = document.createElement('span');
	icon.className = 'bi ' + (send.paused ? 'bi-pause-circle' : send.gone ? 'bi-wifi-off' : 'bi-arrow-down-circle text-work') + ' me-1';
	icon.setAttribute('aria-hidden', 'true');
	head.append(icon, document.createTextNode(send.gone ? t('The console is not answering') : send.paused ? t('Paused') : running ? t('Installing %v', netFileName(running)) : t('Waiting')));
	box.append(head);
	if (running && !send.gone) {
		const percent = netPercent(running);
		const bar = document.createElement('div');
		bar.className = 'progress net-queue-bar';
		bar.setAttribute('role', 'progressbar');
		bar.setAttribute('aria-valuenow', String(Math.round(percent)));
		bar.setAttribute('aria-valuemin', '0');
		bar.setAttribute('aria-valuemax', '100');
		const fill = document.createElement('div');
		fill.className = 'progress-bar progress-bar-striped progress-bar-animated is-work';
		fill.style.width = percent.toFixed(1) + '%';
		bar.append(fill);
		const parts = [Math.round(percent) + ' %'];
		if (running.stage) {
			parts.push(t(netQueueStages[running.stage] || running.stage));
		}
		if (running.speed) {
			parts.push(netSpeed(running.speed));
		}
		if (running.average) {
			parts.push(t('%v on average', netSpeed(running.average)));
		}
		if (running.left) {
			parts.push(t('%v left', netShortDuration(running.left)));
		}
		box.append(bar, netLine('small text-body-secondary', parts.join(' · ')));
	}
	if (waiting.length) {
		box.append(netLine('small text-body-secondary', t('%v waiting', waiting.length) + ': ' + waiting.slice(0, 3).map(file => file.name).join(', ') + (waiting.length > 3 ? '…' : '')));
	}
	if (send.via === 'NXDock' && !send.gone && (running || waiting.length)) {
		const buttons = document.createElement('div');
		buttons.className = 'd-flex flex-wrap gap-2 mt-1';
		const pause = document.createElement('button');
		pause.type = 'button';
		pause.className = 'btn btn-sm btn-outline-secondary rounded-pill';
		pause.textContent = send.paused ? t('Go on') : t('Pause');
		pause.dataset.netAsk = send.paused ? 'resume' : 'pause';
		pause.dataset.netConsole = send.id;
		buttons.append(pause);
		if (running) {
			const cancel = document.createElement('button');
			cancel.type = 'button';
			cancel.className = 'btn btn-sm btn-outline-danger rounded-pill';
			cancel.textContent = t('Cancel');
			cancel.dataset.netAsk = 'cancel';
			cancel.dataset.netConsole = send.id;
			cancel.dataset.netJob = String(running.job);
			cancel.dataset.netGame = running.name;
			buttons.append(cancel);
		}
		box.append(buttons);
	}
	return box;
}

function netSendEnded(send) {
	const box = document.createElement('div');
	const failedAll = send.failed && !send.ok;
	const warn = send.cut || send.failed;
	box.className = 'net-queue-box ' + (failedAll ? 'is-ended-fail' : warn ? 'is-ended-warn' : 'is-ended');
	const head = document.createElement('div');
	head.className = 'small fw-semibold';
	const icon = document.createElement('span');
	icon.className = 'bi ' + (failedAll ? 'bi-x-circle text-danger-emphasis' : warn ? 'bi-exclamation-circle text-warning-emphasis' : 'bi-check-circle text-success-emphasis') + ' me-1';
	icon.setAttribute('aria-hidden', 'true');
	const at = new Date(send.ended).toLocaleTimeString(document.documentElement.lang || undefined, { hour: '2-digit', minute: '2-digit' });
	head.append(icon, document.createTextNode(t('Ended at %v', at) + ': ' + netCounts(send)));
	box.append(head);
	const facts = [t('%v in %v', formatSize(send.done), netShortDuration(send.seconds))];
	if (send.average) {
		facts.push(t('%v on average', netSpeed(send.average)));
	}
	box.append(netLine('small text-body-secondary', facts.join(' · ')));
	const marks = { done: '✓ ', cut: '✂ ', failed: '✗ ', cancelled: '⊘ ' };
	const files = document.createElement('div');
	files.className = 'small text-body-secondary';
	send.files.slice(0, 10).forEach(file => {
		files.append(netLine('', (marks[file.state] || '· ') + netFileName(file) + (file.average ? ' (' + netSpeed(file.average) + ')' : '')));
	});
	if (send.files.length > 10) {
		files.append(netLine('', t('and %v more', send.files.length - 10)));
	}
	box.append(files);
	const note = document.createElement('div');
	note.className = 'net-fade-note mt-1';
	const link = document.createElement('a');
	link.href = '/tasks.html';
	link.className = 'fw-semibold text-work';
	link.textContent = t('See it in Tasks');
	note.append(document.createTextNode(t('Shown here for half an hour.') + ' '), link);
	box.append(note);
	return box;
}

function fillConsoleQueues(root) {
	const lines = [...(root || document).querySelectorAll('[data-console-queue]')];
	clearTimeout(netQueueTimer);
	if (!lines.length) {
		return;
	}
	const again = busy => {
		netQueueTimer = setTimeout(() => fillConsoleQueues(document), document.hidden ? 15000 : busy ? 3000 : 10000);
	};
	fetch('/api/install/queue', { headers: { Accept: 'application/json' } })
		.then(response => (response.ok ? response.json() : { sends: [] }))
		.then(answer => {
			const byId = new Map((answer.sends || []).map(send => [send.id, send]));
			let busy = false;
			lines.forEach(line => {
				const send = byId.get(line.dataset.consoleQueue);
				line.hidden = !send;
				line.replaceChildren();
				if (!send) {
					return;
				}
				busy = busy || (send.running && !send.gone);
				line.append(send.running ? netSendRunning(send) : netSendEnded(send));
			});
			again(busy);
		})
		.catch(() => again(false));
}

function initNetSend() {
	const button = document.querySelector('[data-net-send]');
	const on = document.querySelector('[data-net-on]');
	const titleId = (button && button.dataset.netSend) || (on && on.dataset.netOn);
	if (!titleId) {
		return;
	}
	const modalElement = document.getElementById('netSend');
	const form = document.querySelector('[data-net-send-form]');
	let game = { consoles: [], files: [] };

	// what the consoles have of the game, under its actions
	const showOnConsoles = () => {
		if (!on) {
			return;
		}
		on.replaceChildren();
		game.consoles.forEach(console => {
			if (!console.installed) {
				return;
			}
			const chip = document.createElement('span');
			chip.className = 'chip chip-console';
			const icon = document.createElement('span');
			icon.className = 'bi bi-nintendo-switch';
			icon.setAttribute('aria-hidden', 'true');
			const storage = netStorageName(console.installed.storage);
			const text = storage
				? t('On %v: v%v (%v)', console.name, console.installed.version, storage)
				: t('On %v: v%v', console.name, console.installed.version);
			chip.append(icon, document.createTextNode(text));
			on.append(chip);
		});
		on.hidden = !on.children.length;
	};

	// the files ticked at first: the game and its newest update, unless the console has them
	const ticks = console => {
		const installed = console && console.installed;
		const boxes = form.querySelectorAll('[data-net-send-files] input[type=checkbox]');
		boxes.forEach(box => {
			const file = game.files.find(item => item.id === box.value);
			let checked = file.checked;
			if (checked && installed) {
				checked = file.kind === 'update' ? installed.version < file.version : false;
			}
			box.checked = checked;
		});
	};

	const fileLabel = file => {
		if (file.kind === 'game') {
			return t('Game') + (file.version ? ' v' + file.version : '');
		}
		if (file.kind === 'update') {
			return t('Update') + ' v' + file.version;
		}
		return file.name;
	};

	const fillDialog = () => {
		const select = form.querySelector('[data-net-send-console]');
		select.replaceChildren(...game.consoles.map(console => {
			const option = document.createElement('option');
			option.value = console.id;
			option.textContent = console.name;
			return option;
		}));
		const list = form.querySelector('[data-net-send-files]');
		list.replaceChildren(...game.files.map((file, i) => {
			const item = document.createElement('div');
			item.className = 'form-check';
			const box = document.createElement('input');
			box.type = 'checkbox';
			box.className = 'form-check-input';
			box.id = 'net-send-file-' + i;
			box.value = file.id;
			const label = document.createElement('label');
			label.className = 'form-check-label small';
			label.htmlFor = box.id;
			const name = document.createElement('span');
			name.className = 'text-break';
			name.textContent = fileLabel(file);
			const size = document.createElement('span');
			size.className = 'text-body-secondary';
			size.textContent = formatSize(file.size);
			label.append(name, size);
			item.append(box, label);
			return item;
		}));
		ticks(game.consoles[0]);
		select.onchange = () => ticks(game.consoles.find(console => console.id === select.value));
	};

	const error = form && form.querySelector('[data-net-send-error]');
	const showError = message => {
		error.textContent = message;
		error.hidden = !message;
	};

	fetch('/api/install/game/' + encodeURIComponent(titleId), { headers: { Accept: 'application/json' } })
		.then(response => (response.ok ? response.json() : null))
		.then(answer => {
			if (!answer) {
				return;
			}
			game = { consoles: answer.consoles || [], files: answer.files || [] };
			showOnConsoles();
			if (button && form && modalElement && answer.enabled && game.consoles.length && game.files.length) {
				button.hidden = false;
			}
		})
		.catch(() => {});

	if (!button || !form || !modalElement) {
		return;
	}
	button.addEventListener('click', () => {
		showError('');
		fillDialog();
		Modal.getOrCreateInstance(modalElement).show();
	});
	form.addEventListener('submit', e => {
		e.preventDefault();
		const keyId = form.querySelector('[data-net-send-console]').value;
		const fileIds = [...form.querySelectorAll('[data-net-send-files] input:checked')].map(box => box.value);
		if (!fileIds.length) {
			showError(t('Tick at least one file.'));
			return;
		}
		showError('');
		const send = form.querySelector('[data-net-send-go]');
		send.disabled = true;
		apiFetch('/api/install/push', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ keyId, fileIds }) })
			.then(response => readJSON(response).then(json => {
				if (!response.ok) {
					throw new Error(netErrorMessage(response, json));
				}
				Modal.getOrCreateInstance(modalElement).hide();
				showToast('alert-success', 'bi-check-circle-fill', '', t('Sent: the console asks before installing.'));
			}))
			.catch(e => showError(e.message || t('Could not reach the server.')))
			.finally(() => {
				send.disabled = false;
			});
	});
}

document.addEventListener('DOMContentLoaded', () => {
	initNetSend();
	fillConsoleInventories(document);
	fillConsoleQueues(document);
});
