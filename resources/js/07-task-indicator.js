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
			// on phones the menu is closed: the fan next to Synchronize says work goes on
			document.querySelectorAll('[data-header-tasks]').forEach(link => {
				link.hidden = running === 0;
				link.title = t('Tasks running: %v', running);
				const count = link.querySelector('[data-header-tasks-count]');
				if (count) {
					count.textContent = running > 1 ? String(running) : '';
				}
			});
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
				// how many tasks run, in violet, and how many failed, in red
				const setBadge = (selector, className, count, label, before) => {
					let badge = link.querySelector(selector);
					if (count > 0) {
						if (!badge) {
							badge = document.createElement('span');
							badge.className = className;
							link.insertBefore(badge, before ? link.querySelector('.nav-count') : null);
						}
						badge.textContent = String(count);
						badge.title = label;
						const hidden = document.createElement('span');
						hidden.className = 'visually-hidden';
						hidden.textContent = ' ' + label;
						badge.appendChild(hidden);
					} else if (badge) {
						badge.remove();
					}
				};
				setBadge('.nav-running', 'badge rounded-pill nav-count nav-running ms-auto ms-xxl-0', running, t('Tasks running: %v', running), true);
				setBadge('.nav-count.text-bg-danger', 'badge rounded-pill nav-count text-bg-danger ms-xxl-0', failed, t('Failed tasks: %v', failed), false);
				const failedBadge = link.querySelector('.nav-count.text-bg-danger');
				if (failedBadge) {
					failedBadge.classList.toggle('ms-auto', running === 0);
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
	if (taskLinks().some(link => link.querySelector('.nav-busy'))) {
		update();
	}
	followTaskEvents(update);
}

// followTaskEvents calls changed on every change of the tasks, while the page is seen. A
// browser opens few connections at once to the same server (six), and each page of the app
// left open in a tab would keep one: with several tabs, the requests of the page seen waited
// for good. A tab in the background closes its connection, and looks again once it is seen.
function followTaskEvents(changed) {
	let source = null;
	const open = () => {
		if (source || document.hidden) {
			return;
		}
		source = new EventSource('/api/tasks/events');
		source.addEventListener('tasks', changed);
	};
	const close = () => {
		if (source) {
			source.close();
			source = null;
		}
	};
	open();
	document.addEventListener('visibilitychange', () => {
		if (document.hidden) {
			close();
		} else {
			open();
			// what changed meanwhile
			changed();
		}
	});
	window.addEventListener('pagehide', close);
	// back to the page from the history of the browser: the connection was closed on leaving
	window.addEventListener('pageshow', e => {
		if (e.persisted) {
			open();
			changed();
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
		// an answer that never comes is given up: the next change asks again
		const controller = new AbortController();
		const timeout = setTimeout(() => controller.abort(), 20000);
		fetch('/tasks.html?part=list', { cache: 'no-store', signal: controller.signal })
			.then(response => response.ok ? response.text() : Promise.reject(response))
			.then(html => new Promise(drawn => {
				const moving = !window.matchMedia('(prefers-reduced-motion: reduce)').matches;
				const draw = () => {
					// where the next file of each task was shown, and the files being worked on: the
					// next file moves up into the place of the one that ended, instead of jumping
					const nextRows = {};
					const lanesBefore = new Set();
					list.querySelectorAll('[data-task-id]').forEach(card => {
						const row = card.querySelector('.task-next');
						nextRows[card.dataset.taskId] = row ? { name: row.querySelector('[title]')?.title, top: row.getBoundingClientRect().top } : null;
						card.querySelectorAll('.task-lane [title]').forEach(name => lanesBefore.add(card.dataset.taskId + '|' + name.title));
					});
					// every bar (the total and the one of each file) goes on from where it was
					// instead of jumping: by task, the total first, then the files by name, in their lanes
					// or on their covers. Any other bar has no key: with the one of the total, the total
					// and every cover would go on from the width of whichever bar came last
					const barKey = (card, bar) => {
						const file = bar.closest('.task-lane, .task-tile');
						if (file) {
							const name = file.querySelector('[title]')?.title;
							// a new step of a file starts its bar again: it does not shrink back from the last one
							return name ? card.dataset.taskId + '|' + name + '|' + (file.dataset.step || '') : null;
						}
						return bar.classList.contains('task-total-bar') ? card.dataset.taskId + '|' : null;
					};
					const widths = {};
					const shares = {};
					list.querySelectorAll('[data-task-id]').forEach(card => {
						card.querySelectorAll('.progress-bar').forEach(bar => {
							if (barKey(card, bar) === null) {
								return;
							}
							widths[barKey(card, bar)] = bar.style.width;
							// the share done, which sets the colours of the total
							shares[barKey(card, bar)] = bar.style.getPropertyValue('--pct');
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
					// the covers already drawn stay: new images of the same cover would be drawn again on
					// every update, and blink
					const covers = new Map();
					list.querySelectorAll('img.task-lane-cover, img.task-next-cover, img.task-tile-img').forEach(image => {
						if (image.complete && image.naturalWidth > 0) {
							const key = image.className + ' ' + image.getAttribute('src');
							if (!covers.has(key)) {
								covers.set(key, []);
							}
							covers.get(key).push(image);
						}
					});
					// the tasks of the queue marked to take out stay marked
					const marked = new Set([...list.querySelectorAll('[data-queue-item]:checked')].map(box => box.value));
					list.innerHTML = html;
					applyTaskSections(list);
					list.querySelectorAll('[data-queue-item]').forEach(box => {
						box.checked = marked.has(box.value);
					});
					updateQueueButton(list);
					list.querySelectorAll('img.task-lane-cover, img.task-next-cover, img.task-tile-img').forEach(image => {
						const kept = covers.get(image.className + ' ' + image.getAttribute('src'));
						if (kept && kept.length) {
							image.replaceWith(kept.shift());
						}
					});
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
							if (barKey(card, bar) === null) {
								return;
							}
							const before = widths[barKey(card, bar)];
							if (before) {
								// drawn already: it does not grow from nothing again on every update
								bar.style.animation = 'none';
							}
							if (!before || before === bar.style.width) {
								return;
							}
							const after = bar.style.width;
							const share = bar.style.getPropertyValue('--pct');
							const shareBefore = shares[barKey(card, bar)];
							bar.style.transition = 'none';
							bar.style.width = before;
							if (share && shareBefore) {
								bar.style.setProperty('--pct', shareBefore);
							}
							void bar.offsetWidth;
							bar.style.transition = '';
							bar.style.width = after;
							if (share && shareBefore) {
								bar.style.setProperty('--pct', share);
							}
						});
					});
					if (moving) {
						list.querySelectorAll('[data-task-id]').forEach(card => {
							if (!(card.dataset.taskId in nextRows)) {
								return;
							}
							const before = nextRows[card.dataset.taskId];
							card.querySelectorAll('.task-lane').forEach(lane => {
								const name = lane.querySelector('[title]')?.title;
								if (!name || lanesBefore.has(card.dataset.taskId + '|' + name)) {
									return;
								}
								if (before && before.name === name) {
									const rise = before.top - lane.getBoundingClientRect().top;
									lane.animate([{ transform: `translateY(${rise}px)`, opacity: 0.6 }, { transform: 'none', opacity: 1 }], { duration: 480, easing: 'cubic-bezier(.2, .7, .2, 1)' });
								} else {
									lane.animate([{ transform: 'translateY(10px)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 360, easing: 'ease-out' });
								}
							});
							const row = card.querySelector('.task-next');
							if (row && (!before || before.name !== row.querySelector('[title]')?.title)) {
								row.animate([{ transform: 'translateY(10px)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 360, delay: 180, easing: 'ease-out', fill: 'backwards' });
							}
						});
					}
					drawn();
				};
				// a bar that goes away because its file or its task ended well is seen reaching the
				// end first: the file a worker finished, or the whole task. Not a task cancelled,
				// failed or paused
				const next = document.createElement('template');
				next.innerHTML = html;
				const barKey = (card, bar) => card.dataset.taskId + '|' + (bar.closest('.task-lane')?.querySelector('[title]')?.title || '');
				const nextBars = new Set();
				next.content.querySelectorAll('[data-task-id]').forEach(card => {
					card.querySelectorAll('.progress-bar').forEach(bar => nextBars.add(barKey(card, bar)));
				});
				const endedWell = id => [...next.content.querySelectorAll('[data-task-dismiss]')].some(button =>
					button.dataset.taskDismiss.split(',').includes(id) && button.closest('.task-success, .task-warning'));
				const ending = [];
				list.querySelectorAll('[data-task-id]').forEach(card => {
					const running = next.content.querySelector('[data-task-id="' + card.dataset.taskId + '"]');
					if (card.classList.contains('is-paused') || (!running && !endedWell(card.dataset.taskId))) {
						return;
					}
					card.querySelectorAll('.progress-bar').forEach(bar => {
						if (!nextBars.has(barKey(card, bar)) && bar.style.width && parseFloat(bar.style.width) < 100) {
							ending.push(bar);
						}
					});
				});
				// a drawing that fails still lets the next ones happen
				const drawSafely = () => {
					try {
						draw();
					} catch (error) {
						drawn();
						throw error;
					}
				};
				if (!ending.length || document.hidden) {
					drawSafely();
					return;
				}
				ending.forEach(bar => {
					bar.style.width = '100%';
					if (bar.style.getPropertyValue('--pct')) {
						bar.style.setProperty('--pct', '100');
					}
					// the file that ended rises and fades once its bar is full, making room for the next
					const lane = bar.closest('.task-lane');
					const card = bar.closest('[data-task-id]');
					if (moving && lane && next.content.querySelector('[data-task-id="' + card.dataset.taskId + '"]')) {
						lane.animate([{ transform: 'none', opacity: 1 }, { transform: 'translateY(-14px)', opacity: 0 }], { duration: 260, delay: 520, easing: 'ease-in', fill: 'forwards' });
					}
				});
				setTimeout(drawSafely, moving ? 800 : 250);
			}))
			.catch(() => {
				// the next event tries again
			})
			.finally(() => {
				clearTimeout(timeout);
				loading = false;
				if (pending) {
					pending = false;
					refresh();
				}
			});
	};

	if (window.EventSource) {
		followTaskEvents(e => {
			// seen again: the list is drawn anew, the version is not known
			if (!e || !e.data) {
				refresh();
				return;
			}
			const version = Number(e.data);
			if (version !== rendered) {
				rendered = version;
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

// The queue of the tasks: the ones marked are taken out before they start.
function updateQueueButton(scope) {
	const button = scope.querySelector('[data-queue-remove]');
	if (button) {
		button.disabled = scope.querySelectorAll('[data-queue-item]:checked').length === 0;
	}
}

function initQueue() {
	document.addEventListener('change', e => {
		if (e.target.matches('[data-queue-item]')) {
			updateQueueButton(document);
		}
	});
	// one place earlier or later; the list is drawn again in its new order
	document.addEventListener('click', e => {
		const move = e.target.closest('[data-queue-move]');
		if (!move) {
			return;
		}
		move.disabled = true;
		postForm('/api/queue/move', { id: move.dataset.queueId, dir: move.dataset.queueMove }).catch(error => {
			move.disabled = false;
			insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), error.message || t('Could not reach the server.'));
		});
	});
	document.addEventListener('click', e => {
		const button = e.target.closest('[data-queue-remove]');
		if (!button) {
			return;
		}
		const ids = [...document.querySelectorAll('[data-queue-item]:checked')].map(box => box.value);
		if (ids.length === 0) {
			return;
		}
		button.disabled = true;
		// the list is drawn again without them once the server has taken them out
		postForm('/api/queue/remove', { id: ids.join(',') }).catch(error => {
			button.disabled = false;
			insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), error.message || t('Could not reach the server.'));
		});
	});
}