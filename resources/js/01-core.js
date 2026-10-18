// Translations, notifications and alerts, shared by every page.
// The files of this folder are joined into resources/static/web.js by scripts/build-js.mjs, in the
// order of their names, and share one scope.

// the translations of the page, as JSON data (inline scripts are not allowed)
function readTranslations() {
	try {
		const data = document.getElementById('slm-translations');
		return data ? JSON.parse(data.textContent) : {};
	} catch (e) {
		return {};
	}
}

// Bundled with Bootstrap by esbuild into resources/static/web.js (npm run build).
// Importing Bootstrap also enables its data attributes (offcanvas, dismissible alerts, ...).
import { Dropdown, Modal, Tab, Tooltip } from 'bootstrap';
import qrcode from 'qrcode-generator';

// Translations of the texts below are provided by the server in the page language.
// "%v" placeholders are replaced by the arguments in order.
function t(text, ...args) {
	const translations = window.SLM_TRANSLATIONS || readTranslations();
	let result = translations[text] || text;
	args.forEach(arg => {
		result = result.replace('%v', arg);
	});
	return result;
}

// Messages appear as notifications in a corner of the screen and go away by themselves:
// successes after a few seconds, errors after a little longer. Hovering keeps them, and the
// same message is shown once.
const TOAST_SUCCESS_MS = 5000;
const TOAST_ERROR_MS = 10000;
const MAX_TOASTS = 4;

function toastStack() {
	let stack = document.getElementById('toastStack');
	if (!stack) {
		stack = document.createElement('div');
		stack.id = 'toastStack';
		stack.className = 'toast-stack';
		stack.setAttribute('aria-live', 'polite');
		document.body.appendChild(stack);
	}
	return stack;
}

function closeToast(toast) {
	clearTimeout(toast.toastTimer);
	toast.classList.remove('show');
	setTimeout(() => toast.remove(), 200);
}

function startToastTimer(toast) {
	clearTimeout(toast.toastTimer);
	toast.toastTimer = setTimeout(() => closeToast(toast), toast.toastDuration);
}

function showToast(contextualClass, iconClass, strongMessage, message) {
	const stack = toastStack();
	const key = contextualClass + '|' + strongMessage + '|' + message;
	const existing = [...stack.children].find(toast => toast.dataset.toastKey === key);
	if (existing) {
		startToastTimer(existing);
		return existing;
	}

	const toast = document.createElement('div');
	toast.className = 'alert toast-note d-flex align-items-start gap-2 fade ' + contextualClass;
	if (contextualClass === 'alert-success') {
		playSound('done');
	}
	toast.setAttribute('role', contextualClass === 'alert-danger' ? 'alert' : 'status');
	toast.dataset.toastKey = key;
	toast.toastDuration = contextualClass === 'alert-danger' ? TOAST_ERROR_MS : TOAST_SUCCESS_MS;

	const icon = document.createElement('span');
	icon.classList.add('bi', iconClass, 'flex-shrink-0', 'mt-1');
	icon.setAttribute('aria-hidden', 'true');
	toast.appendChild(icon);

	const text = document.createElement('div');
	text.className = 'flex-grow-1';
	if (strongMessage) {
		const strong = document.createElement('strong');
		strong.textContent = strongMessage;
		text.appendChild(strong);
		text.appendChild(document.createTextNode(' '));
	}
	text.appendChild(document.createTextNode(message));
	toast.appendChild(text);

	const close = document.createElement('button');
	close.type = 'button';
	close.className = 'btn-close flex-shrink-0';
	close.setAttribute('aria-label', t('Close'));
	close.addEventListener('click', () => closeToast(toast));
	toast.appendChild(close);

	toast.addEventListener('mouseenter', () => clearTimeout(toast.toastTimer));
	toast.addEventListener('mouseleave', () => startToastTimer(toast));
	toast.addEventListener('focusin', () => clearTimeout(toast.toastTimer));
	toast.addEventListener('focusout', () => startToastTimer(toast));

	stack.appendChild(toast);
	while (stack.children.length > MAX_TOASTS) {
		closeToast(stack.firstElementChild);
		stack.firstElementChild.remove();
	}
	// reading the layout first makes the fade-in run, also in a tab in the background
	void toast.offsetWidth;
	toast.classList.add('show');
	startToastTimer(toast);
	return toast;
}

// the messages the server put in the page (after saving a form) become notifications
function initFlashMessages() {
	document.querySelectorAll('[data-flash]').forEach(alert => {
		const danger = alert.classList.contains('alert-danger');
		showToast(danger ? 'alert-danger' : 'alert-success', danger ? 'bi-exclamation-triangle-fill' : 'bi-check-circle-fill', '', alert.textContent.trim());
		alert.remove();
	});
}

function insertAlert(element, contextualClass, iconClass, strongMessage, message, dismissible = true, id = "") {
	// alerts with an id stay in the page and are updated (the synchronization progress)
	if (id == "") {
		showToast(contextualClass, iconClass, strongMessage, message);
		return;
	}
	const alert = document.createElement('div');
	alert.classList.add('alert', contextualClass, 'd-flex', 'align-items-center', 'fade', 'show');
	if (dismissible) {
		alert.classList.add('alert-dismissible');
	}
	alert.setAttribute('role', 'alert');
	if (id != "") {
		alert.id = id;
	}

	const icon = document.createElement('span');
	icon.classList.add('bi', iconClass, 'flex-shrink-0', 'me-2');
	alert.appendChild(icon);

	const fullMessageWrapper = document.createElement('div');

	if (strongMessage) {
		const strongMessageWrapper = document.createElement('strong');
		strongMessageWrapper.appendChild(document.createTextNode(strongMessage));
		fullMessageWrapper.appendChild(strongMessageWrapper);
		fullMessageWrapper.appendChild(document.createTextNode(' '));
	}

	fullMessageWrapper.appendChild(document.createTextNode(message));
	alert.appendChild(fullMessageWrapper);

	if (dismissible) {
		const closeBtn = document.createElement('button');
		closeBtn.setAttribute('aria-label', t('Close'));
		closeBtn.setAttribute('type', 'button');
		closeBtn.classList.add('btn-close');
		closeBtn.dataset.bsDismiss = 'alert';
		alert.appendChild(closeBtn);
	}

	element.insertBefore(alert, element.firstChild);
	if (element.tagName === 'FORM') {
		// long forms: bring the feedback into view
		alert.scrollIntoView({ behavior: 'smooth', block: 'center' });
	}
}

const SYNC_ALERT_ID = 'alert_sync';
const SYNC_POLL_INTERVAL = 1000;

