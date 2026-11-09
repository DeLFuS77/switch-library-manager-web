// A console asking to be paired: a notice on top of any page, for the users who may download,
// with the code the console shows and Accept. Accepted, the app makes a key of the role Console
// for the user and gives it to the console, which signs in by itself from then on.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

const NET_PAIR_POLL_MS = 4000;

function initNetPair() {
	const main = document.querySelector('#main > .container-xxl');
	if (!main) {
		return;
	}
	const shown = new Map(); // id of the pairing -> its notice
	let timer = 0;
	let stopped = false;

	const notice = pairing => {
		const box = document.createElement('div');
		box.className = 'net-pair mt-3';
		box.setAttribute('role', 'status');
		box.setAttribute('aria-live', 'polite');

		const icon = document.createElement('span');
		icon.className = 'bi bi-nintendo-switch flex-shrink-0';
		icon.setAttribute('aria-hidden', 'true');
		box.appendChild(icon);

		const text = document.createElement('div');
		text.className = 'net-pair-text';
		const title = document.createElement('div');
		const strong = document.createElement('strong');
		strong.textContent = t('A console wants to connect');
		title.appendChild(strong);
		title.appendChild(document.createTextNode(' · ' + pairing.console + ' · ' + pairing.ip));
		const check = document.createElement('div');
		check.className = 'small text-body-secondary';
		check.appendChild(document.createTextNode(t('Check that the console shows this code:') + ' '));
		const code = document.createElement('span');
		code.className = 'net-pair-code';
		code.textContent = pairing.code;
		check.appendChild(code);
		text.append(title, check);
		box.appendChild(text);

		const refuse = document.createElement('button');
		refuse.type = 'button';
		refuse.className = 'btn btn-sm btn-outline-secondary';
		refuse.textContent = t('Refuse');
		const accept = document.createElement('button');
		accept.type = 'button';
		accept.className = 'btn btn-sm net-pair-accept';
		const enable = !pairing.enabled && pairing.canEnable;
		accept.textContent = enable ? t('Accept and turn on') : t('Accept');
		box.append(refuse, accept);

		if (!pairing.enabled && !pairing.canEnable) {
			const off = document.createElement('div');
			off.className = 'net-pair-off';
			off.textContent = t('Install over the network is off: an administrator has to turn it on before the console can install.');
			box.appendChild(off);
		}

		const decide = action => {
			refuse.disabled = accept.disabled = true;
			const body = new URLSearchParams({ action, enable: enable ? 'true' : 'false' });
			apiFetch('/api/install/pairs/' + encodeURIComponent(pairing.id), { method: 'POST', body })
				.then(response => readJSON(response).then(json => ({ response, json })))
				.then(({ response, json }) => {
					box.remove();
					shown.delete(pairing.id);
					if (!response.ok) {
						const error = json.globalError || {};
						showToast('alert-danger', 'bi-exclamation-triangle-fill', error.strongMessage || t('Error!'), error.message || response.statusText);
					} else if (action === 'accept') {
						showToast('alert-success', 'bi-check-circle-fill', '', t('The console %v is connected.', json.name || pairing.console));
					}
				})
				.catch(() => {
					refuse.disabled = accept.disabled = false;
					showToast('alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), t('Could not reach the server.'));
				});
		};
		refuse.addEventListener('click', () => decide('reject'));
		accept.addEventListener('click', () => decide('accept'));
		return box;
	};

	const poll = () => {
		timer = 0;
		if (stopped || document.hidden) {
			return;
		}
		fetch('/api/install/pairs', { headers: { Accept: 'application/json' } })
			.then(response => {
				if (!response.ok) {
					// signed out, or a page that is not of the app: stop asking
					stopped = response.status === 401 || response.status === 403 || response.status === 404;
					return [];
				}
				return response.json();
			})
			.then(list => {
				const waiting = new Set();
				(Array.isArray(list) ? list : []).forEach(pairing => {
					waiting.add(pairing.id);
					if (!shown.has(pairing.id)) {
						const box = notice(pairing);
						shown.set(pairing.id, box);
						main.prepend(box);
					}
				});
				// a console that stopped waiting, or was decided in another tab
				shown.forEach((box, id) => {
					if (!waiting.has(id)) {
						box.remove();
						shown.delete(id);
					}
				});
			})
			.catch(() => {})
			.finally(() => {
				if (!stopped && !timer) {
					timer = setTimeout(poll, NET_PAIR_POLL_MS);
				}
			});
	};

	document.addEventListener('visibilitychange', () => {
		if (!document.hidden && !timer && !stopped) {
			poll();
		}
	});
	poll();
}

document.addEventListener('DOMContentLoaded', initNetPair);
