// The page Saves and the emulators: For emulator tells where the files of a save go in Eden, its
// forks or Ryujinx, and downloads it; Upload from emulator takes the files of the folder of saves of
// an emulator (or the folder itself, or a ZIP) and keeps them as a new copy of the game, which
// NXDock restores on the console.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

// netEmulatorFiles walks what was dropped: files, and the files of the folders in them with their
// path; folder tells that a folder came, whose name begins the paths.
async function netEmulatorFiles(items) {
	const files = [];
	let folder = false;
	const readAll = reader => new Promise((resolve, reject) => {
		const all = [];
		const next = () => reader.readEntries(batch => {
			if (!batch.length) {
				resolve(all);
				return;
			}
			all.push(...batch);
			next();
		}, reject);
		next();
	});
	const walk = async (entry, prefix) => {
		if (entry.isFile) {
			const file = await new Promise((resolve, reject) => entry.file(resolve, reject));
			files.push({ file, path: prefix + file.name });
		} else if (entry.isDirectory) {
			for (const child of await readAll(entry.createReader())) {
				await walk(child, prefix + entry.name + '/');
			}
		}
	};
	for (const item of items) {
		const entry = item.webkitGetAsEntry && item.webkitGetAsEntry();
		if (!entry) {
			continue;
		}
		folder = folder || entry.isDirectory;
		await walk(entry, '');
	}
	return { files, folder };
}

function initNetSavesEmulator() {
	const emulatorElement = document.getElementById('netSaveEmulator');
	const uploadElement = document.getElementById('netSaveUpload');
	if (!emulatorElement || !uploadElement) {
		return;
	}

	// For emulator: the steps for the emulator chosen, with the name of the game, and the version
	// of the game the save is from
	let shownGame = '';
	const fillSteps = () => {
		const kind = emulatorElement.querySelector('input[name="netEmuKind"]:checked').value;
		emulatorElement.querySelectorAll('[data-net-emu-step]').forEach(step => {
			step.hidden = step.dataset.netEmuStep !== kind;
			step.textContent = step.dataset.text.replace('%v', shownGame);
		});
	};
	emulatorElement.querySelectorAll('input[name="netEmuKind"]').forEach(input => input.addEventListener('change', fillSteps));
	document.addEventListener('click', event => {
		const button = event.target.closest('[data-net-save-emulator]');
		if (!button) {
			return;
		}
		const row = button.closest('[data-net-save]');
		shownGame = row.dataset.netSaveGame;
		const title = emulatorElement.querySelector('[data-net-emu-title]');
		title.textContent = title.dataset.text.replace('%v', shownGame);
		const version = emulatorElement.querySelector('[data-net-emu-version]');
		version.hidden = !button.dataset.netSaveVersion || button.dataset.netSaveVersion === '0';
		version.textContent = version.dataset.text.replace('%v', 'v' + button.dataset.netSaveVersion);
		emulatorElement.querySelector('[data-net-emu-download]').href = row.dataset.netSave;
		fillSteps();
		Modal.getOrCreateInstance(emulatorElement).show();
	});

	// Upload from emulator
	const form = uploadElement.querySelector('[data-net-upload-form]');
	const game = form.elements.game;
	const chosenList = form.querySelector('[data-net-upload-chosen]');
	const error = form.querySelector('[data-net-upload-error]');
	const submit = form.querySelector('[data-net-upload-submit]');
	const filesInput = form.querySelector('[data-net-upload-files]');
	const folderInput = form.querySelector('[data-net-upload-folder]');
	const drop = form.querySelector('[data-net-upload-drop]');
	let chosen = { files: [], folder: false };
	const showChosen = () => {
		chosenList.replaceChildren();
		if (!chosen.files.length) {
			return;
		}
		const count = document.createElement('div');
		count.className = 'fw-semibold mb-1';
		count.textContent = chosen.files.length === 1 ? t('1 file chosen') : t('%v files chosen', chosen.files.length);
		chosenList.append(count);
		chosen.files.slice(0, 8).forEach(({ path }) => {
			const chip = document.createElement('span');
			chip.className = 'net-upload-chip';
			chip.textContent = path;
			chosenList.append(chip);
		});
	};
	const fail = message => {
		error.textContent = message;
		error.hidden = !message;
	};
	document.addEventListener('click', event => {
		const button = event.target.closest('[data-net-save-upload]');
		if (!button) {
			return;
		}
		const titleId = button.dataset.netSaveUpload;
		// a game of the saves the library does not have any more is offered too
		if (titleId && ![...game.options].some(option => option.value === titleId)) {
			game.add(new Option(button.dataset.netSaveUploadGame || titleId, titleId));
		}
		if (titleId) {
			game.value = titleId;
		}
		chosen = { files: [], folder: false };
		form.elements.note.value = '';
		fail('');
		showChosen();
		Modal.getOrCreateInstance(uploadElement).show();
	});
	form.querySelector('[data-net-upload-pick="files"]').addEventListener('click', () => filesInput.click());
	form.querySelector('[data-net-upload-pick="folder"]').addEventListener('click', () => folderInput.click());
	filesInput.addEventListener('change', () => {
		chosen = { files: [...filesInput.files].map(file => ({ file, path: file.name })), folder: false };
		filesInput.value = '';
		showChosen();
	});
	folderInput.addEventListener('change', () => {
		chosen = { files: [...folderInput.files].map(file => ({ file, path: file.webkitRelativePath || file.name })), folder: true };
		folderInput.value = '';
		showChosen();
	});
	drop.addEventListener('dragover', event => {
		event.preventDefault();
		drop.classList.add('is-over');
	});
	drop.addEventListener('dragleave', () => drop.classList.remove('is-over'));
	drop.addEventListener('drop', event => {
		event.preventDefault();
		drop.classList.remove('is-over');
		netEmulatorFiles([...event.dataTransfer.items]).then(found => {
			chosen = found;
			showChosen();
		});
	});
	form.addEventListener('submit', event => {
		event.preventDefault();
		if (!chosen.files.length) {
			fail(t('Choose the files of the save first.'));
			return;
		}
		if (!game.value) {
			return;
		}
		fail('');
		const body = new FormData();
		chosen.files.forEach(({ file, path }) => {
			body.append('path', path);
			body.append('file', file, file.name);
		});
		const query = new URLSearchParams({ emulator: form.elements.emulator.value, note: form.elements.note.value.trim(), folder: chosen.folder ? '1' : '0' });
		submit.disabled = true;
		apiFetch('/api/saves/' + encodeURIComponent(game.value) + '?' + query, { method: 'POST', headers: { Accept: 'application/json' }, body })
			.then(response => readJSON(response).then(json => {
				if (!response.ok) {
					throw new Error(netSaveError(response, json));
				}
				window.location.reload();
			}))
			.catch(e => {
				submit.disabled = false;
				fail(e.message);
			});
	});
}

document.addEventListener('DOMContentLoaded', initNetSavesEmulator);
