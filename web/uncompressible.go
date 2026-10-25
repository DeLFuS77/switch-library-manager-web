package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/dtrunk90/switch-library-manager-web/process"
)

// UNCOMPRESSIBLE_FILENAME holds the files the app tried to compress and could not shrink at
// all: they are not offered again while they keep their size, so a compression of the whole
// library does not stop at them each time.
const UNCOMPRESSIBLE_FILENAME = "uncompressible.json"

type uncompressibleStore struct {
	mutex sync.Mutex
	path  string
	files map[string]uncompressibleFile
}

type uncompressibleFile struct {
	Size int64 `json:"size"`
	// the size is the one of the file; before 1.41.4 it was the one of the progress, the size
	// times the passes of the task (see get)
	Exact bool `json:"exact,omitempty"`
	// a game in sparse storage, a layout no compressor reads yet
	Sparse bool `json:"sparse,omitempty"`
}

// UncompressibleEntry is a file of the library the Compress page says cannot be compressed.
type UncompressibleEntry struct {
	Name   string
	Path   string
	Size   int64
	Sparse bool
}

func (web *Web) uncompressible() *uncompressibleStore {
	web.uncompressibleOnce.Do(func() {
		store := &uncompressibleStore{path: filepath.Join(web.dataFolder, UNCOMPRESSIBLE_FILENAME), files: map[string]uncompressibleFile{}}
		if data, err := os.ReadFile(store.path); err == nil {
			parseStore(store.path, data, &store.files)
		}
		if store.files == nil {
			store.files = map[string]uncompressibleFile{}
		}
		web.uncompressibleStore = store
	})
	return web.uncompressibleStore
}

// add remembers a file that could not be compressed, with its size.
func (s *uncompressibleStore) add(path string, size int64, sparse bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.files[path] = uncompressibleFile{Size: size, Sparse: sparse, Exact: true}
	if data, err := json.Marshal(s.files); err == nil {
		writeFileAtomic(s.path, data)
	}
}

// follow keeps the files the organizer moved under their new path, and forgets the ones it
// deleted: a file moved keeps its content, and would otherwise be offered and tried again
// (every game in sparse storage, once Organize gave the library folders with IDs).
func (s *uncompressibleStore) follow(operations []process.Operation) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	changed := false
	for _, op := range operations {
		file, ok := s.files[op.From]
		if !ok || op.Error != "" {
			continue
		}
		switch op.Kind {
		case process.OP_MOVE:
			s.files[op.To] = file
			delete(s.files, op.From)
			changed = true
		case process.OP_DELETE:
			delete(s.files, op.From)
			changed = true
		}
	}
	if !changed {
		return
	}
	if data, err := json.Marshal(s.files); err == nil {
		writeFileAtomic(s.path, data)
	}
}

// get tells whether a file of this size could not be compressed; a file replaced by another
// one (another size) is tried again.
func (s *uncompressibleStore) get(path string, size int64) (uncompressibleFile, bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	file, ok := s.files[path]
	if !ok || size <= 0 {
		return file, false
	}
	if file.Size == size {
		return file, true
	}
	// noted by an older version, with the size of the progress: the passes of a task are a few
	return file, !file.Exact && file.Size%size == 0 && file.Size/size <= 4
}

// uncompressibleFiles are the files of the library that could not be compressed, the largest
// first.
func (web *Web) uncompressibleFiles() []UncompressibleEntry {
	store := web.uncompressible()
	entries := []UncompressibleEntry{}
	for _, candidate := range web.libraryFiles(".nsp", ".xci") {
		if file, ok := store.get(candidate.Path, candidate.Size); ok {
			entries = append(entries, UncompressibleEntry{Name: candidate.Name, Path: candidate.Path, Size: candidate.Size, Sparse: file.Sparse})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Size > entries[j].Size })
	return entries
}
