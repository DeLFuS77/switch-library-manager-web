// The bin of the quarantine in the header: when more files wait in it than on the last page, it
// gives a small jump (the motion is in _motion.scss, off with reduced motion).
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.
(() => {
	const link = document.querySelector('[data-quarantine-files]');
	if (!link) {
		return;
	}
	const files = Number(link.dataset.quarantineFiles) || 0;
	let before = null;
	try {
		before = sessionStorage.getItem('slm-quarantine-files');
		sessionStorage.setItem('slm-quarantine-files', String(files));
	} catch (e) {
		// no storage: no jump
	}
	if (before !== null && files > Number(before)) {
		link.classList.add('is-dropped');
		link.addEventListener('animationend', () => link.classList.remove('is-dropped'), { once: true });
	}
})();
