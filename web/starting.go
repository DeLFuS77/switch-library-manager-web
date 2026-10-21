package web

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strings"
	"sync/atomic"
)

// switchHandler serves with the handler set last: the page of the start while the app gets
// ready, then the app.
type switchHandler struct {
	handler atomic.Pointer[http.Handler]
}

func (s *switchHandler) set(handler http.Handler) {
	s.handler.Store(&handler)
}

func (s *switchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	(*s.handler.Load()).ServeHTTP(w, r)
}

// startingSteps are what the app does while it starts, in their order, in English to
// translate.
var startingSteps = []string{
	"Opening the local database...",
	"Loading the keys...",
	"Checking the users...",
	"Preparing the pages...",
}

// startingStep is the step the app is on while it starts.
var startingStep atomic.Int32

func setStartingStep(step string) {
	for i, known := range startingSteps {
		if known == step {
			startingStep.Store(int32(i))
		}
	}
}

func currentStartingStep(lang string) string {
	return translate(lang, startingSteps[startingStep.Load()])
}

// startingLanguage is the language of the browser for the page of the start, which comes
// before the settings are read.
func startingLanguage(r *http.Request) string {
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		lang := strings.ToLower(strings.TrimSpace(strings.SplitN(strings.SplitN(part, ";", 2)[0], "-", 2)[0]))
		if isSupportedLanguage(lang) {
			return lang
		}
	}
	return DEFAULT_LANGUAGE
}

// StartingStepView is a step of the page of the start.
type StartingStepView struct {
	Text   string
	Status string
}

var startingPage = template.Must(template.New("starting").Parse(`<!doctype html>
<html lang="{{.Lang}}" data-theme="dark">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="dark light">
<title>{{.Title}} - Switch Library Manager</title>
<link rel="icon" href="/resources/static/icon.svg" type="image/svg+xml">
<script src="/starting.js"></script>
<style>
:root {
	--red: #d62839; --cyan: #0fb5d8; --green: #1f9d55;
	--bg: #0d1117; --fg: #e6edf3; --muted: #9aa4b2; --surface: rgba(22, 27, 34, .72); --border: rgba(255, 255, 255, .09); --track: rgba(255, 255, 255, .07);
}
[data-theme="light"] {
	--bg: #f4f6fa; --fg: #1d2330; --muted: #5b6475; --surface: rgba(255, 255, 255, .8); --border: rgba(20, 30, 50, .1); --track: rgba(20, 30, 50, .08);
}
* { box-sizing: border-box; }
html, body { height: 100%; }
body {
	margin: 0; display: grid; place-items: center; padding: 1.5rem; overflow: hidden;
	background: var(--bg); color: var(--fg);
	font-family: "Inter", system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
}
/* the glows of the app, drifting slowly */
body::before, body::after {
	content: ""; position: fixed; width: 60vmax; height: 60vmax; border-radius: 50%; filter: blur(90px); opacity: .28; z-index: -1;
	animation: drift 18s ease-in-out infinite alternate;
}
body::before { background: var(--red); top: -25vmax; left: -20vmax; }
body::after { background: var(--cyan); bottom: -28vmax; right: -22vmax; animation-delay: -9s; }
[data-theme="light"] body::before, [data-theme="light"] body::after { opacity: .16; }
@keyframes drift { to { transform: translate(6vmax, 4vmax) scale(1.08); } }

.card {
	width: min(100%, 27rem); padding: 2rem 2rem 1.6rem; text-align: center;
	background: var(--surface); border: 1px solid var(--border); border-radius: 1.4rem;
	box-shadow: 0 1.5rem 4rem rgba(0, 0, 0, .35); backdrop-filter: blur(18px);
}
.brand { display: inline-flex; align-items: center; gap: .6rem; margin-bottom: 1.6rem; font-weight: 800; letter-spacing: -.01em; }
.brand-mark { display: grid; place-items: center; width: 2.2rem; height: 2.2rem; border-radius: .7rem; background: linear-gradient(135deg, var(--red), #a3122a); box-shadow: 0 .4rem 1.2rem rgba(214, 40, 57, .45); }
.brand-mark img { width: 1.35rem; height: 1.35rem; }

.ring { position: relative; width: 4.6rem; height: 4.6rem; margin: 0 auto 1.3rem; }
.ring::before {
	content: ""; position: absolute; inset: 0; border-radius: 50%; padding: .32rem;
	background: conic-gradient(var(--red), var(--cyan), transparent 75%);
	-webkit-mask: linear-gradient(#000 0 0) content-box, linear-gradient(#000 0 0); -webkit-mask-composite: xor; mask-composite: exclude;
	animation: spin 1.1s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }

h1 { margin: 0 0 .35rem; font-size: 1.4rem; font-weight: 800; }
.lead { margin: 0 0 1.3rem; color: var(--muted); font-size: .92rem; line-height: 1.5; }

.bar { position: relative; height: .55rem; margin-bottom: 1.3rem; border-radius: 1rem; background: var(--track); overflow: hidden; }
.bar > span {
	position: absolute; inset: 0 auto 0 0; border-radius: inherit; overflow: hidden;
	background: linear-gradient(90deg, var(--red), var(--cyan)); transition: width .6s ease;
}
.bar > span::after {
	content: ""; position: absolute; inset: 0 0 0 -1rem;
	background-image: linear-gradient(45deg, rgba(255, 255, 255, .18) 25%, transparent 25%, transparent 50%, rgba(255, 255, 255, .18) 50%, rgba(255, 255, 255, .18) 75%, transparent 75%, transparent);
	background-size: 1rem 1rem; animation: stripes 1s linear infinite;
}
@keyframes stripes { to { transform: translateX(1rem); } }

ol { list-style: none; margin: 0; padding: 0; text-align: left; font-size: .9rem; }
li { display: flex; align-items: center; gap: .7rem; padding: .45rem .6rem; border-radius: .7rem; color: var(--muted); transition: color .3s, background-color .3s; }
li .dot { display: grid; place-items: center; flex: 0 0 auto; width: 1.35rem; height: 1.35rem; border-radius: 50%; border: 2px solid var(--track); font-size: .75rem; font-weight: 700; }
li.done { color: var(--fg); }
li.done .dot { border-color: var(--green); background: var(--green); color: #fff; }
li.current { color: var(--fg); font-weight: 600; background: rgba(15, 181, 216, .1); }
li.current .dot { border-color: var(--cyan); border-top-color: transparent; animation: spin .8s linear infinite; }
.footer { margin: 1.1rem 0 0; color: var(--muted); font-size: .78rem; }
@media (prefers-reduced-motion: reduce) {
	body::before, body::after, .bar > span::after { animation: none; }
	.ring::before, li.current .dot { animation-duration: 3s; }
}
</style>
</head>
<body>
<main class="card" role="status" aria-live="polite">
	<div class="brand"><span class="brand-mark"><img src="/resources/static/icon.svg" alt="" aria-hidden="true"></span>Switch Library Manager</div>
	<div class="ring" aria-hidden="true"></div>
	<h1>{{.Title}}</h1>
	<p class="lead">{{.Text}}</p>
	<div class="bar" aria-hidden="true"><span id="bar" style="width: {{.Percent}}%"></span></div>
	<ol id="steps">
		{{- range .Steps}}
		<li class="{{.Status}}"><span class="dot">{{if eq .Status "done"}}✓{{end}}</span>{{.Text}}</li>
		{{- end}}
	</ol>
	<p class="footer">{{.Footer}}</p>
</main>
</body>
</html>
`))

// startingScript shows the theme of the app, asks the app every second what it is doing and
// opens the page once it is ready. It is a file of its own: the pages only run the scripts
// of the app (Content-Security-Policy).
const startingScript = `(function () {
	try {
		var theme = localStorage.getItem('slm-theme') || 'dark';
		if (theme === 'auto') {
			theme = window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
		}
		document.documentElement.setAttribute('data-theme', theme);
	} catch (e) {}
	function show(state) {
		var steps = document.querySelectorAll('#steps li');
		for (var i = 0; i < steps.length; i++) {
			steps[i].className = i < state.step ? 'done' : (i === state.step ? 'current' : '');
			steps[i].querySelector('.dot').textContent = i < state.step ? '✓' : '';
		}
		var bar = document.getElementById('bar');
		if (bar) {
			bar.style.width = state.percent + '%';
		}
	}
	// a start of a second or two would only flash: the page stays at least this long, and its
	// steps are ticked one by one before the app opens
	var shown = Date.now();
	var least = 2500;
	function finish() {
		var steps = document.querySelectorAll('#steps li');
		var from = 0;
		for (var i = 0; i < steps.length; i++) {
			if (steps[i].className !== 'done') {
				from = i;
				break;
			}
			from = steps.length;
		}
		var tick = 350;
		for (var step = from + 1; step <= steps.length; step++) {
			(function (step) {
				setTimeout(function () {
					show({ step: step, percent: Math.round(step * 100 / steps.length) });
				}, (step - from) * tick);
			})(step);
		}
		var wait = Math.max((steps.length - from) * tick + 400, least - (Date.now() - shown));
		setTimeout(function () { window.location.reload(); }, wait);
	}
	function check() {
		fetch('/healthz?detail=1', { cache: 'no-store' }).then(function (response) {
			if (response.ok) {
				finish();
				return;
			}
			return response.json().then(function (state) {
				show(state);
				setTimeout(check, 1000);
			});
		}).catch(function () {
			setTimeout(check, 1500);
		});
	}
	setTimeout(check, 600);
})();
`

// startingPercent is how far the start is, for the bar.
func startingPercent() int {
	return (int(startingStep.Load())*100 + 50) / (len(startingSteps) + 1)
}

// startingHandler answers while the app gets ready: pages show that it is starting, with
// what it is doing, and open by themselves once it is ready; the health check says it is
// not ready yet, and the rest is asked to come back. icon is the mark of the app.
func startingHandler(icon []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Retry-After", "2")
		lang := startingLanguage(r)
		switch {
		case r.URL.Path == "/healthz":
			if r.URL.Query().Get("detail") != "" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				json.NewEncoder(w).Encode(map[string]any{"step": startingStep.Load(), "text": currentStartingStep(lang), "percent": startingPercent()})
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(currentStartingStep(lang)))
		case r.URL.Path == "/starting.js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			w.Write([]byte(startingScript))
		case r.URL.Path == "/resources/static/icon.svg" && len(icon) > 0:
			w.Header().Set("Content-Type", "image/svg+xml")
			w.Write(icon)
		case (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.Contains(r.Header.Get("Accept"), "text/html"):
			current := int(startingStep.Load())
			steps := []StartingStepView{}
			for i, step := range startingSteps {
				status := ""
				switch {
				case i < current:
					status = "done"
				case i == current:
					status = "current"
				}
				steps = append(steps, StartingStepView{Text: translate(lang, step), Status: status})
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			startingPage.Execute(w, map[string]any{
				"Lang":    lang,
				"Title":   translate(lang, "Starting the app..."),
				"Text":    translate(lang, "After an update or a restart this takes a moment. The page opens by itself when the app is ready."),
				"Footer":  translate(lang, "Your library, games and settings are safe; nothing is changed while the app starts."),
				"Steps":   steps,
				"Percent": startingPercent(),
			})
		default:
			http.Error(w, "starting", http.StatusServiceUnavailable)
		}
	})
}
