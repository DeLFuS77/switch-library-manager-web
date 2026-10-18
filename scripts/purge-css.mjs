// Leaves out of resources/static/web.css the rules of classes the app never uses: most of the
// icons of bootstrap-icons, flags, utilities and components of Bootstrap. A class is in use when
// its name appears in a template, in the bundled script (Bootstrap's own included, with the classes
// it adds at run time) or in the Go code; a class built at run time from a prefix ("chip-{{.Tone}}",
// 'bi-' + name, "tone-%s") keeps every class with that prefix.
//
// A rule goes only when each of its selectors needs a class that is never used; a class inside
// :not(), :is() or :where() does not count, as the selector may still match without it.
//
//   node scripts/purge-css.mjs            (run by npm run build, after the CSS and the script)

import { readFileSync, readdirSync, statSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

const root = join(import.meta.dirname, '..');
const cssPath = join(root, 'resources/static/web.css');

function filesIn(dir, extensions, found = []) {
	for (const name of readdirSync(dir)) {
		const path = join(dir, name);
		if (statSync(path).isDirectory()) {
			filesIn(path, extensions, found);
		} else if (extensions.some(extension => name.endsWith(extension)) && !name.endsWith('_test.go')) {
			found.push(path);
		}
	}
	return found;
}

const sources = [
	...filesIn(join(root, 'resources'), ['.html', '.json']),
	join(root, 'resources/static/web.js'),
	join(root, 'resources/static/theme.js'),
	...filesIn(join(root, 'web'), ['.go']),
	...filesIn(join(root, 'process'), ['.go']),
].map(path => readFileSync(path, 'utf8')).join('\n');

// prefixes of the classes built at run time
const prefixes = new Set();
for (const match of sources.matchAll(/([a-z][a-z0-9-]*-)(?:\{\{|\$\{|['"`]\s*\+|%[sdv])/g)) {
	prefixes.add(match[1]);
}

const usedCache = new Map();
function used(className) {
	if (!usedCache.has(className)) {
		let found = sources.includes(className);
		for (const prefix of prefixes) {
			found ||= className.startsWith(prefix);
		}
		usedCache.set(className, found);
	}
	return usedCache.get(className);
}

// a selector is dead when it needs a class never used; what is inside :not(), :is(), :where()
// and the like is left aside
function deadSelector(selector) {
	let plain = selector;
	let previous;
	do {
		previous = plain;
		plain = plain.replace(/:(not|is|where|has|-webkit-any|-moz-any)\((?:[^()]|\([^()]*\))*\)/g, '');
	} while (plain !== previous);
	const classes = [...plain.matchAll(/\.((?:\\.|[a-zA-Z0-9_-])+)/g)].map(match => match[1].replace(/\\(.)/g, '$1'));
	return classes.some(className => !used(className));
}

// the selectors of a rule, split on the commas that are not inside parentheses
function splitSelectors(text) {
	const parts = [];
	let depth = 0;
	let start = 0;
	for (let i = 0; i < text.length; i++) {
		if (text[i] === '(') depth++;
		else if (text[i] === ')') depth--;
		else if (text[i] === ',' && depth === 0) {
			parts.push(text.slice(start, i));
			start = i + 1;
		}
	}
	parts.push(text.slice(start));
	return parts;
}

// walks a block of the compressed CSS: rules are kept or left out, @media and @supports are
// walked into, other at-rules (@font-face, @keyframes...) are kept as they are
function purge(css) {
	let out = '';
	let i = 0;
	while (i < css.length) {
		const open = css.indexOf('{', i);
		if (open < 0) {
			out += css.slice(i);
			break;
		}
		const head = css.slice(i, open);
		// the end of the block, braces and strings aside
		let depth = 1;
		let j = open + 1;
		let quote = null;
		for (; j < css.length && depth > 0; j++) {
			const c = css[j];
			if (quote) {
				if (c === '\\') j++;
				else if (c === quote) quote = null;
			} else if (c === '"' || c === "'") quote = c;
			else if (c === '{') depth++;
			else if (c === '}') depth--;
		}
		const body = css.slice(open + 1, j - 1);
		const at = head.trim();
		if (at.startsWith('@media') || at.startsWith('@supports') || at.startsWith('@layer') || at.startsWith('@container')) {
			const inner = purge(body);
			if (inner.trim()) {
				out += `${head}{${inner}}`;
			}
		} else if (at.startsWith('@')) {
			out += `${head}{${body}}`;
		} else {
			const kept = splitSelectors(head).filter(selector => !deadSelector(selector));
			if (kept.length) {
				out += `${kept.join(',')}{${body}}`;
			}
		}
		i = j;
	}
	return out;
}

const before = readFileSync(cssPath, 'utf8');
// the banner comment of Bootstrap stays first
const banner = before.match(/^﻿?(@charset[^;]*;)?\s*(\/\*![\s\S]*?\*\/)?/)[0];
const after = banner + purge(before.slice(banner.length));
writeFileSync(cssPath, after);
console.log(`web.css: ${Math.round(before.length / 1024)} KB -> ${Math.round(after.length / 1024)} KB`);
