// A box in a long list of choices of the filters (the publishers) leaves the choices whose name
// has what is typed, whatever the case and the accents.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

function filterFindText(text) {
	return text.normalize('NFD').replace(/[̀-ͯ]/g, '').toLowerCase();
}

document.addEventListener('input', event => {
	const box = event.target.closest('[data-filter-find]');
	if (!box) {
		return;
	}
	const wanted = filterFindText(box.value.trim());
	box.closest('.filter-more').querySelectorAll('.filter-choice').forEach(choice => {
		choice.hidden = wanted !== '' && !filterFindText(choice.textContent).includes(wanted);
	});
});

// Enter in that box only finds: it does not send the search of the page the box is in
document.addEventListener('keydown', event => {
	if (event.key === 'Enter' && event.target.closest('[data-filter-find]')) {
		event.preventDefault();
	}
});
