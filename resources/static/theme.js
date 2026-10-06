// Applies the saved theme before the page is painted (see initThemeSwitcher in web.js).
// A separate file, so the Content Security Policy can forbid inline scripts.
(function () {
	try {
		var theme = localStorage.getItem('slm-theme') || 'dark';
		if (theme === 'auto') {
			theme = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
		}
		document.documentElement.setAttribute('data-bs-theme', theme);
	} catch (e) {}
	// coming back to a page, its cards are already known: they appear without the cascade
	try {
		var navigation = performance.getEntriesByType('navigation')[0];
		if (navigation && navigation.type === 'back_forward') {
			document.documentElement.classList.add('is-returning');
		}
	} catch (e) {}
})();
