// Interface sounds, the couch mode and the game controller.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

function soundsOn() {
	try {
		return localStorage.getItem(SOUND_KEY) === '1';
	} catch (e) {
		return false;
	}
}

function playSound(name, force = false) {
	if (!force && !soundsOn()) {
		return;
	}
	try {
		soundContext = soundContext || new (window.AudioContext || window.webkitAudioContext)();
		if (soundContext.state === 'suspended') {
			soundContext.resume();
		}
		const now = soundContext.currentTime;
		SOUNDS[name].forEach(([frequency, start, length, type, volume]) => {
			const oscillator = soundContext.createOscillator();
			const gain = soundContext.createGain();
			oscillator.type = type;
			oscillator.frequency.setValueAtTime(frequency, now + start);
			gain.gain.setValueAtTime(.0001, now + start);
			gain.gain.exponentialRampToValueAtTime(volume, now + start + .01);
			gain.gain.exponentialRampToValueAtTime(.0001, now + start + length);
			oscillator.connect(gain).connect(soundContext.destination);
			oscillator.start(now + start);
			oscillator.stop(now + start + length + .02);
		});
	} catch (e) {
		// no sound in this browser
	}
}

function initSounds() {
	const toggles = [...document.querySelectorAll('[data-sound-toggle]')];
	const show = on => toggles.forEach(toggle => {
		toggle.setAttribute('aria-checked', String(on));
		const icon = toggle.querySelector('.bi');
		if (icon) {
			icon.classList.toggle('bi-volume-up', on);
			icon.classList.toggle('bi-volume-mute', !on);
		}
	});
	show(soundsOn());
	toggles.forEach(toggle => toggle.addEventListener('click', e => {
		// the menu stays open, to see the switch change
		e.stopPropagation();
		const on = !soundsOn();
		try {
			localStorage.setItem(SOUND_KEY, on ? '1' : '0');
		} catch (err) {
			// not kept on this device
		}
		show(on);
		playSound(on ? 'on' : 'off', true);
	}));
	// the keyboard moving from card to card ticks, like the controller
	document.addEventListener('focusin', e => {
		if (e.target.matches('.game-card .stretched-link:focus-visible')) {
			playSound('move');
		}
	});
	document.addEventListener('show.bs.modal', () => playSound('open'));
	document.addEventListener('hide.bs.modal', () => playSound('back'));
}

// The couch mode is the app for a TV, seen from afar and used with a controller: everything is
// bigger, the covers take the room, what is secondary goes and the page fills the screen. It is
// turned on from the menu of the header or with the Select button of a controller, and left
// with Select, Esc or the pill it shows.
const COUCH_KEY = 'slm-couch';

function couchOn() {
	return document.documentElement.classList.contains('is-couch');
}

function setCouch(on, fullscreen = true) {
	document.documentElement.classList.toggle('is-couch', on);
	try {
		localStorage.setItem(COUCH_KEY, on ? '1' : '0');
	} catch (e) {
		// only for this page
	}
	document.querySelectorAll('[data-couch-toggle]').forEach(toggle => toggle.setAttribute('aria-checked', String(on)));
	let pill = document.getElementById('couchPill');
	if (on && !pill) {
		pill = document.createElement('button');
		pill.type = 'button';
		pill.id = 'couchPill';
		pill.className = 'couch-pill';
		pill.innerHTML = '<span class="bi bi-tv" aria-hidden="true"></span>';
		pill.append(t('Couch mode: Select or Esc to leave'));
		// still being tried: it may change
		const testing = document.createElement('span');
		testing.className = 'chip chip-info';
		testing.textContent = t('In testing');
		pill.append(testing);
		pill.addEventListener('click', () => setCouch(false));
		document.body.append(pill);
	} else if (!on && pill) {
		pill.remove();
	}
	// the whole screen, when the browser lets it (it needs a click or a button of the controller)
	try {
		if (!fullscreen) {
			// back from another page: the screen stays as the browser left it
		} else if (on && !document.fullscreenElement && document.documentElement.requestFullscreen) {
			document.documentElement.requestFullscreen().catch(() => undefined);
		} else if (!on && document.fullscreenElement) {
			document.exitFullscreen().catch(() => undefined);
		}
	} catch (e) {
		// not in this browser
	}
}

function initCouchMode() {
	if (couchOn()) {
		// back from another page: the pill and the switches again
		setCouch(true, false);
	}
	document.querySelectorAll('[data-couch-toggle]').forEach(toggle => toggle.addEventListener('click', () => {
		playSound(couchOn() ? 'back' : 'open');
		setCouch(!couchOn());
	}));
	document.addEventListener('keydown', e => {
		if (e.key === 'Escape' && couchOn() && !document.querySelector('.modal.show, .offcanvas.show, .dropdown-menu.show')) {
			setCouch(false);
		}
	});
}

// A game controller moves through the page like on a console: the D-pad or the left stick go to
// the nearest control in that direction, A opens it, B closes a window or goes back, Start opens
// the quick search. The page is only read while a controller is connected and the page is seen.
const GAMEPAD_HINT_KEY = 'slm-gamepad-hint';
const PAD = { a: 0, b: 1, select: 8, start: 9, up: 12, down: 13, left: 14, right: 15 };

function gamepadTargets() {
	const scope = document.querySelector('.modal.show, .offcanvas.show') || document;
	return [...scope.querySelectorAll('a[href], button, input, select, textarea, [tabindex]')].filter(element => {
		if (element.disabled || element.tabIndex < 0 || element.closest('[hidden], [aria-hidden="true"], .d-none')) {
			return false;
		}
		const rect = element.getBoundingClientRect();
		return rect.width > 0 && rect.height > 0 && getComputedStyle(element).visibility !== 'hidden';
	});
}

// the box a control stands for: a game card for the link that covers it
function gamepadBox(element) {
	return (element.closest('.game-card, .saga-card') || element).getBoundingClientRect();
}

function gamepadMove(direction) {
	const targets = gamepadTargets();
	const current = document.activeElement;
	if (!targets.includes(current)) {
		// the first control in sight, or the first one of the page
		const inSight = targets.find(element => {
			const rect = gamepadBox(element);
			return rect.top >= 0 && rect.bottom <= window.innerHeight;
		});
		gamepadFocus(inSight || targets[0]);
		return;
	}
	const from = gamepadBox(current);
	const fromX = from.left + from.width / 2;
	const fromY = from.top + from.height / 2;
	const card = current.closest('.game-card, .saga-card');
	// the bars fixed at the bottom of the screen are only chosen when nothing else lies that way
	const bars = '.bottom-nav, .save-bar';
	const inBar = element => Boolean(element.closest(bars));
	const forward = direction === 'down' || direction === 'right';
	let best = null;
	let bestScore = Infinity;
	let bar = null;
	let barScore = Infinity;
	targets.forEach(element => {
		if (element === current || (card && card.contains(element))) {
			return;
		}
		// what comes after in the page for down and right, before for up and left: the sticky
		// bars move on the screen, the order of the page does not
		const following = Boolean(current.compareDocumentPosition(element) & Node.DOCUMENT_POSITION_FOLLOWING);
		if (following !== forward) {
			return;
		}
		const rect = gamepadBox(element);
		const x = rect.left + rect.width / 2;
		const y = rect.top + rect.height / 2;
		const dx = x - fromX;
		const dy = y - fromY;
		// only what lies in the direction asked, the nearest first and the most in line
		const along = { up: -dy, down: dy, left: -dx, right: dx }[direction];
		const across = direction === 'up' || direction === 'down' ? Math.abs(dx) : Math.abs(dy);
		if (along <= 1) {
			return;
		}
		const score = along + across * 2;
		if (inBar(element) && !inBar(current)) {
			if (score < barScore) {
				barScore = score;
				bar = element;
			}
		} else if (score < bestScore) {
			bestScore = score;
			best = element;
		}
	});
	best = best || bar;
	if (best) {
		// a card is entered by its link, not by the buttons drawn on it
		const target = best.closest('.game-card')?.querySelector('.stretched-link') || best;
		gamepadFocus(target);
		// a card ticks by itself when it takes the focus
		if (!target.closest('.game-card')) {
			playSound('move');
		}
	} else if (direction === 'up' || direction === 'down') {
		window.scrollBy({ top: (direction === 'up' ? -1 : 1) * window.innerHeight * .6, behavior: reducedMotion() ? 'auto' : 'smooth' });
	}
}

function gamepadFocus(element) {
	if (!element) {
		return;
	}
	element.focus({ preventScroll: true, focusVisible: true });
	(element.closest('.game-card, .saga-card') || element).scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: reducedMotion() ? 'auto' : 'smooth' });
}

function gamepadBack() {
	playSound('back');
	const open = document.querySelector('.modal.show, .offcanvas.show');
	if (open) {
		const close = open.querySelector('[data-bs-dismiss]');
		if (close) {
			close.click();
			return;
		}
	}
	if (history.length > 1) {
		history.back();
	}
}

function initGamepad() {
	if (!('getGamepads' in navigator)) {
		return;
	}
	let frame = 0;
	const held = {};
	const press = (name, pressed, action, now) => {
		const state = held[name] || (held[name] = { down: false, next: 0 });
		if (!pressed) {
			state.down = false;
			return;
		}
		// a held direction repeats, slowly first
		if (!state.down) {
			state.down = true;
			state.next = now + 380;
			action();
		} else if (now >= state.next && ['up', 'down', 'left', 'right'].includes(name)) {
			state.next = now + 130;
			action();
		}
	};
	const read = now => {
		frame = 0;
		const pad = [...navigator.getGamepads()].find(Boolean);
		if (!pad || document.hidden) {
			return;
		}
		const button = index => Boolean(pad.buttons[index] && pad.buttons[index].pressed);
		const [x = 0, y = 0] = pad.axes;
		const using = pad.buttons.some(b => b.pressed) || Math.abs(x) > .5 || Math.abs(y) > .5;
		if (using) {
			document.documentElement.classList.add('is-gamepad');
		}
		press('up', button(PAD.up) || y < -.5, () => gamepadMove('up'), now);
		press('down', button(PAD.down) || y > .5, () => gamepadMove('down'), now);
		press('left', button(PAD.left) || x < -.5, () => gamepadMove('left'), now);
		press('right', button(PAD.right) || x > .5, () => gamepadMove('right'), now);
		press('a', button(PAD.a), () => {
			if (document.activeElement && document.activeElement !== document.body) {
				playSound('open');
				document.activeElement.click();
			}
		}, now);
		press('b', button(PAD.b), gamepadBack, now);
		press('start', button(PAD.start), () => document.querySelector('.search-trigger')?.click(), now);
		press('select', button(PAD.select), () => {
			playSound(couchOn() ? 'back' : 'open');
			setCouch(!couchOn());
		}, now);
		frame = requestAnimationFrame(read);
	};
	const start = () => {
		if (!frame) {
			frame = requestAnimationFrame(read);
		}
	};
	window.addEventListener('gamepadconnected', () => {
		start();
		let shown = false;
		try {
			shown = localStorage.getItem(GAMEPAD_HINT_KEY) === '1';
			localStorage.setItem(GAMEPAD_HINT_KEY, '1');
		} catch (e) {
			// the hint shows again next time
		}
		if (!shown) {
			showToast('alert-info', 'bi-controller', t('Controller connected'), t('D-pad to move, A to open, B to go back, Start to search.'));
		}
	});
	document.addEventListener('visibilitychange', () => {
		if (!document.hidden && [...navigator.getGamepads()].some(Boolean)) {
			start();
		}
	});
	// the mouse or the keyboard take over again
	['pointerdown', 'keydown'].forEach(type => document.addEventListener(type, () => document.documentElement.classList.remove('is-gamepad'), { passive: true }));
	if ([...navigator.getGamepads()].some(Boolean)) {
		start();
	}
}

