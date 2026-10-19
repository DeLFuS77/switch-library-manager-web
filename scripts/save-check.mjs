// Checks in a real browser that the forms of the preferences save: a value is changed, the
// save button pressed, and the page of the server must then have the new value, with the
// save button off again. A note in a form once stopped every save while all the other tests
// passed, as none of them pressed the button.
//
// Usage: node scripts/save-check.mjs [address of an app that is not in demo mode]
// Needs Chrome (or Chromium, or Edge); set CHROME to its path if it is not found.
import { spawn } from 'node:child_process';
import { existsSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const BASE = (process.argv[2] || process.env.SLM_URL || 'http://localhost:3001').replace(/\/$/, '');
const PORT = 9335;

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
	for (let i = 0; i < 150; i++) {
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

// the forms, what is changed in each, and how to tell the save button is off
const CHECKS = [
	{
		page: 'organize.html',
		form: '#organizeForm',
		field: '#keep_duplicate',
		change: `(field => { field.value = field.value === 'auto' ? 'nsp' : 'auto'; return field.value; })`,
		saved: `(form => form.querySelector('button[type="submit"]').disabled)`,
	},
	{
		page: 'settings.html',
		form: '#settingsForm',
		field: '#hide_demo_games',
		change: `(field => { field.checked = !field.checked; return String(field.checked); })`,
		saved: `(form => form.querySelector('.save-bar').classList.contains('is-unchanged'))`,
	},
];

async function main() {
	const profile = mkdtempSync(join(tmpdir(), 'slm-save-'));
	const chrome = spawn(findChrome(), [
		'--headless=new', `--remote-debugging-port=${PORT}`, `--user-data-dir=${profile}`,
		'--no-first-run', '--no-default-browser-check', '--disable-gpu', 'about:blank',
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
		const evaluate = async expression => {
			const result = await cdp.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
			if (result.exceptionDetails) {
				throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text);
			}
			return result.result.value;
		};
		for (const check of CHECKS) {
			await go(`${BASE}/${check.page}`);
			const result = await evaluate(`(async () => {
				const form = document.querySelector('${check.form}');
				const field = document.querySelector('${check.field}');
				if (!form || !field) {
					return { problem: 'the form or its field is missing' };
				}
				// what the field holds on the server, as the page shows it
				const read = async () => {
					const page = new DOMParser().parseFromString(await (await fetch(location.href, { cache: 'no-store' })).text(), 'text/html');
					const again = page.querySelector('${check.field}');
					return again.type === 'checkbox' ? String(again.checked) : again.value;
				};
				const wanted = (${check.change})(field);
				field.dispatchEvent(new Event('input', { bubbles: true }));
				field.dispatchEvent(new Event('change', { bubbles: true }));
				await new Promise(resolve => setTimeout(resolve, 300));
				const changedOn = !(${check.saved})(form);
				// the form tells when the server took it; without it, the save never happened
				const done = new Promise(resolve => {
					form.addEventListener('slm:saved', () => resolve(true), { once: true });
					setTimeout(() => resolve(false), 10000);
				});
				form.querySelector('button[type="submit"]').click();
				const answered = await done;
				// the button is looked at once the request is over
				await new Promise(resolve => setTimeout(resolve, 600));
				return { wanted, changedOn, answered, savedOff: (${check.saved})(form), onServer: await read() };
			})()`);
			const problems = [];
			if (result.problem) {
				problems.push(result.problem);
			} else {
				if (!result.changedOn) {
					problems.push('the save button did not come on after a change');
				}
				if (!result.answered) {
					problems.push('the form never told it was saved');
				}
				if (result.onServer !== result.wanted) {
					problems.push(`not saved: the server has ${result.onServer}, not ${result.wanted}`);
				}
				if (!result.savedOff) {
					problems.push('the save button stayed on after saving');
				}
			}
			if (problems.length) {
				failures += problems.length;
				console.log(`✗ ${check.page}`);
				problems.forEach(problem => console.log(`    ${problem}`));
			} else {
				console.log(`✓ ${check.page}`);
			}
		}
		cdp.close();
	} finally {
		chrome.kill();
		await sleep(300);
		try {
			rmSync(profile, { recursive: true, force: true });
		} catch {
			// Chrome may still hold the profile on Windows
		}
	}
	if (failures) {
		console.log(`${failures} problems`);
		process.exit(1);
	}
}

main().catch(error => {
	console.error(error);
	process.exit(1);
});
