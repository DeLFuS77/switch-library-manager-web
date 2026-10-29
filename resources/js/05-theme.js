// The light and dark themes.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

function storedTheme() {
	try {
		return localStorage.getItem(THEME_KEY) || 'dark';
	} catch (e) {
		return 'dark';
	}
}

function applyTheme(theme) {
	const dark = theme === 'dark' || (theme === 'auto' && window.matchMedia('(prefers-color-scheme: dark)').matches);
	document.documentElement.setAttribute('data-bs-theme', dark ? 'dark' : 'light');
	// the bar of the browser on the phone in the colour of the page (see theme.js)
	document.querySelector('meta[name="theme-color"]')?.setAttribute('content', dark ? '#0d1117' : '#f4f6fa');
	document.querySelectorAll('[data-theme-value]').forEach(item => {
		const active = item.dataset.themeValue === theme;
		item.classList.toggle('active', active);
		item.setAttribute('aria-pressed', active);
	});
}

function initThemeSwitcher() {
	applyTheme(storedTheme());
	document.querySelectorAll('[data-theme-value]').forEach(item => item.addEventListener('click', () => {
		try {
			localStorage.setItem(THEME_KEY, item.dataset.themeValue);
		} catch (e) {
			// the theme is still applied to this page
		}
		applyTheme(item.dataset.themeValue);
	}));
	// follow the system when "automatic" is selected
	window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
		if (storedTheme() === 'auto') {
			applyTheme('auto');
		}
	});
}

