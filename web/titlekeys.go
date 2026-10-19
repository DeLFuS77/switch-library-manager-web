package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// Some NSP have no ticket, and title.keys does not have their title key: their contents cannot
// be decrypted, so they cannot be compressed nor checked whole, and a compression of them fails
// halfway. After a scan the files of the library are looked at once (the headers of their NCA
// and their tickets only), the result kept while a file stays the same: Issues lists the ones
// without their key, and Compress no longer offers them.

const TITLEKEYS_FILENAME = "titlekeys.json"

type titleKeyCheck struct {
	Size    int64 `json:"size"`
	ModTime int64 `json:"modTime"`
	// the rights ID whose title key is missing, or "" when the file has its keys
	Missing string `json:"missing,omitempty"`
}

type titleKeyStore struct {
	mutex   sync.Mutex
	path    string
	files   map[string]titleKeyCheck
	running sync.Mutex
}

func (web *Web) titleKeys() *titleKeyStore {
	web.titleKeysOnce.Do(func() {
		store := &titleKeyStore{path: filepath.Join(web.dataFolder, TITLEKEYS_FILENAME), files: map[string]titleKeyCheck{}}
		if data, err := os.ReadFile(store.path); err == nil {
			parseStore(store.path, data, &store.files)
		}
		if store.files == nil {
			store.files = map[string]titleKeyCheck{}
		}
		web.titleKeysStore = store
	})
	return web.titleKeysStore
}

func (s *titleKeyStore) set(path string, check titleKeyCheck) {
	s.mutex.Lock()
	s.files[path] = check
	s.mutex.Unlock()
}

func (s *titleKeyStore) save() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if data, err := json.Marshal(s.files); err == nil {
		writeFileAtomic(s.path, data)
	}
}

// current is what is known of a file as it is now.
func (s *titleKeyStore) current(path string, info os.FileInfo) (titleKeyCheck, bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	check, ok := s.files[path]
	return check, ok && check.Size == info.Size() && check.ModTime == info.ModTime().UnixNano()
}

// checkTitleKeys looks at the files of the library not looked at yet, or changed since, and
// again at the ones that missed their key (title.keys may have it now).
func (web *Web) checkTitleKeys() {
	if keys, _ := settings.SwitchKeys(); keys == nil || keys.GetKey("header_key") == "" {
		return
	}
	store := web.titleKeys()
	if !store.running.TryLock() {
		return
	}
	defer store.running.Unlock()
	changed := false
	seen := map[string]bool{}
	for _, file := range web.libraryFiles(".nsp", ".nsz") {
		seen[file.Path] = true
		info, err := os.Stat(file.Path)
		if err != nil || info.IsDir() {
			continue
		}
		if check, ok := store.current(file.Path, info); ok && check.Missing == "" {
			continue
		}
		missing, err := switchfs.MissingTitleKey(file.Path)
		if err != nil {
			continue
		}
		before, known := store.current(file.Path, info)
		if !known || before.Missing != missing {
			changed = true
		}
		store.set(file.Path, titleKeyCheck{Size: info.Size(), ModTime: info.ModTime().UnixNano(), Missing: missing})
	}
	// the files gone are forgotten
	store.mutex.Lock()
	for path := range store.files {
		if !seen[path] {
			delete(store.files, path)
		}
	}
	store.mutex.Unlock()
	store.save()
	if changed {
		web.invalidateDerived()
	}
}

// noteMissingTitleKey keeps a file a compression found without its title key.
func (web *Web) noteMissingTitleKey(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	missing, err := switchfs.MissingTitleKey(path)
	if err != nil || missing == "" {
		return
	}
	store := web.titleKeys()
	store.set(path, titleKeyCheck{Size: info.Size(), ModTime: info.ModTime().UnixNano(), Missing: missing})
	store.save()
}

// missingTitleKeys are the files of the library without their title key, as they are now:
// path → rights ID.
func (web *Web) missingTitleKeys() map[string]string {
	store := web.titleKeys()
	store.mutex.Lock()
	candidates := map[string]titleKeyCheck{}
	for path, check := range store.files {
		if check.Missing != "" {
			candidates[path] = check
		}
	}
	store.mutex.Unlock()
	result := map[string]string{}
	for path, check := range candidates {
		if info, err := os.Stat(path); err == nil && info.Size() == check.Size && info.ModTime().UnixNano() == check.ModTime {
			result[path] = check.Missing
		}
	}
	return result
}
