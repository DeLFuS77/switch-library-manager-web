// Checks the pages of the demo for the accessibility rules the app keeps: text contrast of WCAG
// AA in both themes, no text under 12px, no sideways scroll on a phone of 320px, controls of 24px
// at least and no endless animation when the system asks for less motion.
//
// It drives a headless Chrome through its DevTools protocol, with nothing to install:
//   SLM_DEMO=true SLM_DATA_DIR=<empty folder> ./slm &
//   node scripts/a11y-check.mjs [http://localhost:3000]
// CHROME can point to the browser when it is not found.

import { spawn } from 'node:child_process';
import { existsSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const BASE = (process.argv[2] || process.env.SLM_URL || 'http://localhost:3000').replace(/\/$/, '');
const PORT = 9333;

const PAGES = [
	'index.html', 'updates.html', 'dlc.html', 'missing.html', 'upcoming.html', 'sagas.html',
	'statistics.html', 'year.html', 'compress.html', 'issues.html', 'space.html', 'sd.html',
	'tasks.html', 'settings.html', 'organize.html', 'maintenance.html', 'saves.html',
	'title/0100D00000150000.html',
];
const THEMES = ['light', 'dark'];
const VIEWPORTS = [{ width: 1440, height: 900, mobile: false }, { width: 320, height: 760, mobile: true }];

function findChrome() {
	const candidates = [
		process.env.CHROME,
		'/usr/bin/google-chrome', '/usr/bin/google-chrome-stable', '/usr/bin/chromium', '/usr/bin/chromium-browser',
		'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
		'C:/Program Files/Google/Chrome/Application/chrome.exe',
		'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',
	];
	const found = candidates.find(path => path && existsSync(path));
	if (!found) {
		throw new Error('Chrome not found: set CHROME to its path');
	}
	return found;
}

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));

async function connect() {
	for (let i = 0; i < 50; i++) {
		try {
			const targets = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json();
			const page = targets.find(target => target.type === 'page');
			if (page) {
				return page.webSocketDebuggerUrl;
			}
		} catch {
			// not listening yet
		}
		await sleep(200);
	}
	throw new Error('Chrome did not start');
}

function client(url) {
	const socket = new WebSocket(url);
	let id = 0;
	const pending = new Map();
	const waiting = new Map();
	socket.addEventListener('message', event => {
		const message = JSON.parse(event.data);
		if (message.id && pending.has(message.id)) {
			const { resolve, reject } = pending.get(message.id);
			pending.delete(message.id);
			message.error ? reject(new Error(message.error.message)) : resolve(message.result);
		} else if (message.method && waiting.has(message.method)) {
			waiting.get(message.method)();
			waiting.delete(message.method);
		}
	});
	const send = (method, params = {}) => new Promise((resolve, reject) => {
		pending.set(++id, { resolve, reject });
		socket.send(JSON.stringify({ id, method, params }));
	});
	const once = method => new Promise(resolve => waiting.set(method, resolve));
	const open = new Promise((resolve, reject) => {
		socket.addEventListener('open', resolve);
		socket.addEventListener('error', reject);
	});
	return { send, once, open, close: () => socket.close() };
}

// runs in the page: returns the problems found
function measure(checkWidth) {
	const parse = c => {
		const m = c.match(/rgba?\(([^)]+)\)/);
		if (!m) {
			return null;
		}
		const p = m[1].split(/[ ,/]+/).filter(Boolean).map(Number);
		return { r: p[0], g: p[1], b: p[2], a: p.length > 3 ? p[3] : 1 };
	};
	const lum = c => {
		const f = v => {
			v /= 255;
			return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
		};
		return 0.2126 * f(c.r) + 0.7152 * f(c.g) + 0.0722 * f(c.b);
	};
	const blend = (t, b) => ({ r: t.r * t.a + b.r * (1 - t.a), g: t.g * t.a + b.g * (1 - t.a), b: t.b * t.a + b.b * (1 - t.a), a: 1 });
	const backgroundOf = element => {
		const layers = [];
		for (let e = element; e; e = e.parentElement) {
			const style = getComputedStyle(e);
			// text on a picture or on a colored gradient cannot be measured here
			if (style.backgroundImage !== 'none' && (!style.backgroundImage.includes('gradient') || e.matches('[data-a11y-gradient]'))) {
				return null;
			}
			const color = parse(style.backgroundColor);
			if (color && color.a > 0) {
				layers.push(color);
				if (color.a >= 1) {
					break;
				}
			}
		}
		let base = parse(getComputedStyle(document.body).backgroundColor) || { r: 255, g: 255, b: 255, a: 1 };
		if (base.a < 1) {
			base = { r: 255, g: 255, b: 255, a: 1 };
		}
		for (let i = layers.length - 1; i >= 0; i--) {
			base = blend(layers[i], base);
		}
		return base;
	};
	const label = element => element.tagName.toLowerCase() + [...element.classList].slice(0, 3).map(c => '.' + c).join('');
	const problems = new Set();
	const skip = '[hidden], .visually-hidden, [aria-hidden="true"], .is-missing, :disabled, .disabled, .modal, .offcanvas, .toast-note, .year-hero';
	document.querySelectorAll('body *').forEach(element => {
		if (!element.getClientRects().length || element.closest(skip)) {
			return;
		}
		const text = [...element.childNodes].filter(n => n.nodeType === 3).map(n => n.textContent).join('').trim();
		if (!text) {
			return;
		}
		const style = getComputedStyle(element);
		if (style.visibility === 'hidden') {
			return;
		}
		const size = parseFloat(style.fontSize);
		if (size < 11.9) {
			problems.add(`text under 12px (${size}px): ${label(element)} "${text.slice(0, 24)}"`);
		}
		let opacity = 1;
		for (let e = element; e; e = e.parentElement) {
			opacity *= parseFloat(getComputedStyle(e).opacity);
		}
		const background = backgroundOf(element);
		let color = parse(style.color);
		if (!background || !color || opacity === 0) {
			return;
		}
		color = blend({ ...color, a: color.a * opacity }, background);
		const l1 = lum(color);
		const l2 = lum(background);
		const ratio = (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05);
		const large = size >= 24 || (parseInt(style.fontWeight) >= 700 && size >= 18.66);
		if (ratio < (large ? 3 : 4.5)) {
			problems.add(`contrast ${ratio.toFixed(2)}:1: ${label(element)} "${text.slice(0, 24)}"`);
		}
	});
	document.querySelectorAll('button, [role="button"], input[type="checkbox"]:not(.btn-check), input[type="radio"]:not(.btn-check), .page-link').forEach(element => {
		if (!element.getClientRects().length || element.closest(skip)) {
			return;
		}
		const rect = element.getBoundingClientRect();
		// a box with its label beside it is reached through the label too
		const labelled = element.matches('input') && (element.closest('label') || (element.id && document.querySelector(`label[for="${CSS.escape(element.id)}"]`)));
		if (!labelled && (rect.width < 23.5 || rect.height < 23.5)) {
			problems.add(`target ${Math.round(rect.width)}x${Math.round(rect.height)}: ${label(element)}`);
		}
	});
	if (checkWidth && document.documentElement.scrollWidth > document.documentElement.clientWidth) {
		problems.add(`scrolls sideways: ${document.documentElement.scrollWidth}px wide`);
	}
	const endless = document.getAnimations().filter(a => a.effect && a.effect.getTiming().iterations === Infinity && a.playState === 'running');
	return { problems: [...problems], endless: endless.map(a => label(a.effect.target || document.body) + ' ' + (a.animationName || '')) };
}

async function main() {
	const profile = mkdtempSync(join(tmpdir(), 'slm-a11y-'));
	const chrome = spawn(findChrome(), [
		'--headless=new', `--remote-debugging-port=${PORT}`, `--user-data-dir=${profile}`,
		'--no-first-run', '--no-default-browser-check', '--disable-gpu', '--hide-scrollbars', 'about:blank',
	], { stdio: 'ignore' });
	let failures = 0;
	try {
		const cdp = client(await connect());
		await cdp.open;
		await cdp.send('Page.enable');
		await cdp.send('Runtime.enable');
		const go = async url => {
			const loaded = cdp.once('Page.loadEventFired');
			await cdp.send('Page.navigate', { url });
			await Promise.race([loaded, sleep(15000)]);
		};
		const evaluate = async expression => (await cdp.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true })).result.value;

		await go(BASE + '/index.html');
		for (const theme of THEMES) {
			for (const viewport of VIEWPORTS) {
				await cdp.send('Emulation.setDeviceMetricsOverride', { width: viewport.width, height: viewport.height, deviceScaleFactor: 1, mobile: viewport.mobile });
				for (const reduce of [false, true]) {
					// the full check with motion, the endless animations with less motion
					if (reduce && viewport.mobile) {
						continue;
					}
					await cdp.send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: reduce ? 'reduce' : 'no-preference' }] });
					for (const page of PAGES) {
						await evaluate(`localStorage.setItem('slm-theme', '${theme}'); localStorage.setItem('slm-no-login-notice', String(Date.now())); localStorage.setItem('slm-whats-new-off', '1'); localStorage.setItem('slm-gamepad-hint', '1'); true`);
						await go(BASE + '/' + page);
						const result = await evaluate(`(async () => {
							await new Promise(r => setTimeout(r, 400));
							document.querySelectorAll('.is-waiting').forEach(e => e.classList.remove('is-waiting'));
							document.getAnimations().forEach(a => { if (a.effect && a.effect.getTiming().iterations !== Infinity) { try { a.finish(); } catch (e) {} } });
							await new Promise(r => setTimeout(r, 200));
							return (${measure.toString()})(${viewport.mobile});
						})()`);
						const where = `${page} · ${theme} · ${viewport.width}px${reduce ? ' · less motion' : ''}`;
						const problems = reduce ? result.endless.map(a => `endless animation with less motion: ${a}`) : result.problems;
						if (problems.length) {
							failures += problems.length;
							console.log(`✗ ${where}`);
							problems.forEach(problem => console.log(`    ${problem}`));
						} else {
							console.log(`✓ ${where}`);
						}
					}
				}
			}
		}
		cdp.close();
	} finally {
		chrome.kill();
		await sleep(300);
		try {
			rmSync(profile, { recursive: true, force: true });
		} catch {
			// Chrome may still hold a file
		}
	}
	if (failures) {
		console.log(`\n${failures} problem(s) found`);
		process.exit(1);
	}
	console.log('\nNo problem found');
}

main().catch(error => {
	console.error(error);
	process.exit(1);
});
