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

// The welcome of the wizard: a soft chord that swells under chimes going up, with an echo. It
// plays when the wizard starts, or at the first touch when the browser keeps the page silent
// until then (a wizard that opens by itself).
function playWizardWelcome() {
	try {
		soundContext = soundContext || new (window.AudioContext || window.webkitAudioContext)();
		if (soundContext.state === 'suspended') {
			soundContext.resume();
		}
		const now = soundContext.currentTime + .05;
		const out = soundContext.createGain();
		out.gain.value = .5;
		const echo = soundContext.createDelay();
		echo.delayTime.value = .32;
		const feedback = soundContext.createGain();
		feedback.gain.value = .35;
		echo.connect(feedback).connect(echo);
		out.connect(soundContext.destination);
		echo.connect(soundContext.destination);
		const voice = (frequency, start, length, type, volume, attack, wet) => {
			const oscillator = soundContext.createOscillator();
			const gain = soundContext.createGain();
			oscillator.type = type;
			oscillator.frequency.value = frequency;
			gain.gain.setValueAtTime(.0001, now + start);
			gain.gain.exponentialRampToValueAtTime(volume, now + start + attack);
			gain.gain.exponentialRampToValueAtTime(.0001, now + start + length);
			oscillator.connect(gain).connect(out);
			if (wet) {
				gain.connect(echo);
			}
			oscillator.start(now + start);
			oscillator.stop(now + start + length + .05);
		};
		// the chord under it (D, A, E, F sharp), slow in and slow out
		[146.83, 220, 329.63, 369.99].forEach(frequency => {
			voice(frequency, 0, 4.2, 'sine', .05, 1.2, false);
			voice(frequency * 1.003, 0, 4.2, 'triangle', .015, 1.4, false);
		});
		// chimes going up, then two high notes that settle
		[[440, .25], [587.33, .5], [739.99, .75], [880, 1], [1318.51, 1.45], [1174.66, 1.9]].forEach(([frequency, start], i) => {
			const volume = i < 4 ? .11 : .08;
			voice(frequency, start, 1.8, 'sine', volume, .008, true);
			voice(frequency * 2, start, .6, 'sine', volume / 4, .005, true);
		});
	} catch (e) {
		// no sound in this browser
	}
}

// The music of the wizard: a slow bossa nova of soft chords played short and ahead of the beat,
// over a bass on one and the "and" of two, with a quiet wooden click. It is made here while the
// wizard is open, starts after the welcome, fades out when the wizard closes or the page is
// hidden, and can be turned off (remembered by the browser).
const WIZARD_MUSIC_KEY = 'slm-wizard-music';
let wizardMusic = null;

function wizardMusicOn() {
	try {
		return localStorage.getItem(WIZARD_MUSIC_KEY) !== 'off';
	} catch (e) {
		return true;
	}
}

function startWizardMusic() {
	if (wizardMusic || document.hidden) {
		return;
	}
	try {
		soundContext = soundContext || new (window.AudioContext || window.webkitAudioContext)();
		if (soundContext.state === 'suspended') {
			soundContext.resume();
		}
		const context = soundContext;
		const note = midi => 440 * 2 ** ((midi - 69) / 12);
		const out = context.createGain();
		out.gain.setValueAtTime(.0001, context.currentTime);
		out.gain.exponentialRampToValueAtTime(.7, context.currentTime + 5);
		const soft = context.createBiquadFilter();
		soft.type = 'lowpass';
		soft.frequency.value = 2400;
		soft.connect(out).connect(context.destination);
		const voice = (frequency, start, length, type, volume, attack) => {
			const oscillator = context.createOscillator();
			const gain = context.createGain();
			oscillator.type = type;
			oscillator.frequency.value = frequency;
			gain.gain.setValueAtTime(.0001, start);
			gain.gain.exponentialRampToValueAtTime(volume, start + attack);
			gain.gain.exponentialRampToValueAtTime(.0001, start + length);
			oscillator.connect(gain).connect(soft);
			oscillator.start(start);
			oscillator.stop(start + length + .05);
		};
		// the wooden click: a very short burst of noise through a narrow filter
		const noise = context.createBuffer(1, context.sampleRate / 10, context.sampleRate);
		const samples = noise.getChannelData(0);
		for (let i = 0; i < samples.length; i++) {
			samples[i] = Math.random() * 2 - 1;
		}
		const click = start => {
			const source = context.createBufferSource();
			const filter = context.createBiquadFilter();
			const gain = context.createGain();
			source.buffer = noise;
			filter.type = 'bandpass';
			filter.frequency.value = 1900;
			gain.gain.setValueAtTime(.02, start);
			gain.gain.exponentialRampToValueAtTime(.0001, start + .045);
			source.connect(filter).connect(gain).connect(out);
			source.start(start);
			source.stop(start + .07);
		};
		// D major 9, B minor 9, G major 9, A 6 sus: four bars that turn round and round
		const chords = [[50, 57, 61, 64, 66], [47, 54, 57, 61, 62], [43, 50, 54, 57, 59], [45, 52, 59, 62, 66]];
		// each bar has 16 steps: where the bass plays (and for how many steps), the short chords and the clicks
		const bass = [[0, 5], [6, 2], [8, 5], [14, 2]];
		const stabs = [3, 6, 10, 13];
		const clicks = [0, 3, 6, 10, 12];
		const step = 60 / 72 / 4;
		let next = context.currentTime + .3;
		let count = 0;
		const schedule = () => {
			while (next < context.currentTime + 1.6) {
				const chord = chords[Math.floor(count / 16) % chords.length];
				const at = count % 16;
				bass.filter(([start]) => start === at).forEach(([, length]) => {
					voice(note(chord[0] - 12), next, Math.max(length * step, .5) + .3, 'sine', .05, .04);
				});
				if (stabs.includes(at)) {
					chord.slice(1).forEach(midi => voice(note(midi), next, .55, 'triangle', .02, .02));
				}
				if (clicks.includes(at)) {
					click(next);
				}
				next += step;
				count++;
			}
		};
		schedule();
		const timer = setInterval(schedule, 400);
		wizardMusic = {
			stop() {
				clearInterval(timer);
				const now = context.currentTime;
				out.gain.cancelScheduledValues(now);
				out.gain.setValueAtTime(Math.max(out.gain.value, .0001), now);
				out.gain.exponentialRampToValueAtTime(.0001, now + 1.2);
				setTimeout(() => out.disconnect(), 1500);
			},
		};
	} catch (e) {
		// no sound in this browser
	}
}

function stopWizardMusic() {
	if (wizardMusic) {
		wizardMusic.stop();
		wizardMusic = null;
	}
}

// the music stops while the page is not seen, and comes back with it if the wizard is open
document.addEventListener('visibilitychange', () => {
	if (document.hidden) {
		stopWizardMusic();
	} else if (document.querySelector('#setupWizard.show') && wizardMusicOn() && navigator.userActivation?.hasBeenActive !== false) {
		startWizardMusic();
	}
});

function wizardWelcome(element) {
	const begin = () => {
		playWizardWelcome();
		if (wizardMusicOn()) {
			startWizardMusic();
		}
	};
	const silent = navigator.userActivation && !navigator.userActivation.hasBeenActive;
	if (!silent) {
		begin();
		return;
	}
	const first = () => {
		element.removeEventListener('pointerdown', first);
		element.removeEventListener('keydown', first);
		begin();
	};
	element.addEventListener('pointerdown', first);
	element.addEventListener('keydown', first);
}

function openWizard(auto) {
	const existing = document.getElementById('setupWizard');
	if (existing) {
		Modal.getOrCreateInstance(existing).show();
		wizardWelcome(existing);
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
			wizardWelcome(element);
		})
		.catch(() => {
			// not an administrator, or the server could not be reached
		})
		.finally(() => {
			wizardLoading = null;
		});
	return wizardLoading;
}

// A small tree of folders that shows what an order of the files does: the rows of the start move
// to their place in the end, each row found by its id, so a file slides into its folder.
function wizardTree(box, states) {
	const rows = box.querySelector('[data-wizard-tree-rows]');
	const caption = box.querySelector('[data-wizard-tree-caption]');
	const shown = {};
	const height = 25;
	const indent = 18;
	const icons = { folder: 'bi-folder-fill', sub: 'bi-folder', base: 'bi-controller', update: 'bi-arrow-up-circle-fill', dlc: 'bi-puzzle' };
	const draw = (state, moving = []) => {
		const ids = new Set(state.rows.map(row => row.id));
		state.rows.forEach((row, i) => {
			let line = shown[row.id];
			if (!line) {
				line = document.createElement('div');
				line.className = 'wizard-tree-row is-hidden';
				line.append(Object.assign(document.createElement('span'), { className: 'bi' }), Object.assign(document.createElement('span'), { className: 'wizard-tree-name' }));
				line.style.transform = `translate(${row.depth * indent}px, ${i * height}px)`;
				rows.appendChild(line);
				shown[row.id] = line;
				void line.offsetWidth;
			}
			line.className = `wizard-tree-row is-${row.kind}${moving.includes(row.id) ? ' is-moving' : ''}${row.renamed ? ' is-renamed' : ''}`;
			line.firstChild.className = `bi ${icons[row.kind]}`;
			line.lastChild.textContent = row.name;
			if (row.badge) {
				line.lastChild.append(' ', Object.assign(document.createElement('span'), { className: `wizard-tree-badge${row.badgeNew ? ' is-new' : ''}`, textContent: row.badge }));
			}
			line.style.transform = `translate(${row.depth * indent}px, ${i * height}px)`;
		});
		Object.keys(shown).forEach(id => shown[id].classList.toggle('is-hidden', !ids.has(id)));
		rows.style.height = `${state.rows.length * height}px`;
		caption.textContent = state.text;
	};
	let timer;
	const play = () => {
		clearTimeout(timer);
		const [start, end] = states();
		draw(start);
		const still = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
		timer = setTimeout(() => {
			draw(end, end.moving || []);
			timer = setTimeout(() => draw(end), 800);
		}, still ? 0 : 900);
	};
	box.querySelector('[data-wizard-tree-again]').addEventListener('click', play);
	return play;
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
	// the trees of folders of the steps, played when their step is shown
	const trees = {};

	for (let i = 0; i <= last; i++) {
		const dot = document.createElement('li');
		dot.className = 'wizard-dot';
		dots.appendChild(dot);
	}

	const showAlert = message => {
		alert.textContent = message || '';
		alert.hidden = !message;
		if (message) {
			alert.scrollIntoView({ block: 'nearest' });
		}
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
		if (step.dataset.step === 'keys' && previous !== step) {
			checkKeys();
		}
		if (previous !== step) {
			trees[step.dataset.step]?.();
		}
		if (step.dataset.step === 'automation') {
			updateNeeds();
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
		themeButtons.forEach(button => {
			const chosen = button.dataset.wizardTheme === theme;
			button.classList.toggle('is-active', chosen);
			button.setAttribute('aria-checked', String(chosen));
		});
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
			showAlert(say('noFolder'));
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
		})]).then(results => {
			const ok = results.every(Boolean);
			if (ok) {
				showAlert('');
			}
			return ok;
		});
	};
	element.querySelector('[data-wizard-check-folders]').addEventListener('click', checkFolders);
	folderField.addEventListener('input', () => showAlert(''));

	// the folders of the server, to choose one instead of writing it
	const browser = element.querySelector('[data-wizard-browser]');
	const browseButton = element.querySelector('[data-wizard-browse]');
	const browserPath = element.querySelector('[data-wizard-browser-path]');
	const browserList = element.querySelector('[data-wizard-browser-list]');
	const browserPlaces = element.querySelector('[data-wizard-browser-places]');
	const browserUp = element.querySelector('[data-wizard-browser-up]');
	let browsing = '';
	const browse = path => fetch(`/wizard/folders?path=${encodeURIComponent(path)}`, { cache: 'no-store' })
		.then(response => (response.ok ? response.json() : Promise.reject(response.status)))
		.then(result => {
			browsing = result.path || '';
			browserPath.textContent = result.error || browsing;
			browserPath.classList.toggle('text-danger', Boolean(result.error));
			browserUp.disabled = !result.parent;
			browserUp.dataset.path = result.parent || '';
			browserPlaces.replaceChildren(...(result.places || []).map(place => {
				const option = new Option(place, place);
				option.selected = browsing === place || browsing.startsWith(place.endsWith('/') || place.endsWith('\\') ? place : `${place}/`);
				return option;
			}));
			const items = (result.folders || []).map(name => {
				const item = document.createElement('li');
				const button = document.createElement('button');
				button.type = 'button';
				button.className = 'wizard-browser-item';
				const mark = document.createElement('span');
				mark.className = 'bi bi-folder-fill';
				mark.setAttribute('aria-hidden', 'true');
				button.append(mark, document.createTextNode(` ${name}`));
				button.addEventListener('click', () => browse(`${browsing}${/[\\/]$/.test(browsing) ? '' : (browsing.includes('\\') ? '\\' : '/')}${name}`));
				item.appendChild(button);
				return item;
			});
			if (items.length === 0 && !result.error) {
				const empty = document.createElement('li');
				empty.className = 'text-body-secondary small';
				empty.textContent = say('noFolders');
				items.push(empty);
			}
			if (result.more) {
				const more = document.createElement('li');
				more.className = 'text-body-secondary small';
				more.textContent = say('more', (result.folders || []).length);
				items.push(more);
			}
			browserList.replaceChildren(...items);
			browserList.scrollTop = 0;
		})
		.catch(() => {
			browserPath.textContent = say('unreachable');
		});
	const closeBrowser = () => {
		browser.hidden = true;
		browseButton.setAttribute('aria-expanded', 'false');
	};
	browseButton.addEventListener('click', () => {
		if (!browser.hidden) {
			closeBrowser();
			return;
		}
		browser.hidden = false;
		browseButton.setAttribute('aria-expanded', 'true');
		// it starts at the last folder written, or at the first place of the server
		const written = folderField.value.split('\n').map(line => line.trim()).filter(Boolean);
		browse(written[written.length - 1] || '');
	});
	browserUp.addEventListener('click', () => browse(browserUp.dataset.path || ''));
	browserPlaces.addEventListener('change', () => browse(browserPlaces.value));
	element.querySelector('[data-wizard-browser-cancel]').addEventListener('click', closeBrowser);
	element.querySelector('[data-wizard-browser-use]').addEventListener('click', () => {
		if (!browsing) {
			return;
		}
		const written = folderField.value.split('\n').map(line => line.trim()).filter(Boolean);
		if (!written.includes(browsing)) {
			written.push(browsing);
		}
		folderField.value = written.join('\n');
		closeBrowser();
		checkFolders();
	});

	// the interval of the checks of the folders only counts while they are watched
	const watchSwitch = form.elements.watch_folders;
	const watchInterval = element.querySelector('[data-wizard-watch-interval]');
	const updateWatch = () => {
		watchInterval.classList.toggle('is-off', !watchSwitch.checked);
		// inert, not disabled: a disabled field is not saved
		watchInterval.toggleAttribute('inert', !watchSwitch.checked);
	};
	watchSwitch.addEventListener('change', updateWatch);
	updateWatch();

	// the level and the originals only count when the app compresses by itself
	const compressOptions = element.querySelector('[data-wizard-compress-options]');
	const updateCompress = () => {
		const manual = !form.elements.auto_compress.value;
		compressOptions.classList.toggle('is-off', manual);
		compressOptions.toggleAttribute('inert', manual);
	};
	[...form.elements.auto_compress].forEach(input => input.addEventListener('change', updateCompress));
	updateCompress();
	const keysResult = element.querySelector('[data-wizard-keys-result]');
	let keysFound = null;
	const checkKeys = () => check('keys', form.elements.prod_keys.value).then(result => {
		keysFound = result.ok;
		checkLine(keysResult, result);
	}).catch(() => {});
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

	// installing new versions by itself only counts while they are told
	const autoInstall = element.querySelector('[data-wizard-auto-install]');
	if (autoInstall) {
		const told = form.elements.check_for_updates;
		const updateInstall = () => {
			autoInstall.classList.toggle('is-off', !told.checked);
			autoInstall.toggleAttribute('inert', !told.checked);
		};
		told.addEventListener('change', updateInstall);
		updateInstall();
	}

	// the order of the files: a tree shows it, made of a game with a folder of its own (MK8) and
	// the loose files of another one
	const layoutOf = () => form.elements.organize_layout.value;
	const row = (id, kind, name, depth, more = {}) => ({ id, kind, name, depth, ...more });
	const orderStates = () => {
		const layout = layoutOf();
		const keep = form.elements.keep_game_folders.checked;
		const rename = form.elements.rename_files.checked;
		const empty = form.elements.delete_empty_folders.checked;
		const named = (plain, full) => (rename ? full : plain);
		const files = depth => [
			row('zb', 'base', named('zelda.nsp', 'The Legend of Zelda [01007EF00011E000][v0].nsp'), depth, { renamed: rename }),
			row('zu', 'update', named('zelda-update.nsp', 'The Legend of Zelda [01007EF00011E800][v131072].nsp'), depth, { renamed: rename }),
			row('zd', 'dlc', named('zelda-dlc.nsp', 'The Legend of Zelda (Expansion Pass)[01007EF00011F001][v0].nsp'), depth, { renamed: rename }),
		];
		const mk = named('mk8.nsp', 'Mario Kart 8 Deluxe [0100152000022000][v0].nsp');
		const start = { text: say('treeBefore'), rows: [row('mk', 'folder', 'MK8', 0), row('mkb', 'base', 'mk8.nsp', 1), row('dl', 'folder', 'downloads', 0), ...files(0)] };
		const mkFolder = row('mk', 'folder', keep ? 'MK8' : 'Mario Kart 8 Deluxe', 0);
		const downloads = empty && layout !== 'none' ? [] : [row('dl', 'folder', 'downloads', 0)];
		let rows;
		let text;
		if (layout === 'none') {
			rows = [row('mk', 'folder', 'MK8', 0), row('mkb', 'base', mk, 1, { renamed: rename }), ...downloads, ...files(0)];
			text = say(rename ? 'treeNoneRenamed' : 'treeNone');
		} else if (layout === 'folder') {
			rows = [mkFolder, row('mkb', 'base', mk, 1, { renamed: rename }), ...downloads, row('z', 'folder', 'The Legend of Zelda', 0), ...files(1)];
			text = say(keep ? 'treeKeep' : 'treeFolder');
		} else {
			const [zb, zu, zd] = files(2);
			rows = [mkFolder, row('mkB', 'sub', 'Base', 1), row('mkb', 'base', mk, 2, { renamed: rename }), ...downloads, row('z', 'folder', 'The Legend of Zelda', 0),
				row('zB', 'sub', 'Base', 1), zb, row('zU', 'sub', 'Updates', 1), zu, row('zD', 'sub', 'DLC', 1), zd];
			text = say('treeSub');
		}
		if (layout !== 'none' && empty) {
			text += ` ${say('treeEmpty')}`;
		}
		return [start, { rows, text, moving: ['mkb', 'zb', 'zu', 'zd'] }];
	};
	trees.organize = wizardTree(element.querySelector('[data-wizard-tree="organize"]'), orderStates);
	const keepFolders = element.querySelector('[data-wizard-keep-folders]');
	const updateOrder = () => {
		const loose = layoutOf() === 'none';
		keepFolders.classList.toggle('is-off', loose);
		keepFolders.toggleAttribute('inert', loose);
	};
	['organize_layout', 'keep_game_folders', 'rename_files', 'delete_empty_folders'].forEach(name => {
		const field = form.elements[name];
		(field instanceof RadioNodeList ? [...field] : [field]).forEach(input => input.addEventListener('change', () => {
			updateOrder();
			trees.organize();
		}));
	});
	updateOrder();

	// organizing by itself: only the new files, or the whole library
	const organizeStep = element.querySelector('[data-wizard-organize-step]');
	const scope = element.querySelector('[data-wizard-organize-scope]');
	const scopeStates = () => {
		const newOnly = form.elements.automation_organize_new_only.value === 'true';
		const update = (depth, more = {}) => row('zu', 'update', 'zelda-update.nsp', depth, { badge: say('new'), badgeNew: true, ...more });
		const start = { text: say('treeArrives'), rows: [row('mk', 'folder', 'MK8', 0), row('mkb', 'base', 'mk8.nsp', 1), row('z', 'folder', 'The Legend of Zelda', 0),
			row('zb', 'base', 'zelda.nsp', 1), row('old', 'base', 'odyssey.xci', 0), update(0)] };
		if (newOnly) {
			return [start, { text: say('treeNewOnly'), moving: ['zu'], rows: [row('mk', 'folder', 'MK8', 0), row('mkb', 'base', 'mk8.nsp', 1), row('z', 'folder', 'The Legend of Zelda', 0),
				row('zb', 'base', 'zelda.nsp', 1), update(1), row('old', 'base', 'odyssey.xci', 0, { badge: say('untouched') })] }];
		}
		return [start, { text: say('treeAll'), moving: ['zu', 'old'], rows: [row('mk', 'folder', 'MK8', 0), row('mkb', 'base', 'mk8.nsp', 1), row('z', 'folder', 'The Legend of Zelda', 0),
			row('zb', 'base', 'zelda.nsp', 1), update(1), row('s', 'folder', 'Super Mario Odyssey', 0), row('old', 'base', 'odyssey.xci', 1)] }];
	};
	const playScope = wizardTree(element.querySelector('[data-wizard-tree="automation"]'), scopeStates);
	trees.automation = () => {
		if (organizeStep.checked) {
			playScope();
		}
	};
	const updateScope = () => {
		scope.hidden = !organizeStep.checked;
	};
	organizeStep.addEventListener('change', () => {
		updateScope();
		trees.automation();
	});
	[...form.elements.automation_organize_new_only].forEach(input => input.addEventListener('change', playScope));
	updateScope();

	// a step of the automations that needs the keys or a channel says so
	const channels = ['discord_webhook_url', 'webhook_url', 'telegram_bot_token'];
	const keysNeeded = element.querySelector('[data-wizard-keys-needed]');
	const channelNeeded = element.querySelector('[data-wizard-channel-needed]');
	const updateNeeds = () => {
		keysNeeded.hidden = !(element.querySelector('[data-wizard-needs-keys]').checked && keysFound === false);
		channelNeeded.hidden = !element.querySelector('[data-wizard-needs-channel]').checked || channels.some(name => form.elements[name].value.trim());
	};
	element.querySelectorAll('[data-wizard-needs-keys], [data-wizard-needs-channel]').forEach(input => input.addEventListener('change', updateNeeds));

	// a test notification with the channels written here, before they are saved
	const testButton = element.querySelector('[data-wizard-test-notification]');
	const testResult = element.querySelector('[data-wizard-test-result]');
	testButton.addEventListener('click', () => {
		testButton.disabled = true;
		testResult.className = 'wizard-check-line m-0';
		testResult.textContent = say('sending');
		const body = new URLSearchParams({ draft: '1' });
		[...channels, 'telegram_chat_id'].forEach(name => body.set(name, form.elements[name].value));
		fetch('/notifications/test', { method: 'POST', body })
			.then(response => response.json().catch(() => ({})).then(json => ({ ok: response.ok, json })))
			.then(({ ok, json }) => checkLine(testResult, { ok, message: ok ? json.message : ((json.globalError && json.globalError.message) || say('unreachable')) }))
			.catch(() => checkLine(testResult, { ok: false, message: say('unreachable') }))
			.finally(() => {
				testButton.disabled = false;
			});
	});

	// the measure of the server, as on the Settings page: it fills the hidden fields of the
	// threads, so saving keeps what it found
	initMeasure(element.querySelector('[data-measure]'));

	// the first administrator, checked by the server before anything is saved: a wrong password
	// made "Save and start" fail again and again without saying why
	const adminName = form.elements.wizard_admin_name;
	const adminPassword = form.elements.wizard_admin_password;
	const adminResult = element.querySelector('[data-wizard-admin-result]');
	let adminOk = true;
	const checkAdmin = () => {
		if (authEnabled || !adminName) {
			return Promise.resolve(true);
		}
		const name = adminName.value.trim();
		const password = adminPassword.value;
		if (!name && !password) {
			adminResult.replaceChildren();
			adminResult.className = 'wizard-check-line';
			adminOk = true;
			return Promise.resolve(true);
		}
		return fetch('/wizard/admin', { method: 'POST', body: new URLSearchParams({ name, password }) })
			.then(response => (response.ok ? response.json() : Promise.reject(response.status)))
			.then(result => {
				adminOk = result.ok;
				if (result.ok) {
					adminResult.replaceChildren();
					adminResult.className = 'wizard-check-line';
				} else {
					checkLine(adminResult, result);
				}
				return result.ok;
			})
			.catch(() => {
				adminOk = false;
				showAlert(say('unreachable'));
				return false;
			});
	};
	element.querySelector('[data-wizard-show-password]')?.addEventListener('change', e => {
		adminPassword.type = e.target.checked ? 'text' : 'password';
	});
	[adminName, adminPassword].forEach(field => field?.addEventListener('input', () => {
		adminResult.replaceChildren();
		showAlert('');
	}));

	// a step goes on only when what it asks is right
	const validate = () => {
		const step = steps[current].dataset.step;
		if (step === 'folders') {
			return checkFolders().then(ok => {
				if (!ok) {
					showAlert(folderResults.querySelector('.is-bad')?.textContent.trim() || say('noFolder'));
				}
				return ok;
			});
		}
		if (step === 'security') {
			return checkAdmin().then(ok => {
				if (!ok) {
					showAlert(adminResult.textContent.trim() || say('adminFailed'));
				}
				return ok;
			});
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
			const label = checked ? checked.closest('label').querySelector('.wizard-card-label') || checked.closest('label') : null;
			return label ? label.textContent.trim() : '';
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
		const regions = ['preferred_region', 'preferred_region_2', 'preferred_region_3']
			.filter(name => form.elements[name].value).map(choice);
		const order = [choice('organize_layout'), form.elements.rename_files.checked ? say('rename') : ''].filter(Boolean).join(' · ');
		const organizeNewOnly = form.elements.automation_organize.checked && form.elements.automation_organize_new_only.value === 'true';
		const rows = [
			['bi-translate', say('language'), choice('language'), 'welcome'],
			['bi-folder2-open', say('folders'), folders.join(', ') || say('none'), 'folders'],
			['bi-key', say('keys'), [form.elements.prod_keys.value.trim() || say('default'), keysFound === null ? '' : say(keysFound ? 'found' : 'notFound')].filter(Boolean).join(' · '), 'keys'],
			['bi-nintendo-switch', say('firmware'), form.elements.console_firmware.value.trim() || '—', 'console'],
			['bi-globe2', say('regions'), regions.join(', ') || say('none'), 'library'],
			['bi-arrow-repeat', say('sync'), choice('sync_interval_hours'), 'library'],
			['bi-moon-stars', say('background'), choice('background_hours'), 'background'],
			['bi-file-zip', say('compress'), [choice('auto_compress'), form.elements.auto_compress.value ? choice('auto_compress_level') : ''].filter(Boolean).join(' · '), 'compression'],
			['bi-folder-symlink', say('order'), order || say('none'), 'organize'],
			['bi-magic', say('automation'), master.checked && automation ? [say('stepsOn', automation), organizeNewOnly ? say('newOnly') : ''].filter(Boolean).join(' · ') : say('off'), 'automation'],
			['bi-bell', say('notifications'), notifications ? say('set') : say('none'), 'notifications'],
			['bi-shield-check', say('verify'), choice('verify_interval_days'), 'extras'],
			['bi-hourglass-split', say('igdb'), form.elements.igdb_client_id.value.trim() ? say('set') : say('none'), 'extras'],
		];
		if (!authEnabled) {
			rows.push(['bi-shield-lock', say('admin'), (adminOk && form.elements.wizard_admin_name.value.trim()) || say('none'), 'security']);
		}
		list.replaceChildren(...rows.map(([iconName, label, value, stepName], i) => {
			const row = document.createElement('li');
			const target = steps.findIndex(item => item.dataset.step === stepName);
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
			text.title = value;
			row.append(mark, name, text);
			if (target >= 0) {
				const edit = document.createElement('button');
				edit.type = 'button';
				edit.className = 'btn btn-sm btn-link wizard-summary-edit';
				edit.textContent = say('change');
				edit.setAttribute('aria-label', `${say('change')}: ${label}`);
				edit.addEventListener('click', () => show(target, -1));
				row.appendChild(edit);
			}
			return row;
		}));
	};

	back.addEventListener('click', () => show(current - 1, -1));
	skip.addEventListener('click', () => {
		if (steps[current].dataset.step === 'security' && adminName) {
			adminName.value = '';
			adminPassword.value = '';
			adminResult.replaceChildren();
			adminOk = true;
		}
		show(current + 1, 1);
	});
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
		checkAdmin().then(ok => {
			if (!ok) {
				finish.disabled = false;
				show(stepOf('wizard_admin_name'), -1);
				showAlert(adminResult.textContent.trim() || say('adminFailed'));
				return;
			}
			save();
		});
	});
	const save = () => {
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
						finish.hidden = current !== last;
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
								showAlert(say('adminFailed'));
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
				element.querySelector('[data-wizard-see-order]').hidden = layoutOf() === 'none' && !form.elements.rename_files.checked;
				// the covers of the games found are downloaded at once
				fetch('/wizard/state', { method: 'POST', body: new URLSearchParams({ state: 'done', covers: '1' }) });
				show(done, 1);
			})
			.catch(() => showAlert(say('unreachable')))
			.finally(() => {
				finish.disabled = false;
				finish.innerHTML = finish.dataset.label;
			});
	};

	// the music can be turned off and on; the choice is kept for the next time
	const musicButton = element.querySelector('[data-wizard-music]');
	const markMusic = () => {
		const on = wizardMusicOn();
		musicButton.setAttribute('aria-pressed', String(on));
		musicButton.querySelector('.bi').className = `bi ${on ? 'bi-volume-up-fill' : 'bi-volume-mute-fill'}`;
	};
	musicButton.addEventListener('click', () => {
		const on = !wizardMusicOn();
		try {
			localStorage.setItem(WIZARD_MUSIC_KEY, on ? 'on' : 'off');
		} catch (e) {
			// for this visit only
		}
		if (on) {
			startWizardMusic();
		} else {
			stopWizardMusic();
		}
		markMusic();
	});
	markMusic();

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
	element.addEventListener('hide.bs.modal', stopWizardMusic);
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

