// The help of the fields, the titles and the cards of the app, as NXPress has it (its issue #67): a
// small ⓘ after a name tells there is some, and it shows in a card. With a mouse, while the mouse is
// on the name or the field has the focus of the keyboard. On a touch screen a touch opens it and
// another one closes it: on the name of a title, or on a ⓘ of its own after the name of a field or
// a button, as a touch on those changes the field or presses the button. The help of a card or a
// choice of filter of the Library is the link's: on a touch screen it is only read out. The help
// stays in the page and describes its field to screen readers. Statuses, warnings and what must be
// read now stay in sight.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

const hoverHelp = { owners: new WeakMap(), open: null, control: null, timer: 0, hideTimer: 0, warm: 0 };

// the name a help goes with: the label of its switch (in it, or just before it), else the label
// or title before it; a help with none (one for several fields) stays in sight
function helpName(help) {
	const check = help.closest('.form-check');
	if (check) {
		return check.querySelector(':scope > .form-check-label');
	}
	for (let element = help.previousElementSibling; element; element = element.previousElementSibling) {
		if (element.matches('.form-label, legend, .help-name')) {
			return element;
		}
		// a head with the name in it, like a title next to its buttons
		const inner = element.querySelector(':scope > .help-name');
		if (inner) {
			return inner;
		}
		// a field just before it, in a box of its own: its label
		const next = element.nextElementSibling;
		const close = element === help.previousElementSibling || (next && next.matches('.help-keep') && next.nextElementSibling === help);
		if (element.matches('.form-check')) {
			return close ? element.querySelector(':scope > .form-check-label') : null;
		}
		if (close && element.querySelector(':scope > .form-label')) {
			return element.querySelector(':scope > .form-label');
		}
		// the help of another field, not a status of this one, ends the search
		if (element.matches('.form-text:not(.help-keep)')) {
			return null;
		}
	}
	return null;
}

function initHoverHelp() {
	const fine = window.matchMedia('(hover: hover) and (pointer: fine)').matches;
	const helps = document.querySelectorAll('[data-hover-help] .form-text:not(.help-keep)');
	if (!helps.length) {
		return;
	}
	const pop = document.createElement('div');
	pop.className = 'help-pop';
	pop.id = 'help-pop';
	pop.hidden = true;
	pop.setAttribute('aria-hidden', 'true');
	pop.innerHTML = '<div class="help-title"><span class="bi bi-info-lg" aria-hidden="true"></span><span></span></div><div class="help-body"></div><span class="help-arrow"></span>';
	document.body.append(pop);
	helps.forEach((help, i) => {
		const name = helpName(help);
		if (!name || name.dataset.help) {
			return;
		}
		// the field the name goes with is the one described, and its focus shows the help; a name
		// in a link (a card or a choice of filter) is the link's; a name with no field of its own
		// (the title of a group) takes the focus itself
		const control = (name.matches('label') && name.control) || name.closest('a, button') || name;
		if (!fine && control.closest('a')) {
			return;
		}
		help.id ||= 'hover-help-' + i;
		help.classList.add('help-text');
		name.dataset.help = help.id;
		const described = (control.getAttribute('aria-describedby') || '').split(' ').filter(Boolean);
		if (!described.includes(help.id)) {
			control.setAttribute('aria-describedby', [...described, help.id].join(' '));
		}
		hoverHelp.owners.set(control, name);
		if (!fine && (control !== name || name.matches('button'))) {
			// a touch on the name of a field changes it, and one on a button presses it: the ⓘ is a
			// button of its own, after the name
			const mark = document.createElement('button');
			mark.type = 'button';
			mark.className = 'help-mark';
			mark.dataset.help = help.id;
			mark.dataset.helpTitle = name.textContent.trim();
			mark.setAttribute('aria-label', t('Help about %v', name.textContent.trim()));
			mark.setAttribute('aria-expanded', 'false');
			name.after(mark);
			return;
		}
		name.classList.add('has-help');
		if (control === name) {
			name.tabIndex = 0;
		}
	});
	document.addEventListener('pointerover', event => {
		// a finger opens the card with a touch, below
		if (event.pointerType === 'touch') {
			return;
		}
		if (pop.contains(event.target)) {
			clearTimeout(hoverHelp.hideTimer);
			return;
		}
		const name = event.target.closest('.has-help');
		if (!name || event.buttons) {
			return;
		}
		clearTimeout(hoverHelp.hideTimer);
		if (name === hoverHelp.open) {
			return;
		}
		clearTimeout(hoverHelp.timer);
		// a moment on the name first, so the cards do not flash while the mouse goes by; one after
		// another comes at once
		hoverHelp.timer = setTimeout(() => showHoverHelp(name, null), hoverHelp.open || Date.now() < hoverHelp.warm ? 40 : 300);
	});
	document.addEventListener('pointerout', event => {
		if (event.pointerType === 'touch') {
			return;
		}
		const name = event.target.closest('.has-help');
		if (!name && !pop.contains(event.target)) {
			return;
		}
		const to = event.relatedTarget;
		if (to && ((name && name.contains(to)) || pop.contains(to) || (hoverHelp.open && hoverHelp.open.contains(to)))) {
			return;
		}
		// brought by the keyboard, it stays while its field has the focus
		if (hoverHelp.control && document.activeElement === hoverHelp.control) {
			return;
		}
		clearTimeout(hoverHelp.timer);
		hoverHelp.hideTimer = setTimeout(hideHoverHelp, 180);
	});
	// the focus of the keyboard on a field, or inside a group of fields with a title
	const ownerOf = element => {
		if (hoverHelp.owners.has(element)) {
			return hoverHelp.owners.get(element);
		}
		// a group of fields, not a whole section, whose title has a help
		const group = element.closest && element.closest('[aria-labelledby]:not(section)');
		const title = group && document.getElementById(group.getAttribute('aria-labelledby'));
		return title && title.classList.contains('has-help') ? title : null;
	};
	document.addEventListener('focusin', event => {
		const name = ownerOf(event.target);
		if (name && event.target.matches(':focus-visible')) {
			showHoverHelp(name, event.target);
		}
	});
	document.addEventListener('focusout', event => {
		if (hoverHelp.open && event.target === hoverHelp.control) {
			hideHoverHelp();
		}
	});
	document.addEventListener('keydown', event => {
		if (event.key === 'Escape') {
			hideHoverHelp();
		}
	});
	// a click elsewhere closes it; one in the field being filled in, or on the name of the card,
	// leaves it open
	document.addEventListener('pointerdown', event => {
		if (!pop.contains(event.target) && !(hoverHelp.control && hoverHelp.control.contains(event.target)) && !(hoverHelp.open && hoverHelp.open.contains(event.target))) {
			hideHoverHelp();
		}
	});
	// on a touch screen, a touch on the name or its ⓘ opens its card and another one closes it
	if (!fine) {
		document.addEventListener('click', event => {
			const name = event.target.closest('.has-help, .help-mark');
			if (!name) {
				return;
			}
			event.preventDefault();
			if (hoverHelp.open === name) {
				hideHoverHelp();
			} else {
				showHoverHelp(name, null);
			}
		});
	}
	document.addEventListener('scroll', hideHoverHelp, true);
	window.addEventListener('blur', hideHoverHelp);
}

// the card of the help of a name: under it, or above when there is no room under, never out of
// the window; when the keyboard brought it, it leaves the field being filled in sight
function showHoverHelp(name, control) {
	const help = document.getElementById(name.dataset.help);
	const pop = document.getElementById('help-pop');
	if (!help || !pop || !help.textContent.trim() || !name.getClientRects().length) {
		return;
	}
	clearTimeout(hoverHelp.timer);
	clearTimeout(hoverHelp.hideTimer);
	if (hoverHelp.open && hoverHelp.open !== name) {
		hoverHelp.open.classList.remove('help-open');
		if (hoverHelp.open.matches('.help-mark')) {
			hoverHelp.open.setAttribute('aria-expanded', 'false');
		}
	}
	hoverHelp.open = name;
	hoverHelp.control = control;
	name.classList.add('help-open');
	if (name.matches('.help-mark')) {
		name.setAttribute('aria-expanded', 'true');
	}
	pop.classList.remove('in', 'above');
	pop.querySelector('.help-title span:last-child').textContent = name.dataset.helpTitle || name.textContent.trim();
	// the help as the page has it, with its links and codes, its ids left out
	const body = pop.querySelector('.help-body');
	body.replaceChildren(...[...help.childNodes].map(node => node.cloneNode(true)));
	body.querySelectorAll('[id]').forEach(element => element.removeAttribute('id'));
	pop.classList.toggle('wide', help.textContent.trim().length > 240);
	pop.hidden = false;
	const range = document.createRange();
	range.selectNodeContents(name);
	const words = range.getClientRects();
	const box = name.getBoundingClientRect();
	const last = words.length ? words[words.length - 1] : box;
	const x = Math.min(last.right + 13, box.right);
	// the name, and the field the keyboard is in: the card goes around both
	let top = box.top, bottom = box.bottom;
	if (control && control !== name) {
		const field = control.getBoundingClientRect();
		top = Math.min(top, field.top);
		bottom = Math.max(bottom, field.bottom);
	}
	const width = pop.offsetWidth, height = pop.offsetHeight, gap = 10;
	const above = bottom + gap + height > window.innerHeight - 8 && top - gap - height > 8;
	const left = Math.max(8, Math.min(x - width / 2, window.innerWidth - width - 8));
	pop.style.left = left + 'px';
	pop.style.top = Math.max(8, above ? top - gap - height : bottom + gap) + 'px';
	pop.classList.toggle('above', above);
	pop.querySelector('.help-arrow').style.left = Math.max(10, Math.min(x - left - 6, width - 22)) + 'px';
	requestAnimationFrame(() => pop.classList.add('in'));
}

function hideHoverHelp() {
	clearTimeout(hoverHelp.timer);
	if (!hoverHelp.open) {
		return;
	}
	hoverHelp.open.classList.remove('help-open');
	if (hoverHelp.open.matches('.help-mark')) {
		hoverHelp.open.setAttribute('aria-expanded', 'false');
	}
	hoverHelp.open = null;
	hoverHelp.control = null;
	hoverHelp.warm = Date.now() + 600;
	const pop = document.getElementById('help-pop');
	pop.classList.remove('in');
	setTimeout(() => {
		if (!hoverHelp.open) {
			pop.hidden = true;
		}
	}, 160);
}
