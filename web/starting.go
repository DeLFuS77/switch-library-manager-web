package web

import (
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

// startingStep is what the app is doing while it starts, in English, to translate.
var startingStep atomic.Pointer[string]

func setStartingStep(step string) {
	startingStep.Store(&step)
}

func currentStartingStep(lang string) string {
	if step := startingStep.Load(); step != nil {
		return translate(lang, *step)
	}
	return ""
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

var startingPage = template.Must(template.New("starting").Parse(`<!doctype html>
<html lang="{{.Lang}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="dark light">
<title>{{.Title}} - Switch Library Manager</title>
<style>
:root { color-scheme: dark light; --bg: #0d1117; --fg: #e6edf3; --muted: #9aa4b2; --accent: #e60012; }
@media (prefers-color-scheme: light) { :root { --bg: #f6f7f9; --fg: #1d2330; --muted: #5b6475; } }
* { box-sizing: border-box; }
body { margin: 0; min-height: 100vh; display: grid; place-items: center; padding: 1.5rem; background: var(--bg); color: var(--fg); font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; text-align: center; }
main { max-width: 26rem; }
.spinner { width: 3.2rem; height: 3.2rem; margin: 0 auto 1.5rem; border-radius: 50%; border: .3rem solid color-mix(in srgb, var(--accent) 20%, transparent); border-top-color: var(--accent); animation: spin .9s linear infinite; }
h1 { margin: 0 0 .6rem; font-size: 1.35rem; }
p { margin: 0; color: var(--muted); line-height: 1.5; }
.step { min-height: 1.5em; margin-bottom: .9rem; color: var(--fg); font-weight: 600; }
@keyframes spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .spinner { animation-duration: 3s; } }
</style>
</head>
<body>
<main role="status" aria-live="polite">
<div class="spinner" aria-hidden="true"></div>
<h1>{{.Title}}</h1>
<p class="step" id="step">{{.Step}}</p>
<p>{{.Text}}</p>
</main>
<script src="/starting.js"></script>
</body>
</html>
`))

// startingScript asks the app whether it is ready every second, shows what it is doing and
// opens the page once it is ready. It is a file of its own: the pages only run the scripts
// of the app (Content-Security-Policy).
const startingScript = `(function () {
	function check() {
		fetch('/healthz', { cache: 'no-store' }).then(function (response) {
			if (response.ok) {
				window.location.reload();
				return;
			}
			// while starting, the health check says what the app is doing
			return response.text().then(function (text) {
				if (text) {
					document.getElementById('step').textContent = text;
				}
				setTimeout(check, 1000);
			});
		}).catch(function () {
			setTimeout(check, 1500);
		});
	}
	setTimeout(check, 1000);
})();
`

// startingHandler answers while the app gets ready: pages show that it is starting and open
// by themselves once it is ready, the health check says it is not ready yet, and the rest is
// asked to come back.
func startingHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Retry-After", "2")
		if r.URL.Path == "/healthz" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			step := currentStartingStep(startingLanguage(r))
			if step == "" {
				step = "starting"
			}
			w.Write([]byte(step))
			return
		}
		if r.URL.Path == "/starting.js" {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			w.Write([]byte(startingScript))
			return
		}
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.Contains(r.Header.Get("Accept"), "text/html") {
			lang := startingLanguage(r)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			startingPage.Execute(w, map[string]string{
				"Lang":  lang,
				"Title": translate(lang, "Starting the app..."),
				"Step":  currentStartingStep(lang),
				"Text":  translate(lang, "After an update or a restart this takes a moment. The page opens by itself when the app is ready."),
			})
			return
		}
		http.Error(w, "starting", http.StatusServiceUnavailable)
	})
}
