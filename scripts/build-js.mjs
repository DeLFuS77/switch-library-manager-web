// Builds resources/static/web.js from the files of resources/js, joined in the order of their
// names (01-core.js first, 18-start.js last): they share one scope, as the single web.js they
// come from, so no import or export is needed between them. Bundled with Bootstrap by esbuild.
//
//   node scripts/build-js.mjs            build once (npm run build:js)
//   node scripts/build-js.mjs --watch    build again on every change of resources/js

import { readdirSync, readFileSync, watch } from 'node:fs';
import { join } from 'node:path';
import * as esbuild from 'esbuild';

const root = join(import.meta.dirname, '..');
const folder = join(root, 'resources/js');

function source() {
	return readdirSync(folder)
		.filter(name => name.endsWith('.js'))
		.sort()
		.map(name => readFileSync(join(folder, name), 'utf8'))
		.join('');
}

async function build() {
	await esbuild.build({
		stdin: { contents: source(), resolveDir: join(root, 'resources'), sourcefile: 'web.js', loader: 'js' },
		bundle: true,
		minify: true,
		format: 'iife',
		target: 'es2020',
		outfile: join(root, 'resources/static/web.js'),
		logLevel: 'info',
	});
}

await build();
if (process.argv.includes('--watch')) {
	let timer = null;
	watch(folder, () => {
		clearTimeout(timer);
		timer = setTimeout(() => build().catch(error => console.error(error.message)), 100);
	});
	console.log('watching resources/js');
}
