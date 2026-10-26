// Starts everything once the page is loaded.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

document.addEventListener('DOMContentLoaded', () => {
	if (restoreFilters()) {
		return;
	}
	linkRememberedFilters();
	const tooltipTriggerList = document.querySelectorAll('[data-bs-toggle="tooltip"]');
	[...tooltipTriggerList].map(tooltipTriggerEl => new Tooltip(tooltipTriggerEl));

	const sync = document.getElementById('sync');
	document.querySelectorAll('#sync, [data-sync-action]').forEach(link => {
		link.addEventListener('click', e => {
			e.preventDefault();
			startSync(sync.href);
		});
	});

	// a synchronization was already running when the page was rendered
	if (document.getElementById(SYNC_ALERT_ID)) {
		setSyncBusy(true);
		watchSync();
	}

	document.querySelectorAll('#settingsForm, #organizeForm').forEach(form => {
		form.addEventListener('submit', e => {
			e.preventDefault();
			onSubmit(form);
		});
	});

	initFlashMessages();
	initSaveWhenChanged();
	initKeysUpload();
	initPageTabs();
	initOrganize();
	initIgnoreButtons();
	initLibraryBulk();
	initCollections();
	initSdPlanner();
	initIgnoreTypes();
	initViewToggle();
	initLiveTasks();
	initCompress();

	// the answer of a backup request; one that is not JSON (a file too large for the server,
	// a proxy in between) is still an answer, with its status
	const readBackupAnswer = response => response.json()
		.catch(() => ({ globalError: { message: t('Unexpected server response (%v).', response.status) } }))
		.then(json => ({ ok: response.ok, json }));
	const backupFailed = json => {
		const error = (json && json.globalError) || {};
		insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', error.strongMessage || t('Error!'), error.message || t('Could not reach the server.'));
	};

	const restoreForm = document.getElementById('restoreForm');
	if (restoreForm) {
		restoreForm.addEventListener('submit', e => {
			e.preventDefault();
			const button = restoreForm.querySelector('[type=submit]');
			if (!window.confirm(button.dataset.confirm)) {
				return;
			}
			button.disabled = true;
			fetch('/backup/restore', { method: 'POST', body: new FormData(restoreForm) })
				.then(readBackupAnswer)
				.then(({ ok, json }) => {
					button.disabled = false;
					if (ok) {
						insertAlert(mainContainer(), 'alert-success', 'bi-check-circle-fill', json.strongMessage, json.message);
						setTimeout(() => window.location.reload(), 1500);
					} else {
						backupFailed(json);
					}
				})
				.catch(() => {
					button.disabled = false;
					backupFailed();
				});
		});
	}

	// copies of the configuration kept by the app: make one now, or restore one
	document.querySelectorAll('[data-auto-backup], [data-auto-restore]').forEach(button => {
		button.addEventListener('click', () => {
			const name = button.dataset.autoRestore;
			if (name && !window.confirm(button.dataset.confirm)) {
				return;
			}
			button.disabled = true;
			fetch(name ? `/backup/auto/${encodeURIComponent(name)}/restore` : '/backup/auto', { method: 'POST' })
				.then(readBackupAnswer)
				.then(({ ok, json }) => {
					if (ok) {
						insertAlert(mainContainer(), 'alert-success', 'bi-check-circle-fill', json.strongMessage, json.message);
						setTimeout(() => window.location.reload(), 1500);
					} else {
						button.disabled = false;
						backupFailed(json);
					}
				})
				.catch(() => {
					button.disabled = false;
					backupFailed();
				});
		});
	});

	// installable app; browsers only allow it on HTTPS or localhost
	if ('serviceWorker' in navigator && window.isSecureContext) {
		navigator.serviceWorker.register('/sw.js').catch(() => undefined);
	}

	// searches every missing cover again; the progress is shown in Tasks
	document.querySelectorAll('[data-covers-retry]').forEach(button => {
		button.addEventListener('click', () => {
			button.disabled = true;
			runAsTask(() => fetch('/covers/retry', { method: 'POST' })).then(response => {
				if (!response.ok) {
					button.disabled = false;
				}
			}).catch(() => {
				button.disabled = false;
			});
		});
	});

	initMarks();
	initWhatsNew();
	initSelfUpdate();
	initGamepad();
	initSounds();
	initCouchMode();
	initRevealOnScroll();
	initCollapsibleText();

	// selects and radio buttons that apply their form at once. With the keyboard the arrows of a
	// list only choose (each one would load the page again and leave the focus nowhere): Enter
	// or leaving the list applies the choice. The control has the focus again once the page is
	// loaded again.
	const REFOCUS_KEY = 'slm-refocus';
	try {
		const id = sessionStorage.getItem(REFOCUS_KEY);
		sessionStorage.removeItem(REFOCUS_KEY);
		const control = id && document.getElementById(id);
		if (control) {
			control.focus({ preventScroll: true });
		}
	} catch (e) {
		// not kept: the focus starts at the top
	}
	const apply = input => {
		if (!input.form) {
			return;
		}
		try {
			if (input.id) {
				sessionStorage.setItem(REFOCUS_KEY, input.id);
			}
		} catch (e) {
			// the focus is not given back
		}
		input.form.submit();
	};
	document.querySelectorAll('[data-autosubmit]').forEach(input => {
		if (input.tagName !== 'SELECT') {
			input.addEventListener('change', () => apply(input));
			return;
		}
		let byKeyboard = false;
		let chosen = input.value;
		input.addEventListener('keydown', e => {
			byKeyboard = true;
			if (e.key === 'Enter' && input.value !== chosen) {
				e.preventDefault();
				apply(input);
			}
		});
		input.addEventListener('pointerdown', () => {
			byKeyboard = false;
		});
		input.addEventListener('change', () => {
			if (!byKeyboard) {
				apply(input);
			}
		});
		input.addEventListener('blur', () => {
			if (byKeyboard && input.value !== chosen) {
				apply(input);
			}
			chosen = input.value;
		});
	});

	// notices that stay closed once closed (for a week, so they are not forgotten for good)
	document.querySelectorAll('[data-remember-close]').forEach(notice => {
		const key = notice.dataset.rememberClose;
		let closed = 0;
		try {
			closed = Number(localStorage.getItem(key) || 0);
		} catch (e) {
			// not remembered
		}
		if (Date.now() - closed > 7 * 24 * 3600 * 1000) {
			notice.hidden = false;
		} else {
			notice.hidden = true;
		}
		notice.addEventListener('closed.bs.alert', () => {
			try {
				localStorage.setItem(key, String(Date.now()));
			} catch (e) {
				// not remembered
			}
		});
	});

	// the notice of a new version, with its star on GitHub, shows on every page while there is
	// one: closing it hides it on this page only
	const updateNotice = document.querySelector('[data-update-version]');
	if (updateNotice) {
		updateNotice.hidden = false;
		try {
			localStorage.removeItem('slm-update-dismissed');
		} catch (e) {
			// nothing kept
		}
	}

	document.querySelectorAll('[data-verify]').forEach(button => {
		button.addEventListener('click', () => {
			button.disabled = true;
			const body = new URLSearchParams({ all: button.dataset.verify === 'all' ? 'true' : 'false', scope: button.dataset.verify });
			runAsTask(() => fetch('/verify/start', { method: 'POST', body })).then(response => {
				if (response.ok) {
					return response.json().catch(() => ({})).then(json => {
						if (json && json.queued) {
							button.disabled = false;
							insertQueuedNote(mainContainer(), json.queued);
						}
						if (json && json.nothing) {
							button.disabled = false;
							insertAlert(mainContainer(), 'alert-info', 'bi-check-circle-fill', t('Nothing to check'), t('No file is new or changed since the last check.'));
						}
					});
				}
				return response.json().catch(() => ({})).then(json => {
					button.disabled = false;
					const error = json.globalError || {};
					insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', error.strongMessage || t('Error!'), error.message || t('Unexpected server response (%v).', response.status));
				});
			}).catch(() => {
				button.disabled = false;
				insertAlert(mainContainer(), 'alert-danger', 'bi-exclamation-triangle-fill', t('Error!'), t('Could not reach the server.'));
			});
		});
	});
	initCountUp();
	initCoverLoading();
	initPauseOffscreen();
	initCardTilt();
	initSettingsSections();
	initCoverTransition();
	initQuickSearch();
	initTaskIndicator();
	initQueue();
	initYearReview();
	initAutomationSettings();
	initWizard();
	initRegionRemove();
	initRegionMain();
	initMaintenance();
	initHistoryCharts();
	initIssueDelete();
	initIssueKeep();
	initPlay();
	initSmartCollections();
	initActivityFilter();
	initIssueDeleteFile();
	initGameUpload();
	initSearchClear();
	initCopyButtons();
	initFullRows();
	initImageFallbacks();
	initScreenshots();
	initSpacePreparing();
	initPack();
	initMeasure();
	initHeroMenus();
	initVaultSettings();
	initMobileApp();
	initSaveDelete();

	// forms that delete something ask first
	document.querySelectorAll('form[data-confirm]').forEach(form => {
		form.addEventListener('submit', e => {
			if (!window.confirm(form.dataset.confirm)) {
				e.preventDefault();
			}
		});
	});
	initNotificationTest();
	initThemeSwitcher();
	initBulkActions();
}, false);
