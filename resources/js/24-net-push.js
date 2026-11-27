// Install over the network, from the web: on the page of a game, Send to console (the files go to
// a console of the user, which asks before installing them, or a NUP to nx2-flow on a Switch 2,
// at the address the user types) and what of the game the consoles have; with the console keys (Settings and My account), what is known to be installed on each
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

// how the files of Send to console go: Normal, or Compressed (experimental), remembered in this
// browser
const netSendModeKey = 'slm-net-send-mode';

function netSendModeRemembered() {
	try {
		return localStorage.getItem(netSendModeKey) === 'compressed' ? 'compressed' : 'normal';
	} catch (e) {
		return 'normal';
	}
}

function netSendModeRemember(mode) {
	try {
		localStorage.setItem(netSendModeKey, mode);
	} catch (e) {
		// a browser that keeps nothing: Normal the next time
	}
}

// the target of Send to console chosen last: NXDock or nx2-flow
const netSendTargetKey = 'slm-net-send-target';

function netSendTargetRemembered() {
	try {
		return localStorage.getItem(netSendTargetKey) === 'nx2flow' ? 'nx2flow' : 'nxdock';
	} catch (e) {
		return 'nxdock';
	}
}

function netSendTargetRemember(target) {
	try {
		localStorage.setItem(netSendTargetKey, target);
	} catch (e) {
		// private windows keep nothing
	}
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
	// the NUP files nx2-flow takes, and the address the user sent to last
	let nx2 = { files: [], address: '' };
	let canNxdock = false, canNx2 = false;

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

	const compressedMode = () => {
		const checked = form.querySelector('[data-net-send-mode]:checked');
		return !!checked && checked.value === 'compressed';
	};

	// the note of the way chosen, the size each file sends, and what Compressed saves when every
	// file ticked has its size known
	const showMode = () => {
		const compressed = compressedMode();
		form.querySelector('[data-net-send-note]').textContent = compressed
			? t('Less data goes over the network and NXDock decompresses it while installing. What is not compressed so yet, the app compresses first and it shows in Tasks.')
			: t('It is sent in the format every installer reads. What is stored in an experimental format is converted while it is sent.');
		let normal = 0, less = 0, known = true;
		form.querySelectorAll('[data-net-send-files] input[type=checkbox]').forEach(box => {
			const file = game.files.find(item => item.id === box.value);
			const row = box.closest('.form-check');
			const size = row.querySelector('[data-net-send-size]');
			const how = row.querySelector('[data-net-send-how]');
			const sent = compressed && file.compressedSize > 0 ? file.compressedSize : file.size;
			size.textContent = formatSize(sent);
			const smaller = compressed && sent < file.size;
			size.classList.toggle('net-send-less', smaller);
			size.classList.toggle('text-body-secondary', !smaller);
			how.textContent = !compressed ? '' : file.compressFirst ? t('compressed first') : file.asIs ? t('already compressed: sent as it is') : '';
			how.hidden = !how.textContent;
			if (box.checked) {
				normal += file.size;
				less += file.compressedSize;
				known = known && file.compressedSize > 0;
			}
		});
		const saving = form.querySelector('[data-net-send-saving]');
		saving.hidden = !(compressed && known && normal > less);
		if (!saving.hidden) {
			saving.textContent = t('%v less over the network (−%v %)', formatSize(normal - less), Math.round((1 - less / normal) * 100));
		}
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
			const how = document.createElement('span');
			how.className = 'text-body-secondary';
			how.dataset.netSendHow = '';
			how.hidden = true;
			const size = document.createElement('span');
			size.className = 'net-send-size text-body-secondary';
			size.dataset.netSendSize = '';
			size.textContent = formatSize(file.size);
			label.append(name, how, size);
			item.append(box, label);
			box.addEventListener('change', showMode);
			return item;
		}));
		ticks(game.consoles[0]);
		select.onchange = () => {
			ticks(game.consoles.find(console => console.id === select.value));
			showMode();
		};
		const mode = netSendModeRemembered();
		form.querySelectorAll('[data-net-send-mode]').forEach(radio => {
			radio.checked = radio.value === mode;
			radio.onchange = () => {
				netSendModeRemember(radio.value);
				showMode();
			};
		});
		showMode();
	};

	const error = form && form.querySelector('[data-net-send-error]');
	const showError = message => {
		error.textContent = message;
		error.hidden = !message;
	};

	const showButton = () => {
		if (button && form && modalElement && (canNxdock || canNx2)) {
			button.hidden = false;
		}
	};
	fetch('/api/install/game/' + encodeURIComponent(titleId), { headers: { Accept: 'application/json' } })
		.then(response => (response.ok ? response.json() : null))
		.then(answer => {
			if (!answer) {
				return;
			}
			game = { consoles: answer.consoles || [], files: answer.files || [] };
			showOnConsoles();
			canNxdock = !!answer.enabled && game.consoles.length > 0 && game.files.length > 0;
			showButton();
		})
		.catch(() => {});
	// a NUP of the game goes to nx2-flow too: not in the demo, and only for who may send
	if (button) {
		fetch('/api/nx2flow/game/' + encodeURIComponent(titleId), { headers: { Accept: 'application/json' } })
			.then(response => (response.ok ? response.json() : null))
			.then(answer => {
				if (!answer || answer.demo) {
					return;
				}
				nx2 = { files: answer.files || [], address: answer.address || '' };
				canNx2 = nx2.files.length > 0;
				showButton();
			})
			.catch(() => {});
	}

	// the target chosen, and what shows for it
	const target = () => {
		if (!canNxdock) {
			return 'nx2flow';
		}
		if (!canNx2) {
			return 'nxdock';
		}
		const checked = form.querySelector('[data-net-target]:checked');
		return checked ? checked.value : 'nxdock';
	};
	const showTarget = () => {
		const chosen = target();
		form.querySelectorAll('[data-net-target-part]').forEach(part => {
			part.hidden = part.dataset.netTargetPart !== chosen;
		});
		const title = document.getElementById('netSendTitle');
		if (title) {
			title.querySelector('.bi').className = 'bi ' + (chosen === 'nx2flow' ? 'bi-router' : 'bi-nintendo-switch') + ' me-2';
		}
	};
	// an update or a DLC chosen on its own: nx2-flow only uses the ones inside the NUP of the game
	const showLoose = () => {
		const chosen = form.querySelector('[data-nx2flow-files] input:checked');
		const file = chosen && nx2.files.find(each => each.id === chosen.value);
		form.querySelector('[data-nx2flow-loose]').hidden = !(file && file.loose);
	};
	const fillNx2 = () => {
		const choice = form.querySelector('[data-net-target-choice]');
		choice.hidden = !(canNxdock && canNx2);
		const remembered = netSendTargetRemembered();
		form.querySelectorAll('[data-net-target]').forEach(radio => {
			radio.checked = radio.value === remembered;
			radio.onchange = () => {
				netSendTargetRemember(radio.value);
				showError('');
				showTarget();
			};
		});
		const address = form.querySelector('[data-nx2flow-address]');
		if (!address.value) {
			address.value = nx2.address;
		}
		const list = form.querySelector('[data-nx2flow-files]');
		list.replaceChildren(...nx2.files.map((file, i) => {
			const item = document.createElement('div');
			item.className = 'form-check';
			const radio = document.createElement('input');
			radio.type = 'radio';
			radio.name = 'nx2flow_file';
			radio.className = 'form-check-input';
			radio.id = 'nx2flow-file-' + i;
			radio.value = file.id;
			radio.checked = i === 0;
			const label = document.createElement('label');
			label.className = 'form-check-label small';
			label.htmlFor = radio.id;
			const name = document.createElement('span');
			name.className = 'text-break';
			name.textContent = file.name;
			const size = document.createElement('span');
			size.className = 'net-send-size text-body-secondary';
			size.textContent = formatSize(file.size);
			label.append(name, size);
			item.append(radio, label);
			radio.onchange = showLoose;
			return item;
		}));
		showLoose();
		showTarget();
	};

	if (!button || !form || !modalElement) {
		return;
	}
	button.addEventListener('click', () => {
		showError('');
		fillDialog();
		fillNx2();
		Modal.getOrCreateInstance(modalElement).show();
	});
	// with nx2-flow alone, the address is what there is to type
	modalElement.addEventListener('shown.bs.modal', () => {
		const address = form.querySelector('[data-nx2flow-address]');
		if (target() === 'nx2flow' && !address.value) {
			address.focus();
		}
	});
	const sendToNx2 = () => {
		const address = form.querySelector('[data-nx2flow-address]');
		const file = form.querySelector('[data-nx2flow-files] input:checked');
		if (!address.value.trim()) {
			showError(t('Type the address of the console, as the Transfer screen of nx2-flow shows it (for example 192.168.1.50).'));
			address.focus();
			return;
		}
		showError('');
		const send = form.querySelector('[data-net-send-go]');
		send.disabled = true;
		const body = { titleId, fileId: file ? file.value : '', address: address.value.trim(), cover: form.querySelector('[data-nx2flow-cover]').checked };
		apiFetch('/api/nx2flow/send', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
			.then(response => readJSON(response).then(json => {
				if (!response.ok) {
					throw new Error(netErrorMessage(response, json));
				}
				nx2.address = body.address;
				Modal.getOrCreateInstance(modalElement).hide();
				showToast('alert-success', 'bi-check-circle-fill', '', t('Sending to nx2-flow: the progress shows in Tasks.'));
			}))
			.catch(e => showError(e.message || t('Could not reach the server.')))
			.finally(() => {
				send.disabled = false;
			});
	};
	form.addEventListener('submit', e => {
		e.preventDefault();
		if (target() === 'nx2flow') {
			sendToNx2();
			return;
		}
		const keyId = form.querySelector('[data-net-send-console]').value;
		const fileIds = [...form.querySelectorAll('[data-net-send-files] input:checked')].map(box => box.value);
		if (!fileIds.length) {
			showError(t('Tick at least one file.'));
			return;
		}
		showError('');
		const send = form.querySelector('[data-net-send-go]');
		send.disabled = true;
		const compressed = compressedMode();
		apiFetch('/api/install/push', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ keyId, fileIds, compressed }) })
			.then(response => readJSON(response).then(json => {
				if (!response.ok) {
					throw new Error(netErrorMessage(response, json));
				}
				Modal.getOrCreateInstance(modalElement).hide();
				showToast('alert-success', 'bi-check-circle-fill', '', json.preparing
					? t('Sent: the console asks before installing. What is compressed first goes once it is ready (see Tasks).')
					: t('Sent: the console asks before installing.'));
			}))
			.catch(e => showError(e.message || t('Could not reach the server.')))
			.finally(() => {
				send.disabled = false;
			});
	});
}

// Cancel of a send to nx2-flow in Tasks, also on the rows the live list draws again
document.addEventListener('click', e => {
	const button = e.target.closest('[data-nx2flow-cancel]');
	if (!button) {
		return;
	}
	button.disabled = true;
	apiFetch('/api/nx2flow/cancel/' + encodeURIComponent(button.dataset.nx2flowCancel), { method: 'POST' })
		.then(response => readJSON(response).then(json => {
			if (!response.ok) {
				throw new Error(netErrorMessage(response, json));
			}
		}))
		.catch(error => {
			button.disabled = false;
			showToast('alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), error.message);
		});
});

document.addEventListener('DOMContentLoaded', () => {
	initNetSend();
	fillConsoleInventories(document);
	fillConsoleQueues(document);
});
