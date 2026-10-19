// Motion: counting numbers, covers loading, marks and reveal on scroll.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

const reducedMotion = () => window.matchMedia('(prefers-reduced-motion: reduce)').matches;

// The endless animations (the shimmer of a cover loading, the drift of the numbers, the pulse of
// the ribbons, the auras, the cursor of the cards...) stand still while their element is out of
// the window, and go on where they were when it comes back: what is seen does not change, and
// the browser does not work for what nobody sees, on a phone or a TV above all. Animations that
// start later (a cover that starts loading, a task that starts) are found again every two seconds.
function initPauseOffscreen() {
	if (!('IntersectionObserver' in window) || !document.getAnimations) {
		return;
	}
	const observer = new IntersectionObserver(entries => {
		entries.forEach(entry => {
			entry.target.toggleAttribute('data-offscreen', !entry.isIntersecting);
		});
	}, { rootMargin: '100px' });
	const observed = new WeakSet();
	const scan = () => {
		document.getAnimations().forEach(animation => {
			const target = animation.effect && animation.effect.target;
			if (!target || observed.has(target) || animation.effect.getTiming().iterations !== Infinity) {
				return;
			}
			// the header stays at the top: its animations are always in sight
			if (target.closest('.app-header, .bottom-nav, .modal')) {
				return;
			}
			observed.add(target);
			observer.observe(target);
		});
	};
	scan();
	setInterval(() => {
		if (!document.hidden) {
			scan();
		}
	}, 2000);
}

// numbers count up from zero when the page opens
function initCountUp() {
	if (reducedMotion()) {
		return;
	}
	document.querySelectorAll('[data-count]').forEach(element => {
		const target = Number(element.dataset.count);
		if (!Number.isFinite(target) || target <= 0) {
			return;
		}
		const duration = 700;
		const start = performance.now();
		const format = new Intl.NumberFormat(document.documentElement.lang || undefined);
		const step = now => {
			const progress = Math.min(1, (now - start) / duration);
			// ease out
			const value = Math.round(target * (1 - Math.pow(1 - progress, 3)));
			element.textContent = format.format(value);
			if (progress < 1) {
				requestAnimationFrame(step);
			}
		};
		element.textContent = '0';
		requestAnimationFrame(step);
		// hidden tabs pause animation frames: the right number is always shown in the end
		setTimeout(() => {
			element.textContent = format.format(target);
		}, duration + 100);
	});
}

// covers fade in once loaded, with a shimmer meanwhile
function initCoverLoading() {
	document.querySelectorAll('.game-cover img').forEach(image => {
		if (image.complete) {
			return;
		}
		image.classList.add('is-loading');
		const done = () => image.classList.remove('is-loading');
		image.addEventListener('load', done, { once: true });
		image.addEventListener('error', done, { once: true });
	});
}

// The star of the favorites and the heart of the wishlist change at once, with a little pop,
// and the server is told; if it fails they go back. The buttons with a label (the game page)
// reload the page, which shows the change in more places.
const MARKS = {
	fav: { url: '/favorites', field: 'favorite', state: 'favorite', on: 'bi-star-fill', off: 'bi-star', active: 'is-favorite', add: 'Add to favorites', remove: 'Remove from favorites' },
	wish: { url: '/wishlist', field: 'wanted', state: 'wished', on: 'bi-heart-fill', off: 'bi-heart', active: 'is-wished', add: 'Add to the wishlist', remove: 'Remove from the wishlist' },
};

function setMark(button, mark, marked) {
	button.dataset[mark.state] = String(marked);
	button.setAttribute('aria-pressed', String(marked));
	button.classList.toggle(mark.active, marked);
	// the card of a favorite, or of a game on the wishlist, has its aura
	const card = button.closest('.game-card');
	if (card) {
		card.classList.toggle(mark.state === 'favorite' ? 'is-fav-card' : 'is-wish-card', marked);
	}
	button.title = t(marked ? mark.remove : mark.add);
	const icon = button.querySelector('.bi');
	if (icon) {
		icon.classList.toggle(mark.on, marked);
		icon.classList.toggle(mark.off, !marked);
	}
}

function initMarks() {
	Object.entries(MARKS).forEach(([key, mark]) => {
		document.querySelectorAll(`[data-${key}]`).forEach(button => {
			button.addEventListener('click', e => {
				e.preventDefault();
				e.stopPropagation();
				const id = button.dataset[key];
				const marked = button.dataset[mark.state] !== 'true';
				const labelled = button.textContent.trim() !== '' && !button.querySelector('.visually-hidden');
				if (labelled) {
					button.disabled = true;
					postForm(mark.url, { id, [mark.field]: String(marked) })
						.then(() => window.location.reload())
						.catch(() => {
							button.disabled = false;
						});
					return;
				}
				// every button of the same game on the page
				const buttons = [...document.querySelectorAll(`[data-${key}="${CSS.escape(id)}"]`)];
				buttons.forEach(other => setMark(other, mark, marked));
				playSound(marked ? 'on' : 'off');
				if (!reducedMotion()) {
					button.classList.remove('is-popping');
					void button.offsetWidth;
					button.classList.add('is-popping');
				}
				postForm(mark.url, { id, [mark.field]: String(marked) })
					.catch(() => buttons.forEach(other => setMark(other, mark, !marked)));
			});
		});
	});
}

// what is below the screen at the load waits until it scrolls into view: its animations (bars,
// rings, cards that come up) start then, instead of running unseen
function initRevealOnScroll() {
	if (!('IntersectionObserver' in window) || reducedMotion()) {
		return;
	}
	const below = window.innerHeight;
	const items = [...document.querySelectorAll('main .surface, main .saga-card')]
		.filter(element => !element.closest('.is-waiting') && element.getBoundingClientRect().top > below);
	if (!items.length) {
		return;
	}
	const observer = new IntersectionObserver(entries => {
		entries.forEach(entry => {
			if (entry.isIntersecting) {
				entry.target.classList.remove('is-waiting');
				observer.unobserve(entry.target);
			}
		});
	}, { rootMargin: '0px 0px -8% 0px' });
	items.forEach(element => {
		element.classList.add('is-waiting');
		observer.observe(element);
	});
}

// Short sounds of a console menu, made by the browser (no sound file): a tick to move, two
// notes up to open, two down to go back, a chime when a task ends. Off unless turned on, on
// each device, from the menu of the header.
const SOUND_KEY = 'slm-sounds';
const SOUNDS = {
	move: [[1320, 0, .045, 'triangle', .03]],
	open: [[660, 0, .07, 'triangle', .045], [990, .06, .09, 'triangle', .045]],
	back: [[880, 0, .07, 'triangle', .04], [587, .06, .09, 'triangle', .04]],
	on: [[784, 0, .06, 'sine', .05], [1175, .05, .09, 'sine', .05]],
	off: [[1175, 0, .06, 'sine', .04], [784, .05, .09, 'sine', .04]],
	done: [[784, 0, .12, 'sine', .045], [988, .1, .12, 'sine', .045], [1319, .2, .22, 'sine', .045]],
};
let soundContext = null;

