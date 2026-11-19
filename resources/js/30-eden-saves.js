// The page Saves, Eden on the computers of the user: the save a game keeps when both changed (Keep
// this one), which console account each profile of Eden is, and the switch of Eden on the computer
// the app runs on.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

// edenPost sends a change and gives its answer, or throws its error.
function edenPost(url, body) {
	return apiFetch(url, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
		body: JSON.stringify(body),
	}).then(response => readJSON(response).then(json => {
		if (!response.ok) {
			throw new Error((json.globalError && json.globalError.message) || response.statusText || t('Error!'));
		}
		return json;
	}));
}

function edenFailed(e) {
	showToast('alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), e.message);
}

function initEdenSaves() {
	const conflicts = document.querySelector('[data-eden-conflicts]');
	if (conflicts) {
		conflicts.addEventListener('click', event => {
			const button = event.target.closest('[data-eden-keep]');
			if (!button) {
				return;
			}
			const card = button.closest('[data-eden-conflict]');
			const buttons = card.querySelectorAll('[data-eden-keep]');
			buttons.forEach(other => { other.disabled = true; });
			edenPost(card.dataset.edenConflict, { keep: button.dataset.edenKeep })
				.then(() => {
					card.remove();
					if (!conflicts.querySelector('[data-eden-conflict]')) {
						conflicts.remove();
					}
					showToast('alert-success', 'bi-check-circle-fill', '', t('Chosen: it is applied the next time the emulator is closed.'));
				})
				.catch(e => {
					buttons.forEach(other => { other.disabled = false; });
					edenFailed(e);
				});
		});
	}

	const section = document.querySelector('[data-eden]');
	if (!section) {
		return;
	}
	section.querySelectorAll('[data-eden-profile]').forEach(select => {
		let before = select.value;
		select.addEventListener('change', () => {
			select.disabled = true;
			edenPost('/api/saves/profiles', { device: select.dataset.device, profile: select.dataset.profile, account: select.value })
				.then(() => { before = select.value; })
				.catch(e => {
					select.value = before;
					edenFailed(e);
				})
				.finally(() => { select.disabled = false; });
		});
	});
	const local = section.querySelector('[data-eden-local]');
	if (local) {
		local.addEventListener('change', () => {
			const on = local.checked;
			local.disabled = true;
			edenPost('/api/saves/eden-local', { on })
				.catch(e => {
					local.checked = !on;
					edenFailed(e);
				})
				.finally(() => { local.disabled = false; });
		});
	}
}

document.addEventListener('DOMContentLoaded', initEdenSaves);
