// A box in a long list of choices of the filters (the publishers) leaves the choices whose name
// has what is typed, whatever the case and the accents. In the publishers, a button then chooses
// all the publishers left at once, besides the ones already chosen (#214).
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

// the most publishers a filter takes at once (maxPublishers in web/filter.go)
const FILTER_MAX_PUBLISHERS = 50;
// what joins the publishers chosen (publisherSeparator in web/filter.go)
const FILTER_PUBLISHER_SEPARATOR = '|';

function filterFindText(text) {
	return text.normalize('NFD').replace(/[̀-ͯ]/g, '').toLowerCase();
}

// filterFindChosen is the publishers already chosen in a group of publishers
function filterFindChosen(group) {
	return (group.dataset.publisherChosen || '').split(FILTER_PUBLISHER_SEPARATOR).filter(name => name !== '');
}

// filterFindLeft is the names of the publishers of the group left by what is typed and not chosen yet
function filterFindLeft(group, wanted) {
	const chosen = new Set(filterFindChosen(group).map(name => name.toLowerCase()));
	const names = new Set();
	group.querySelectorAll('.filter-choice[data-publisher]').forEach(choice => {
		const name = choice.dataset.publisher;
		if (filterFindText(name).includes(wanted) && !chosen.has(name.toLowerCase())) {
			names.add(name);
		}
	});
	return [...names];
}

document.addEventListener('input', event => {
	const box = event.target.closest('[data-filter-find]');
	if (!box) {
		return;
	}
	const wanted = filterFindText(box.value.trim());
	const group = box.closest('[data-publisher-group]');
	// in the publishers the box finds in all of them, the first ones too
	(group || box.closest('.filter-more')).querySelectorAll(group ? '.filter-choice[data-publisher]' : '.filter-choice').forEach(choice => {
		choice.hidden = wanted !== '' && !filterFindText(choice.textContent).includes(wanted);
	});
	const all = group && group.querySelector('[data-filter-find-all]');
	if (all) {
		const left = wanted === '' ? [] : filterFindLeft(group, wanted);
		const fits = left.length > 0 && left.length + filterFindChosen(group).length <= FILTER_MAX_PUBLISHERS;
		all.hidden = !fits;
		all.textContent = t('Choose the %v shown', left.length);
	}
});

// The button chooses the publishers left, besides the ones chosen
document.addEventListener('click', event => {
	const all = event.target.closest('[data-filter-find-all]');
	if (!all) {
		return;
	}
	const group = all.closest('[data-publisher-group]');
	const box = group.querySelector('[data-filter-find]');
	const names = filterFindChosen(group).concat(filterFindLeft(group, filterFindText(box.value.trim())));
	const address = new URL(group.dataset.publisherBase, window.location.href);
	address.searchParams.set('publisher', names.join(FILTER_PUBLISHER_SEPARATOR));
	window.location.href = address.toString();
});

// Enter in that box only finds: it does not send the search of the page the box is in
document.addEventListener('keydown', event => {
	if (event.key === 'Enter' && event.target.closest('[data-filter-find]')) {
		event.preventDefault();
	}
});
