// Your year on Switch.
// One of the files joined into resources/static/web.js by scripts/build-js.mjs, in the order of their names.

// Your year on Switch: the summary is drawn as an image (1080 x 1350, the size social networks
// show best) and saved, to share it. Only covers of the app itself are drawn, so the picture
// can always be saved.
function initYearReview() {
	const hero = document.querySelector('[data-year-review]');
	const button = document.querySelector('[data-year-download]');
	if (!hero || !button) {
		return;
	}
	const data = hero.dataset;
	const text = selector => (document.querySelector(selector)?.textContent || '').trim();

	const loadImage = src => new Promise(resolve => {
		const img = new Image();
		img.onload = () => resolve(img);
		img.onerror = () => resolve(null);
		img.src = src;
	});

	const roundRect = (ctx, x, y, w, h, r) => {
		ctx.beginPath();
		ctx.moveTo(x + r, y);
		ctx.arcTo(x + w, y, x + w, y + h, r);
		ctx.arcTo(x + w, y + h, x, y + h, r);
		ctx.arcTo(x, y + h, x, y, r);
		ctx.arcTo(x, y, x + w, y, r);
		ctx.closePath();
	};

	const fit = (ctx, value, maxWidth) => {
		let result = value;
		while (result.length > 1 && ctx.measureText(result).width > maxWidth) {
			result = result.slice(0, -2) + '…';
		}
		return result;
	};

	const draw = async () => {
		const width = 1080;
		const height = 1350;
		const canvas = document.createElement('canvas');
		canvas.width = width;
		canvas.height = height;
		const ctx = canvas.getContext('2d');
		if (document.fonts && document.fonts.ready) {
			await document.fonts.ready;
		}
		const font = (weight, size) => `${weight} ${size}px "Inter Variable", system-ui, sans-serif`;

		// background: the colors of the page
		const background = ctx.createLinearGradient(0, 0, width, height);
		background.addColorStop(0, '#d62839');
		background.addColorStop(0.55, '#8e1d6b');
		background.addColorStop(1, '#1d4f91');
		ctx.fillStyle = background;
		ctx.fillRect(0, 0, width, height);
		const glow = ctx.createRadialGradient(width * 0.85, height * 0.1, 0, width * 0.85, height * 0.1, width * 0.6);
		glow.addColorStop(0, 'rgba(15, 181, 216, 0.55)');
		glow.addColorStop(1, 'rgba(15, 181, 216, 0)');
		ctx.fillStyle = glow;
		ctx.fillRect(0, 0, width, height);

		const margin = 80;
		ctx.fillStyle = 'rgba(255, 255, 255, 0.85)';
		ctx.font = font(700, 30);
		ctx.fillText(text('.year-kicker').toUpperCase(), margin, 130);
		ctx.fillStyle = '#fff';
		ctx.font = font(800, 92);
		ctx.fillText(fit(ctx, text('[data-year-title]'), width - margin * 2), margin, 240);

		// the four numbers
		const stats = [
			[data.games, data.labelGames], [data.updates, data.labelUpdates],
			[data.dlc, data.labelDlc], [data.space, data.labelSpace],
		];
		const boxWidth = (width - margin * 2 - 30) / 2;
		stats.forEach(([value, label], i) => {
			const x = margin + (i % 2) * (boxWidth + 30);
			const y = 300 + Math.floor(i / 2) * 200;
			ctx.fillStyle = 'rgba(255, 255, 255, 0.14)';
			roundRect(ctx, x, y, boxWidth, 170, 32);
			ctx.fill();
			ctx.fillStyle = '#fff';
			ctx.font = font(800, 72);
			ctx.fillText(fit(ctx, String(value), boxWidth - 60), x + 36, y + 92);
			ctx.fillStyle = 'rgba(255, 255, 255, 0.8)';
			ctx.font = font(600, 28);
			ctx.fillText(fit(ctx, label, boxWidth - 60), x + 36, y + 138);
		});

		// the highlights
		const facts = [
			[data.labelGenre, text('[data-year-genre]')],
			[data.labelMonth, data.month],
			[data.labelBiggest, text('[data-year-biggest]')],
		].filter(([, value]) => value);
		let y = 760;
		facts.forEach(([label, value]) => {
			ctx.fillStyle = 'rgba(255, 255, 255, 0.75)';
			ctx.font = font(700, 24);
			ctx.fillText(label.toUpperCase(), margin, y);
			ctx.fillStyle = '#fff';
			ctx.font = font(700, 40);
			ctx.fillText(fit(ctx, value, width - margin * 2), margin, y + 48);
			y += 104;
		});

		// up to six covers of the new games
		const covers = [...document.querySelectorAll('[data-year-games] img')]
			.map(img => img.currentSrc || img.src)
			.filter(src => src.startsWith(window.location.origin))
			.slice(0, 6);
		const images = (await Promise.all(covers.map(loadImage))).filter(Boolean);
		const size = 130;
		const gap = (width - margin * 2 - size * 6) / 5;
		images.forEach((img, i) => {
			const x = margin + i * (size + gap);
			ctx.save();
			roundRect(ctx, x, 1100, size, size, 22);
			ctx.clip();
			ctx.drawImage(img, x, 1100, size, size);
			ctx.restore();
		});

		ctx.fillStyle = 'rgba(255, 255, 255, 0.7)';
		ctx.font = font(600, 24);
		ctx.fillText('Switch Library Manager Web', margin, height - 50);
		return canvas;
	};

	button.addEventListener('click', async () => {
		button.disabled = true;
		try {
			const canvas = await draw();
			const blob = await new Promise(resolve => canvas.toBlob(resolve, 'image/png'));
			if (!blob) {
				return;
			}
			const link = document.createElement('a');
			link.href = URL.createObjectURL(blob);
			link.download = `switch-${(text('[data-year-title]').match(/\d{4}/) || ['year'])[0]}.png`;
			document.body.appendChild(link);
			link.click();
			link.remove();
			setTimeout(() => URL.revokeObjectURL(link.href), 1000);
		} finally {
			button.disabled = false;
		}
	});
}

