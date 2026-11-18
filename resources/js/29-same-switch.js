// Settings > Notifications: a switch that is the same setting as a switch of another section, as the
// message of the automations and of the save vault (#189). It has no name, so only the switch of
// its section is saved; changing either one changes the other.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.
document.addEventListener('change', event => {
	const input = event.target;
	if (!(input instanceof HTMLInputElement) || input.type !== 'checkbox' || !input.id) {
		return;
	}
	const twins = input.dataset.sameAs
		? [document.getElementById(input.dataset.sameAs)]
		: [...document.querySelectorAll(`[data-same-as="${CSS.escape(input.id)}"]`)];
	twins.forEach(twin => {
		if (twin && twin.checked !== input.checked) {
			twin.checked = input.checked;
			// the form sees the change of the switch it saves
			twin.dispatchEvent(new Event('change', { bubbles: true }));
		}
	});
});
