// The tools: packs, measure, Space, Issues, Tune-up and history charts.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

// Space: a copy of a game from another region is deleted, with its updates and DLC, after a
// confirmation; the page shows the library again once it is rescanned.
// The header of the game page clips what goes out of it (its blurred background), so its
// menus (collections, pack) are placed fixed on the screen, over the rest of the page.
function initHeroMenus() {
	document.querySelectorAll('.title-hero [data-bs-toggle="dropdown"]').forEach(toggle => {
		Dropdown.getOrCreateInstance(toggle, { popperConfig: config => ({ ...config, strategy: 'fixed' }) });
	});
}

// Game page > Complete pack: the pack is made as a task, followed on the Tasks page.
function initPack() {
	document.querySelectorAll('[data-pack]').forEach(form => {
		const submit = form.querySelector('[type="submit"]');
		// keeping or deleting the separate files is always chosen, nothing is chosen first
		form.addEventListener('change', () => {
			submit.disabled = !form.elements.delete_originals.value;
		});
		form.addEventListener('submit', e => {
			e.preventDefault();
			const choice = form.elements.delete_originals.value;
			if (!choice) {
				return;
			}
			submit.disabled = true;
			runAsTask(() => postForm('/title/pack', { id: form.dataset.pack, delete_originals: choice }))
				.catch(error => {
					submit.disabled = false;
					insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), error.message);
				});
		});
	});
}

// the measure of the server fills the compression settings with what compresses fastest here
function initMeasure() {
	const box = document.querySelector('[data-measure]');
	if (!box) {
		return;
	}
	const button = box.querySelector('[data-measure-start]');
	const result = box.querySelector('[data-measure-result]');
	const format = (text, ...args) => args.reduce((value, arg) => value.replace('%v', arg), text);
	const line = (icon, text) => {
		const row = document.createElement('div');
		const glyph = document.createElement('span');
		glyph.className = `bi ${icon} me-1`;
		glyph.setAttribute('aria-hidden', 'true');
		row.append(glyph, text);
		return row;
	};
	button.addEventListener('click', () => {
		button.disabled = true;
		result.hidden = false;
		result.classList.remove('text-danger-emphasis');
		result.replaceChildren(line('bi-hourglass-split', result.dataset.textRunning));
		fetch('/settings/measure', { method: 'POST' })
			.then(response => response.json().catch(() => ({})).then(json => {
				if (!response.ok) {
					throw new Error(json.error || (json.globalError && json.globalError.message) || t('Unexpected server response (%v).', response.status));
				}
				return json;
			}))
			.then(measure => {
				const speeds = measure.results
					.map(trial => `${trial.files}×${trial.threads} → ${trial.speed} MB/s`).join(' · ');
				result.replaceChildren(
					line('bi-speedometer2', `${result.dataset.textTrials}: ${speeds}`),
					line('bi-check2-circle', format(result.dataset.textChosen, measure.files_at_once, measure.threads)));
				[['compress_threads', measure.threads], ['compress_files_at_once', measure.files_at_once]].forEach(([id, value]) => {
					const select = document.getElementById(id);
					if (select && [...select.options].some(option => option.value === String(value))) {
						select.value = String(value);
						select.dispatchEvent(new Event('input', { bubbles: true }));
						select.dispatchEvent(new Event('change', { bubbles: true }));
					}
				});
			})
			.catch(error => {
				result.classList.add('text-danger-emphasis');
				result.replaceChildren(line('bi-exclamation-triangle', error.message));
			})
			.finally(() => {
				button.disabled = false;
			});
	});
}

// while the Space page is prepared on the server, it asks until it is ready and opens again
function initSpacePreparing() {
	if (!document.querySelector('[data-space-preparing]')) {
		return;
	}
	const ask = () => fetch('/space/ready', { cache: 'no-store', headers: { Accept: 'application/json' } })
		.then(response => (response.ok ? response.json() : { ready: false }))
		.then(state => {
			if (state.ready) {
				window.location.reload();
			} else {
				setTimeout(ask, 1000);
			}
		})
		.catch(() => setTimeout(ask, 3000));
	setTimeout(ask, 800);
}

// screenshots that cannot be loaded are hidden; when none can, a note says why
function initScreenshots() {
	const strip = document.querySelector('[data-screenshots]');
	if (!strip) {
		return;
	}
	const images = [...strip.querySelectorAll('img')];
	let failed = 0;
	const fail = image => {
		image.closest('a').hidden = true;
		failed++;
		if (failed === images.length) {
			strip.hidden = true;
			document.querySelector('[data-screenshot-note]').hidden = false;
		}
	};
	images.forEach(image => {
		if (image.complete && image.naturalWidth === 0) {
			fail(image);
		} else {
			image.addEventListener('error', () => fail(image), { once: true });
		}
	});
}

// covers and pictures that cannot be loaded (a network that blocks Nintendo, a cover gone)
// show the placeholder instead of a broken image
function initImageFallbacks() {
	document.addEventListener('error', e => {
		const image = e.target;
		if (!(image instanceof HTMLImageElement) || image.dataset.fallback || image.classList.contains('title-screenshot')) {
			return;
		}
		if (image.closest('.game-cover, .dlc-tile-image, .title-hero, .dlc-game, .mini-card, .td-img') || image.classList.contains('td-img')) {
			image.dataset.fallback = 'true';
			image.src = '/resources/static/noimage.png';
		}
	}, true);
}

// pages of cards tell the server how many cards fit in a row, which rounds the cards per
// page to whole rows; the first time, or when the width changed, the page is drawn again
function initFullRows() {
	const grid = document.querySelector('.game-grid');
	if (!grid) {
		return;
	}
	const columns = () => getComputedStyle(grid).gridTemplateColumns.split(' ').filter(Boolean).length;
	const stored = () => {
		const match = document.cookie.match(/(?:^|;\s*)slm-columns=(\d+)/);
		return match ? Number(match[1]) : 0;
	};
	const remember = count => {
		document.cookie = `slm-columns=${count}; path=/; max-age=31536000; SameSite=Lax`;
	};
	const count = columns();
	if (count < 2 || count === stored()) {
		return;
	}
	remember(count);
	// once per width, so a page that cannot fill its rows is never drawn again and again
	const key = `slm-columns-reloaded-${count}`;
	let reloaded = false;
	try {
		reloaded = sessionStorage.getItem(key) === '1';
		sessionStorage.setItem(key, '1');
	} catch (e) {
		reloaded = true;
	}
	if (!reloaded && document.querySelector('.pagination')) {
		window.location.reload();
	}
	let timer = null;
	window.addEventListener('resize', () => {
		clearTimeout(timer);
		timer = setTimeout(() => {
			const now = columns();
			if (now >= 2 && now !== stored()) {
				remember(now);
			}
		}, 300);
	});
}

// emptying a search of a page, with the cross of the field or by deleting the text, shows
// everything again at once
function initSearchClear() {
	document.addEventListener('input', e => {
		const input = e.target;
		if (!input.matches || !input.matches('form input[type="search"][name="q"]') || input.value !== '' || input.defaultValue === '') {
			return;
		}
		if (input.form.requestSubmit) {
			input.form.requestSubmit();
		} else {
			input.form.submit();
		}
	});
}

// buttons that copy a text, such as the ID of a game
// copyText puts a text in the clipboard; without HTTPS (a NAS opened by its address) the
// browser has no clipboard API, and a selected field is copied instead
function copyText(text) {
	if (navigator.clipboard && window.isSecureContext) {
		return navigator.clipboard.writeText(text);
	}
	return new Promise((resolve, reject) => {
		const field = document.createElement('textarea');
		field.value = text;
		field.setAttribute('readonly', '');
		field.style.position = 'fixed';
		field.style.opacity = '0';
		document.body.appendChild(field);
		field.select();
		const copied = document.execCommand('copy');
		field.remove();
		if (copied) {
			resolve();
		} else {
			reject(new Error('not copied'));
		}
	});
}

// buttons that copy a text, such as the ID of a game, and say so for a moment
function initCopyButtons() {
	document.addEventListener('click', e => {
		const button = e.target.closest('[data-copy]');
		if (!button) {
			return;
		}
		copyText(button.dataset.copy).then(() => {
			button.classList.add('is-copied');
			setTimeout(() => button.classList.remove('is-copied'), 1200);
			const label = button.querySelector('[data-copy-label]');
			if (label && button.dataset.copied) {
				const text = label.textContent;
				label.textContent = button.dataset.copied;
				setTimeout(() => {
					label.textContent = text;
				}, 1500);
			}
		}).catch(() => {});
	});
}

// Issues: delete the files of a kind of issue that are of no use, after a confirmation
function initIssueDelete() {
	document.querySelectorAll('[data-issue-delete]').forEach(button => button.addEventListener('click', () => {
		if (!window.confirm(button.dataset.confirm)) {
			return;
		}
		button.disabled = true;
		runAsTask(() => postForm('/issues/delete', { kind: button.dataset.issueDelete }))
			.catch(error => {
				button.disabled = false;
				insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), error.message);
			});
	}));
}

// Issues: keep this copy of a duplicate, the library is scanned again
function initIssueKeep() {
	document.querySelectorAll('[data-issue-keep]').forEach(button => button.addEventListener('click', () => {
		button.disabled = true;
		runAsTask(() => postForm('/issues/keep', { path: button.dataset.issueKeep }))
			.catch(error => {
				button.disabled = false;
				insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), error.message);
			});
	}));
}

// the charts of the history: the values of the day under the pointer
function initHistoryCharts() {
	document.querySelectorAll('.history-charts [data-bs-toggle="pill"]').forEach((button) => {
		button.addEventListener('click', (event) => {
			event.preventDefault();
			Tab.getOrCreateInstance(button).show();
		});
	});
	document.querySelectorAll('[data-history-chart]').forEach((box) => {
		let days;
		try {
			days = JSON.parse(box.dataset.historyChart);
		} catch {
			return;
		}
		const svg = box.querySelector('svg');
		const cursor = box.querySelector('.history-chart-cursor');
		const tip = box.querySelector('.history-chart-tip');
		const labels = Array.from(box.closest('figure').querySelectorAll('[data-series-label]'));
		if (!svg || !cursor || !tip || !days.length) {
			return;
		}
		const show = (event) => {
			const rect = svg.getBoundingClientRect();
			const x = ((event.clientX - rect.left) / rect.width) * 600;
			let day = days[0];
			for (const candidate of days) {
				if (Math.abs(candidate.x - x) < Math.abs(day.x - x)) {
					day = candidate;
				}
			}
			cursor.setAttribute('x1', day.x);
			cursor.setAttribute('x2', day.x);
			cursor.removeAttribute('hidden');
			tip.replaceChildren();
			const date = document.createElement('div');
			date.className = 'fw-semibold';
			date.textContent = day.d;
			tip.append(date);
			day.v.forEach((value, i) => {
				const row = document.createElement('div');
				const dot = labels[i] ? labels[i].querySelector('.chart-dot') : null;
				if (dot) {
					row.append(dot.cloneNode());
					row.append(' ');
				}
				row.append(value);
				tip.append(row);
			});
			tip.hidden = false;
			const left = (day.x / 600) * rect.width;
			const width = tip.offsetWidth;
			tip.style.left = `${Math.min(Math.max(left - width / 2, 0), rect.width - width)}px`;
		};
		const hide = () => {
			cursor.setAttribute('hidden', '');
			tip.hidden = true;
		};
		svg.addEventListener('pointermove', show);
		svg.addEventListener('pointerdown', show);
		svg.addEventListener('pointerleave', hide);
	});
}

// the tune-up: "Review again" scans the library, and while steps are worked out the page
// asks again by itself
function initMaintenance() {
	const button = document.querySelector('[data-maintenance-review]');
	if (button) {
		button.addEventListener('click', () => {
			button.disabled = true;
			fetch('/maintenance/review', { method: 'POST' })
				.then(() => setTimeout(() => window.location.reload(), 1500))
				.catch(() => {
					button.disabled = false;
				});
		});
	}
	if (document.querySelector('[data-maintenance-preparing]')) {
		setTimeout(() => {
			if (document.visibilityState === 'visible') {
				window.location.reload();
			} else {
				document.addEventListener('visibilitychange', () => window.location.reload(), { once: true });
			}
		}, 3000);
	}
}

function initRegionRemove() {
	document.querySelectorAll('[data-region-remove]').forEach(button => {
		button.addEventListener('click', () => {
			if (!window.confirm(button.dataset.confirmText)) {
				return;
			}
			button.disabled = true;
			postForm('/space/regions/remove', { id: button.dataset.regionRemove })
				.then(() => {
					button.closest('.item-row').classList.add('is-removed');
					setTimeout(() => window.location.reload(), 1200);
				})
				.catch(error => {
					button.disabled = false;
					insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), error.message);
				});
		});
	});
}

// Settings > App on your phone: the install prompt of the browser when it offers one
// (Chrome, Edge and Samsung Internet, over HTTPS), the code to open the server on the phone,
// and the guide of the device first. The prompt is caught as soon as the page loads.
let installPrompt = null;
