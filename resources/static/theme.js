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
	// Settings opens on one section from the first paint (see initSettingsSections in web.js)
	if (location.pathname === '/settings.html') {
		var sections = ['library', 'ignored', 'general', 'background', 'compression', 'automation', 'notifications', 'backup'];
		var section = location.hash.slice(1);
		document.documentElement.setAttribute('data-section', sections.indexOf(section) >= 0 ? section : 'library');
	}
	// notices closed in the last week stay hidden from the first paint (see web.js)
	try {
		var closed = Number(localStorage.getItem('slm-no-login-notice') || 0);
		if (Date.now() - closed < 7 * 24 * 3600 * 1000) {
			document.documentElement.classList.add('closed-slm-no-login-notice');
		}
	} catch (e) {}
	// coming back to a page, its cards are already known: they appear without the cascade
	try {
		var navigation = performance.getEntriesByType('navigation')[0];
		if (navigation && navigation.type === 'back_forward') {
			document.documentElement.classList.add('is-returning');
		}
	} catch (e) {}
})();
