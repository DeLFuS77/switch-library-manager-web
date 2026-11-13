// The app on a phone and the save vault.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

window.addEventListener('beforeinstallprompt', event => {
	event.preventDefault();
	installPrompt = event;
	showMobileStatus();
});
window.addEventListener('appinstalled', () => {
	installPrompt = null;
	showMobileStatus(true);
});

function showMobileStatus(justInstalled = false) {
	const section = document.querySelector('[data-mobile-app]');
	if (!section) {
		return;
	}
	const installed = justInstalled || window.matchMedia('(display-mode: standalone)').matches || window.navigator.standalone === true;
	const toggle = (selector, hidden) => {
		const element = section.querySelector(selector);
		if (element) {
			element.hidden = hidden;
		}
	};
	toggle('[data-mobile-installed]', !installed);
	toggle('[data-mobile-install]', installed || !installPrompt);
	toggle('[data-mobile-insecure]', installed || window.isSecureContext);
}

function initMobileApp() {
	const section = document.querySelector('[data-mobile-app]');
	if (!section) {
		return;
	}
	showMobileStatus();
	section.querySelector('[data-mobile-install-button]')?.addEventListener('click', () => {
		// the prompt can be shown once: a second tap does nothing
		const prompt = installPrompt;
		installPrompt = null;
		if (!prompt) {
			return;
		}
		prompt.prompt();
		prompt.userChoice.finally(() => showMobileStatus());
	});

	const address = window.location.origin + '/';
	const addressText = section.querySelector('[data-mobile-address]');
	if (addressText) {
		addressText.textContent = address;
	}
	const localNote = section.querySelector('[data-mobile-local]');
	if (localNote) {
		localNote.hidden = !['localhost', '127.0.0.1', '[::1]'].includes(window.location.hostname);
	}
	const qr = section.querySelector('[data-mobile-qr]');
	if (qr) {
		const code = qrcode(0, 'M');
		code.addData(address);
		code.make();
		qr.innerHTML = code.createSvgTag({ cellSize: 4, margin: 2, scalable: true });
	}

	// the guide of this device open, and first
	const agent = navigator.userAgent;
	const device = /iPhone|iPad|iPod/.test(agent) || (/Macintosh/.test(agent) && navigator.maxTouchPoints > 1) ? 'ios'
		: /Android/.test(agent) ? 'android' : 'desktop';
	const guide = section.querySelector('[data-mobile-guide="' + device + '"]');
	if (guide) {
		guide.open = true;
		guide.parentElement.prepend(guide);
	}
}

// Diagnostics > Open from other devices: the QR code of the address chosen, always in view. In a
// container the app does not know the addresses of the network, so the one of this page is
// shown, when it is not this computer.
function initOtherDevices() {
	const section = document.querySelector('[data-other-devices]');
	const box = section?.querySelector('[data-device-qr]');
	if (!box) {
		return;
	}
	const page = section.querySelector('[data-device-page]');
	if (page) {
		if (['localhost', '127.0.0.1', '[::1]'].includes(window.location.hostname)) {
			return;
		}
		const address = window.location.origin;
		page.querySelector('[data-device-url]').textContent = address;
		page.querySelector('[data-device-address]').dataset.deviceAddress = address;
		page.querySelector('[data-copy]').dataset.copy = address;
		page.hidden = false;
	}
	const show = address => {
		const code = qrcode(0, 'M');
		code.addData(address);
		code.make();
		box.innerHTML = code.createSvgTag({ cellSize: 4, margin: 0, scalable: true });
	};
	const picks = section.querySelectorAll('[data-device-address]');
	picks.forEach(pick => pick.addEventListener('click', () => {
		picks.forEach(other => {
			other.classList.toggle('is-on', other === pick);
			other.setAttribute('aria-pressed', other === pick ? 'true' : 'false');
		});
		show(pick.dataset.deviceAddress);
	}));
	const chosen = section.querySelector('[data-device-address].is-on') || picks[0];
	if (chosen) {
		show(chosen.dataset.deviceAddress);
	}
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

