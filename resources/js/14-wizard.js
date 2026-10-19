// Automations and the setup wizard.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

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
		// a folder twice or inside another one: told before the folders themselves
		const repeated = check('folders', folders.join('\n')).then(result => {
			if (result.ok) {
				return true;
			}
			const line = document.createElement('li');
			checkLine(line, result);
			folderResults.prepend(line);
			return false;
		}).catch(() => true);
		return Promise.all([repeated, ...folders.map(folder => {
			const line = document.createElement('li');
			line.className = 'wizard-check-line';
			line.textContent = `${folder} · ${say('checking')}`;
			folderResults.appendChild(line);
			return check('folder', folder).then(result => {
				checkLine(line, result);
				line.prepend(Object.assign(document.createElement('code'), { textContent: folder }), document.createTextNode(' '));
				return result.ok;
			}).catch(() => false);
		})]).then(results => results.every(Boolean));
	};
	element.querySelector('[data-wizard-check-folders]').addEventListener('click', checkFolders);
	const keysResult = element.querySelector('[data-wizard-keys-result]');
	const checkKeys = () => check('keys', form.elements.prod_keys.value).then(result => checkLine(keysResult, result)).catch(() => {});
	element.querySelector('[data-wizard-check-keys]').addEventListener('click', checkKeys);
	// keys uploaded from here: the field points to them, and they are checked at once
	setupKeysUpload(element.querySelector('[data-keys-upload]'), json => {
		form.elements.prod_keys.value = json.folder || '';
		checkKeys();
	});

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

