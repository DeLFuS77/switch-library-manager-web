// Library: upload games to the library folder, chosen with the button or dropped on the page.
// The files go in pieces, one file after the other; a piece that fails is sent again from
// where the server is, and a file chosen again after the page was closed goes on from there.

const UPLOAD_PIECE = 16 * 1024 * 1024;
const UPLOAD_EXTENSIONS = ['.nsp', '.nsz', '.xci', '.xcz'];
const UPLOAD_RETRIES = 6;

// whether games are being sent, or wait to be: the page is not reloaded meanwhile
let gameUploadsWaiting = 0;
function gameUploadsBusy() {
	return gameUploadsWaiting > 0;
}

function initGameUpload() {
	const panel = document.getElementById('uploadPanel');
	const input = document.getElementById('uploadInput');
	if (!panel || !input) {
		return;
	}
	const list = panel.querySelector('.upload-list');
	const text = name => panel.dataset[name] || '';
	const queue = [];
	let running = false;
	let current = null;

	const fill = (template, ...args) => args.reduce((result, arg) => result.replace('%v', arg), template);
	const answer = response => response.json().catch(() => ({})).then(json => ({ ok: response.ok, status: response.status, json }));
	const failure = json => (json.globalError && json.globalError.message) || text('failed');

	const addItem = file => {
		const item = document.createElement('li');
		item.className = 'upload-item';
		item.innerHTML = '<div class="upload-item-line"><span class="upload-name"></span><span class="upload-state"></span>' +
			'<button type="button" class="btn btn-link btn-sm p-0 text-body-secondary upload-cancel"><span class="bi bi-x-lg" aria-hidden="true"></span></button></div>' +
			'<div class="progress" role="progressbar" aria-valuemin="0" aria-valuemax="100"><div class="progress-bar" style="width: 0%"></div></div>';
		item.querySelector('.upload-name').textContent = file.name;
		item.querySelector('.upload-name').title = file.name;
		const cancel = item.querySelector('.upload-cancel');
		cancel.title = text('cancel');
		cancel.setAttribute('aria-label', text('cancel') + ': ' + file.name);
		list.appendChild(item);
		panel.hidden = false;
		// the folder chosen when the file was added
		const folderField = document.getElementById('uploadFolder');
		const job = { file, item, cancelled: false, id: null, folder: folderField ? folderField.value : '' };
		cancel.addEventListener('click', () => {
			job.cancelled = true;
			if (job.id) {
				postForm('/uploads/cancel', { id: job.id }).catch(() => undefined);
			}
			if (job !== current) {
				item.remove();
				queue.splice(queue.indexOf(job), 1);
				gameUploadsWaiting = queue.length + (current ? 1 : 0);
				hideIfEmpty();
			}
		});
		setState(job, text('waiting'));
		return job;
	};
	const setState = (job, state, received) => {
		job.item.querySelector('.upload-state').textContent = state;
		if (received !== undefined) {
			const percent = job.file.size ? Math.floor(received * 100 / job.file.size) : 100;
			const bar = job.item.querySelector('.progress');
			bar.setAttribute('aria-valuenow', String(percent));
			bar.querySelector('.progress-bar').style.width = percent + '%';
		}
	};
	const hideIfEmpty = () => {
		if (!list.children.length) {
			panel.hidden = true;
		}
	};
	const finishItem = (job, done, message) => {
		job.item.classList.add(done ? 'is-done' : 'is-failed');
		job.item.querySelector('.upload-cancel').hidden = true;
		setState(job, message, done ? job.file.size : undefined);
		if (done) {
			setTimeout(() => {
				job.item.remove();
				hideIfEmpty();
			}, 8000);
		} else {
			// a failed file stays until it is closed
			const close = job.item.querySelector('.upload-cancel');
			close.hidden = false;
			close.onclick = () => {
				job.item.remove();
				hideIfEmpty();
			};
		}
	};

	const sendPiece = (job, offset) => {
		const end = Math.min(offset + UPLOAD_PIECE, job.file.size);
		return fetch('/uploads/chunk?id=' + encodeURIComponent(job.id) + '&offset=' + offset, {
			method: 'PUT',
			body: job.file.slice(offset, end),
			headers: { 'Content-type': 'application/octet-stream' }
		}).then(answer);
	};

	const upload = async job => {
		const started = await fetch('/uploads/start', {
			method: 'POST',
			body: new URLSearchParams({ name: job.file.name, size: String(job.file.size), folder: job.folder }).toString(),
			headers: { 'Content-type': 'application/x-www-form-urlencoded' }
		}).then(answer);
		if (!started.ok) {
			throw new Error(failure(started.json));
		}
		job.id = started.json.id;
		let received = started.json.received || 0;
		let tries = 0;
		let since = Date.now();
		let sentSince = received;
		while (received < job.file.size) {
			if (job.cancelled) {
				return false;
			}
			let result;
			try {
				result = await sendPiece(job, received);
			} catch (error) {
				result = null;
			}
			if (result && result.ok) {
				received = result.json.received;
				tries = 0;
			} else if (result && result.status === 409 && typeof result.json.received === 'number') {
				// the server was elsewhere: from where it is
				received = result.json.received;
				tries++;
				if (tries > UPLOAD_RETRIES) {
					throw new Error(text('failed'));
				}
			} else if (result && result.status === 404) {
				throw new Error(failure(result.json));
			} else {
				tries++;
				if (tries > UPLOAD_RETRIES) {
					throw new Error(text('failed'));
				}
				setState(job, text('retrying'));
				await new Promise(resolve => setTimeout(resolve, Math.min(30000, 1000 * 2 ** tries)));
				// where the server is after a piece cut short
				const status = await fetch('/uploads/status?id=' + encodeURIComponent(job.id), { cache: 'no-store' }).then(answer).catch(() => null);
				if (status && status.ok) {
					received = status.json.received;
				} else if (status && status.status === 404) {
					throw new Error(failure(status.json));
				}
				continue;
			}
			const seconds = (Date.now() - since) / 1000;
			const speed = seconds > 0 ? (received - sentSince) / seconds : 0;
			if (seconds > 5) {
				since = Date.now();
				sentSince = received;
			}
			setState(job, fill(text('progress'), formatSize(received), formatSize(job.file.size)) + (speed > 0 ? ' · ' + formatSize(speed) + '/s' : ''), received);
		}
		if (job.cancelled) {
			return false;
		}
		const finished = await fetch('/uploads/finish', {
			method: 'POST',
			body: new URLSearchParams({ id: job.id, name: job.file.name }).toString(),
			headers: { 'Content-type': 'application/x-www-form-urlencoded' }
		}).then(answer);
		if (!finished.ok) {
			throw new Error(failure(finished.json));
		}
		return true;
	};

	const next = () => {
		if (running) {
			return;
		}
		current = queue.shift();
		gameUploadsWaiting = queue.length + (current ? 1 : 0);
		if (!current) {
			return;
		}
		running = true;
		const job = current;
		setState(job, text('starting'), 0);
		upload(job)
			.then(done => {
				if (done) {
					finishItem(job, true, text('done'));
				} else {
					job.item.remove();
					hideIfEmpty();
				}
			})
			.catch(error => finishItem(job, false, error.message))
			.finally(() => {
				running = false;
				current = null;
				next();
			});
	};

	const add = files => {
		[...files].forEach(file => {
			const extension = file.name.slice(file.name.lastIndexOf('.')).toLowerCase();
			const job = addItem(file);
			if (!UPLOAD_EXTENSIONS.includes(extension)) {
				finishItem(job, false, text('badName'));
				return;
			}
			queue.push(job);
		});
		gameUploadsWaiting = queue.length + (current ? 1 : 0);
		next();
	};

	document.querySelectorAll('[data-upload-open]').forEach(button => button.addEventListener('click', () => input.click()));
	input.addEventListener('change', () => {
		add(input.files);
		input.value = '';
	});
	// closing the page stops the upload: it goes on when the same file is chosen again
	window.addEventListener('beforeunload', e => {
		if (running) {
			e.preventDefault();
			e.returnValue = '';
		}
	});

	// files dragged over the page
	const veil = document.getElementById('uploadDrop');
	let depth = 0;
	const hasFiles = e => e.dataTransfer && [...e.dataTransfer.types].includes('Files');
	document.addEventListener('dragenter', e => {
		if (!hasFiles(e)) {
			return;
		}
		depth++;
		veil.hidden = false;
	});
	document.addEventListener('dragleave', e => {
		if (!hasFiles(e)) {
			return;
		}
		depth = Math.max(0, depth - 1);
		if (depth === 0) {
			veil.hidden = true;
		}
	});
	document.addEventListener('dragover', e => {
		if (hasFiles(e)) {
			e.preventDefault();
			e.dataTransfer.dropEffect = 'copy';
		}
	});
	document.addEventListener('drop', e => {
		if (!hasFiles(e)) {
			return;
		}
		e.preventDefault();
		depth = 0;
		veil.hidden = true;
		add(e.dataTransfer.files);
	});
}
