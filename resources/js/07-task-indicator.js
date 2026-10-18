// The Tasks link of the menu and the live Tasks page.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

// replace covers that cannot be loaded (e.g. the Nintendo servers are not reachable)
document.addEventListener('error', e => {
	const image = e.target;
	if (image.tagName === 'IMG' && !image.dataset.fallback && !image.classList.contains('title-screenshot')) {
		image.dataset.fallback = 'true';
		image.src = '/resources/static/noimage.png';
	}
}, true);

// the Tasks page follows the tasks live: the server sends an event after every change
// and the list is rendered again by the server
// The Tasks link of the menu turns while a task runs and shows the failed ones. The page draws
// it once, so it follows the tasks live: on the Tasks page with its list, elsewhere only while
// a task runs, until the last one ends (also when it is cancelled).
function taskLinks() {
	return [...document.querySelectorAll('.app-nav a.nav-link[href="/tasks.html"]')];
}

function updateTaskIndicator() {
	if (taskLinks().length === 0) {
		return Promise.resolve(0);
	}
	return fetch('/api/tasks', { cache: 'no-store', headers: { Accept: 'application/json' } })
		.then(response => (response.ok ? response.json() : Promise.reject(response)))
		.then(tasks => {
			const active = tasks.filter(task => task.status === 'running');
			const running = active.length;
			const failed = tasks.filter(task => task.status === 'failed').length;
			// the progress of the first task that knows it, for the ring around the icon
			const measured = active.find(task => task.total > 0 && task.current >= 0);
			const percent = measured ? Math.min(100, Math.round(measured.current * 100 / measured.total)) : -1;
			taskLinks().forEach(link => {
				const icon = link.querySelector('.bi');
				if (icon) {
					icon.classList.toggle('bi-fan', running > 0);
					icon.classList.toggle('nav-busy', running > 0);
					icon.classList.toggle('bi-list-task', running === 0);
				}
				link.classList.toggle('is-working', running > 0);
				link.classList.toggle('is-measured', percent >= 0);
				link.style.setProperty('--task-pct', String(Math.max(percent, 0)));
				let badge = link.querySelector('.nav-count');
				if (failed > 0) {
					if (!badge) {
						badge = document.createElement('span');
						badge.className = 'badge rounded-pill nav-count text-bg-danger ms-auto ms-xxl-0';
						link.appendChild(badge);
					}
					badge.textContent = String(failed);
				} else if (badge) {
					badge.remove();
				}
			});
			return running;
		})
		.catch(() => -1);
}

function initTaskIndicator() {
	// the Tasks page updates it with its list (initLiveTasks)
	if (document.querySelector('[data-live-tasks]') || !window.EventSource || taskLinks().length === 0) {
		return;
	}
	// always followed: a task started after the page was drawn (a button of the page, a
	// synchronization, a scheduled or automatic one) shows too. The events come with every
	// change of a task, several a second during a compression: the icon follows them at most
	// every second and a half.
	let waiting = null;
	let again = false;
	const update = () => {
		if (waiting) {
			again = true;
			return;
		}
		updateTaskIndicator();
		waiting = setTimeout(() => {
			waiting = null;
			if (again) {
				again = false;
				update();
			}
		}, 1500);
	};
	let source = null;
	const open = () => {
		source = new EventSource('/api/tasks/events');
		source.addEventListener('tasks', update);
	};
	if (taskLinks().some(link => link.querySelector('.nav-busy'))) {
		update();
	}
	open();
	window.addEventListener('pagehide', () => source.close());
	// back to the page from the history of the browser: the connection was closed on leaving
	window.addEventListener('pageshow', e => {
		if (e.persisted) {
			open();
		}
	});
}

// keepAnimationsInStep makes the spinners and striped bars just drawn go on where the ones
// they replace were, as they all follow the clock of the page, instead of starting again
function keepAnimationsInStep(root) {
	const now = document.timeline ? document.timeline.currentTime : performance.now();
	root.querySelectorAll('.spinner-border').forEach(element => {
		const duration = parseFloat(getComputedStyle(element).animationDuration) * 1000;
		if (duration > 0) {
			element.style.animationDelay = `-${now % duration}ms`;
		}
	});
	// the stripes move on a layer of their own, one second per stripe
	root.querySelectorAll('.progress-bar-animated').forEach(element => {
		element.style.setProperty('--stripe-delay', `-${now % 1000}ms`);
	});
}

function initLiveTasks() {
	const list = document.querySelector('[data-live-tasks]');
	if (!list) {
		return;
	}

	keepAnimationsInStep(list);
	let rendered = Number(list.dataset.version || 0);
	let loading = false;
	let pending = false;
	// while a list of files is being scrolled (the bar dragged, a finger sliding) it is not
	// drawn again: the new list would end the gesture every second
	let scrolledAt = 0;
	let holding = false;
	let waiting = null;
	// the scroll the page puts back after drawing the list is not the user's
	let restoredAt = 0;
	list.addEventListener('scroll', () => {
		if (Date.now() - restoredAt > 250) {
			scrolledAt = Date.now();
		}
	}, true);
	list.addEventListener('pointerdown', e => {
		holding = Boolean(e.target.closest('details[data-task-results]'));
	});
	list.addEventListener('touchstart', e => {
		holding = Boolean(e.target.closest('details[data-task-results]'));
	}, { passive: true });
	['pointerup', 'pointercancel', 'touchend', 'touchcancel'].forEach(name => window.addEventListener(name, () => {
		holding = false;
	}, { passive: true }));
	const refresh = () => {
		if (loading) {
			pending = true;
			return;
		}
		if (holding || Date.now() - scrolledAt < 1500) {
			if (!waiting) {
				waiting = setTimeout(() => {
					waiting = null;
					refresh();
				}, 700);
			}
			return;
		}
		loading = true;
		fetch('/tasks.html?part=list', { cache: 'no-store' })
			.then(response => response.ok ? response.text() : Promise.reject(response))
			.then(html => {
				// every bar (the total and the one of each file) goes on from where it was
				// instead of jumping: by task, the total first, then the files by name
				const barKey = (card, bar) => card.dataset.taskId + '|' + (bar.closest('.task-lane')?.querySelector('[title]')?.title || '');
				const widths = {};
				list.querySelectorAll('[data-task-id]').forEach(card => {
					card.querySelectorAll('.progress-bar').forEach(bar => {
						widths[barKey(card, bar)] = bar.style.width;
					});
				});
				// a list of files opened or folded stays as it was
				// the bars of what a compression saved grow once, when they appear
				const shown = new Set([...list.querySelectorAll('[data-task-saving]')].map(saving => saving.dataset.taskSaving));
				const folded = {};
				// and so does where a long list was scrolled to
				const scrolled = {};
				const scroller = details => [...details.querySelectorAll('*')].find(element => element.scrollHeight > element.clientHeight + 1 && getComputedStyle(element).overflowY !== 'visible');
				list.querySelectorAll('details[data-task-results]').forEach(details => {
					folded[details.dataset.taskResults] = details.open;
					const box = scroller(details);
					if (box && box.scrollTop > 0) {
						scrolled[details.dataset.taskResults] = box.scrollTop;
					}
				});
				list.innerHTML = html;
				keepAnimationsInStep(list);
				list.querySelectorAll('[data-task-saving]').forEach(saving => {
					if (shown.has(saving.dataset.taskSaving)) {
						saving.closest('li, article').querySelectorAll('.saving-bar .progress').forEach(part => {
							part.style.animation = 'none';
						});
					}
				});
				list.querySelectorAll('details[data-task-results]').forEach(details => {
					if (details.dataset.taskResults in folded) {
						details.open = folded[details.dataset.taskResults];
					}
					if (details.dataset.taskResults in scrolled) {
						const box = scroller(details);
						if (box) {
							restoredAt = Date.now();
							box.scrollTop = scrolled[details.dataset.taskResults];
						}
					}
				});
				updateTaskIndicator();
				list.querySelectorAll('[data-task-id]').forEach(card => {
					card.querySelectorAll('.progress-bar').forEach(bar => {
						const before = widths[barKey(card, bar)];
						if (!before || before === bar.style.width) {
							return;
						}
						const after = bar.style.width;
						bar.style.transition = 'none';
						bar.style.width = before;
						void bar.offsetWidth;
						bar.style.transition = '';
						bar.style.width = after;
					});
				});
			})
			.catch(() => {
				// the next event tries again
			})
			.finally(() => {
				loading = false;
				if (pending) {
					pending = false;
					refresh();
				}
			});
	};

	if (window.EventSource) {
		let source = null;
		const open = () => {
			source = new EventSource('/api/tasks/events');
			source.addEventListener('tasks', e => {
				const version = Number(e.data);
				if (version !== rendered) {
					rendered = version;
					refresh();
				}
			});
		};
		open();
		window.addEventListener('pagehide', () => source.close());
		// back to the page from the history of the browser: the connection was closed on
		// leaving, and the tasks went on meanwhile
		window.addEventListener('pageshow', e => {
			if (e.persisted) {
				open();
				refresh();
			}
		});
	} else {
		setInterval(refresh, 3000);
	}

	list.addEventListener('click', e => {
		const dismiss = e.target.closest('[data-task-dismiss]');
		const clear = e.target.closest('[data-task-clear]');
		if (!dismiss && !clear) {
			return;
		}
		const button = dismiss || clear;
		button.disabled = true;
		// a group of tasks shown once is dismissed together
		const urls = dismiss ? dismiss.dataset.taskDismiss.split(',').map(id => `/api/tasks/${encodeURIComponent(id)}/dismiss`) : ['/api/tasks/clear'];
		Promise.all(urls.map(url => fetch(url, { method: 'POST' })))
			.then(refresh)
			.catch(() => {
				button.disabled = false;
			});
	});
}

