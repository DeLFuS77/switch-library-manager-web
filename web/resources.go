package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log"
	"net/http"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// filesOnly serves files but not the lists of the folders.
type filesOnly struct {
	fs.FS
}

func (f filesOnly) Open(name string) (fs.File, error) {
	file, err := f.FS.Open(name)
	if err != nil {
		return nil, err
	}
	if info, err := file.Stat(); err != nil || info.IsDir() {
		file.Close()
		return nil, fs.ErrNotExist
	}
	return file, nil
}

// assetVersion identifies the styles and scripts of this build; their addresses carry it, so
// browsers and the service worker load them again when they change, even in the same version
var assetVersion = "1"

func computeAssetVersion(files fs.FS) string {
	hash := sha256.New()
	for _, name := range []string{"web.css", "web.js", "theme.js"} {
		if data, err := fs.ReadFile(files, name); err == nil {
			hash.Write(data)
		}
	}
	return hex.EncodeToString(hash.Sum(nil))[:12]
}

// cached serves the files of the build with what lets browsers keep them: the styles and
// scripts whose address carries the version of their contents for good, the rest (fonts,
// icons) for a day, then asked again by the tag of the build, which answers "not modified".
// Without it every page loaded them all again: about 280 KB, a third of a second on a NAS.
func cached(files http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("v") == assetVersion {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=86400")
		}
		// the files are part of the build, so its version and the one of its styles and scripts
		// tell them; the file server answers a request with the same tag "not modified"
		w.Header().Set("ETag", `"`+settings.SLM_WEB_VERSION+"-"+assetVersion+`"`)
		files.ServeHTTP(w, r)
	})
}

func (web *Web) HandleResources() {
	fSys, err := fs.Sub(web.embedFS, "resources/static")
	if err != nil {
		web.sugarLogger.Error(fmt.Errorf("getting static files failed: %w", err))
		log.Fatal(err)
	}
	assetVersion = computeAssetVersion(fSys)
	http.Handle("/resources/static/", cached(http.StripPrefix("/resources/static/", http.FileServer(http.FS(filesOnly{fSys})))))

	fSys, err = fs.Sub(web.embedFS, "node_modules")
	if err != nil {
		web.sugarLogger.Error(fmt.Errorf("getting vendor files failed: %w", err))
		log.Fatal(err)
	}
	http.Handle("/resources/vendor/", cached(http.StripPrefix("/resources/vendor/", http.FileServer(http.FS(filesOnly{fSys})))))

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
