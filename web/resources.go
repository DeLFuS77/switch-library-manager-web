package web

import (
	"bytes"
	"fmt"
	"io/fs"
	"log"
	"net/http"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

func (web *Web) HandleResources() {
	fSys, err := fs.Sub(web.embedFS, "resources/static")
	if err != nil {
		web.sugarLogger.Error(fmt.Errorf("getting static files failed: %w", err))
		log.Fatal(err)
	}
	http.Handle("/resources/static/", http.StripPrefix("/resources/static/", http.FileServer(http.FS(fSys))))

	fSys, err = fs.Sub(web.embedFS, "node_modules")
	if err != nil {
		web.sugarLogger.Error(fmt.Errorf("getting vendor files failed: %w", err))
		log.Fatal(err)
	}
	http.Handle("/resources/vendor/", http.StripPrefix("/resources/vendor/", http.FileServer(http.FS(fSys))))

	// the service worker is served from the root so it can handle every page; its cache
	// is named after the version, so a new version replaces the cached files
	serviceWorker, err := fs.ReadFile(web.embedFS, "resources/static/sw.js")
	if err != nil {
		web.sugarLogger.Error(fmt.Errorf("getting the service worker failed: %w", err))
		log.Fatal(err)
	}
	serviceWorker = bytes.ReplaceAll(serviceWorker, []byte("__VERSION__"), []byte(settings.SLM_WEB_VERSION))
	http.HandleFunc("/sw.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(serviceWorker)
	})
}
