// The files of a check on the Tasks page by how their check ended, as Verify does in NXPress
// (NXPress #134): a choice above them leaves in sight the files verified, corrupted, modified or
// skipped, with how many there are; All shows them all again. The choice of each task stays while
// the page is brought up to date, and goes back to All when its kind has no file any more.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

const taskCheckChosen = new Map();
const taskCheckKinds = ['ok', 'failed', 'modified', 'skipped'];

function taskTileKind(tile) {
	if (tile.classList.contains('is-working') || tile.classList.contains('is-waiting')) {
		return '';
	}
	if (tile.classList.contains('is-failed')) {
		return 'failed';
	}
	if (tile.classList.contains('is-modified')) {
		return 'modified';
	}
	if (tile.classList.contains('is-skipped')) {
		return 'skipped';
	}
	return 'ok';
}

function applyTaskCheckFilters(root) {
	root.querySelectorAll('ul[data-check-tiles]').forEach(list => {
		const key = list.closest('[data-task-id]')?.dataset.taskId || list.closest('[id^="task-"]')?.id || '';
		const old = list.previousElementSibling;
		if (old && old.classList.contains('task-check-filter')) {
			old.remove();
		}
		const tiles = [...list.querySelectorAll('.task-tile')];
		const counts = {};
		tiles.forEach(tile => {
			const kind = taskTileKind(tile);
			if (kind) {
				counts[kind] = (counts[kind] || 0) + 1;
			}
		});
		// a choice is worth it with files of two kinds or more
		let chosen = taskCheckChosen.get(key) || '';
		if (Object.keys(counts).length < 2 || (chosen && !counts[chosen])) {
			chosen = '';
			taskCheckChosen.delete(key);
		}
		tiles.forEach(tile => tile.classList.toggle('d-none', chosen !== '' && taskTileKind(tile) !== chosen));
		if (Object.keys(counts).length < 2) {
			return;
		}
		const bar = document.createElement('div');
		bar.className = 'task-check-filter mt-3';
		bar.setAttribute('role', 'radiogroup');
		bar.setAttribute('aria-label', list.dataset.lFilters);
		const add = (kind, count) => {
			const button = document.createElement('button');
			button.type = 'button';
			button.className = 'task-check-choice' + (kind === chosen ? ' active' : '');
			button.setAttribute('role', 'radio');
			button.setAttribute('aria-checked', String(kind === chosen));
			button.dataset.checkKind = kind;
			const dot = document.createElement('span');
			dot.className = 'task-check-dot is-' + (kind || 'all');
			dot.setAttribute('aria-hidden', 'true');
			const number = document.createElement('b');
			number.textContent = count;
			button.append(dot, list.dataset['l' + (kind ? kind[0].toUpperCase() + kind.slice(1) : 'All')] + ' ', number);
			bar.append(button);
		};
		add('', tiles.length);
		taskCheckKinds.forEach(kind => counts[kind] && add(kind, counts[kind]));
		bar.addEventListener('click', event => {
			const button = event.target.closest('[data-check-kind]');
			if (!button) {
				return;
			}
			taskCheckChosen.set(key, button.dataset.checkKind);
			applyTaskCheckFilters(list.parentElement);
		});
		list.before(bar);
	});
}

document.addEventListener('DOMContentLoaded', () => applyTaskCheckFilters(document));
