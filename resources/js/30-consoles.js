// The page Consoles: Bring up to date (the files the library has newer go to the console, which
// asks before installing them), Remove from console (asked in a dialog; the console asks too, and
// copies the saves of the game to the library first), and the page asked again while the library
// is still counting what it has newer.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

function consoleUpdateMessage(json) {
	if (!json.queued) {
		return t('Nothing to send: the console has everything your library has.');
	}
	const parts = [json.queued === 1 ? t('Sent 1 file: the console asks before installing.') : t('Sent %v files: the console asks before installing.', json.queued)];
	if (json.preparing) {
		parts.push(t('What is compressed first goes once it is ready (see Tasks).'));
	}
	if (json.left) {
		parts.push(t('%v files are left: bring it up to date again once these are installed.', json.left));
	}
	return parts.join(' ');
}

function initConsoleUpdate() {
	document.querySelectorAll('[data-console-update]').forEach(button => {
		button.addEventListener('click', () => {
			button.disabled = true;
			const compressed = netSendModeRemembered() === 'compressed';
			apiFetch('/api/install/consoles/' + encodeURIComponent(button.dataset.consoleUpdate) + '/update', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ compressed }),
			})
				.then(response => readJSON(response).then(json => {
					if (!response.ok) {
						throw new Error(netErrorMessage(response, json));
					}
					showToast('alert-success', 'bi-check-circle-fill', '', consoleUpdateMessage(json));
					const icon = document.createElement('span');
					icon.className = 'bi bi-check2 me-2';
					icon.setAttribute('aria-hidden', 'true');
					button.replaceChildren(icon, document.createTextNode(t('Sent')));
				}))
				.catch(e => {
					button.disabled = false;
					showToast('alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), e.message || t('Could not reach the server.'));
				});
		});
	});
}

function initConsoleRemove() {
	const modalElement = document.getElementById('consoleRemove');
	const list = document.querySelector('[data-console-list]');
	if (!modalElement || !list) {
		return;
	}
	const form = modalElement.querySelector('[data-console-remove-form]');
	const what = form.querySelector('[data-console-remove-what]');
	const error = form.querySelector('[data-console-remove-error]');
	const go = form.querySelector('[data-console-remove-go]');
	let row = null;

	list.addEventListener('click', e => {
		const button = e.target.closest('[data-console-remove]');
		if (!button || button.disabled) {
			return;
		}
		row = button.closest('[data-console-game]');
		what.textContent = what.dataset.text.replace('%v', row.dataset.consoleGameName).replace('%v', list.dataset.consoleName);
		error.hidden = true;
		go.disabled = false;
		Modal.getOrCreateInstance(modalElement).show();
	});

	form.addEventListener('submit', e => {
		e.preventDefault();
		if (!row) {
			return;
		}
		go.disabled = true;
		apiFetch('/api/install/consoles/' + encodeURIComponent(list.dataset.consoleList) + '/remove', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ titleId: row.dataset.consoleGame }),
		})
			.then(response => readJSON(response).then(json => {
				if (!response.ok) {
					throw new Error(netErrorMessage(response, json));
				}
				Modal.getOrCreateInstance(modalElement).hide();
				showToast('alert-success', 'bi-check-circle-fill', '', t('Asked: the console asks before removing it.'));
				// asked: the row says so until the console answers
				row.classList.add('is-removing');
				row.querySelector('[data-console-remove]').disabled = true;
				if (!row.querySelector('[data-console-asked]')) {
					const chip = document.createElement('span');
					chip.className = 'chip chip-console';
					chip.dataset.consoleAsked = '';
					const icon = document.createElement('span');
					icon.className = 'bi bi-hourglass-split';
					icon.setAttribute('aria-hidden', 'true');
					chip.append(icon, document.createTextNode(t('Asked to remove')));
					row.querySelector('.console-row-name').append(' ', chip);
				}
			}))
			.catch(e => {
				error.textContent = e.message || t('Could not reach the server.');
				error.hidden = false;
				go.disabled = false;
			});
	});
}

// while the library counts what it has newer, the page asks again in a moment
function initConsolesCounting() {
	if (!document.querySelector('[data-consoles-counting]')) {
		return;
	}
	const again = () => {
		if (document.hidden || document.querySelector('.modal.show')) {
			setTimeout(again, 5000);
			return;
		}
		window.location.reload();
	};
	setTimeout(again, 5000);
}

document.addEventListener('DOMContentLoaded', () => {
	initConsoleUpdate();
	initConsoleRemove();
	initConsolesCounting();
});
