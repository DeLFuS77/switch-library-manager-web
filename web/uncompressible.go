package web

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/dtrunk90/switch-library-manager-web/process"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// UNCOMPRESSIBLE_FILENAME holds the files the app tried to compress and could not shrink at
// all: they are not offered again while they keep their size, so a compression of the whole
// library does not stop at them each time.
const UNCOMPRESSIBLE_FILENAME = "uncompressible.json"

// why nothing of a file can be compressed, kept with it
const (
	// a game in sparse storage, a layout no compressor reads yet
	UNCOMPRESSIBLE_SPARSE = "sparse"
	// an NSZ or XCZ under the name of an NSP or XCI
	UNCOMPRESSIBLE_ALREADY = "already"
	// NCA files with no game data: only control, manual or meta files
	UNCOMPRESSIBLE_NO_DATA = "no_data"
	// sections in an encryption no compressor reads
	UNCOMPRESSIBLE_ENCRYPTION = "encryption"
)

// the label the Compress page gives each reason, and the note of a task
var uncompressibleLabels = map[string]string{
	UNCOMPRESSIBLE_SPARSE:     "Sparse storage",
	UNCOMPRESSIBLE_ALREADY:    "Compressed already",
	UNCOMPRESSIBLE_NO_DATA:    "No game data",
	UNCOMPRESSIBLE_ENCRYPTION: "Encryption",
}

var uncompressibleNotes = map[string]string{
	UNCOMPRESSIBLE_SPARSE:     NOTE_COMPRESS_SPARSE,
	UNCOMPRESSIBLE_ALREADY:    NOTE_COMPRESS_ALREADY,
	UNCOMPRESSIBLE_NO_DATA:    NOTE_COMPRESS_NO_DATA,
	UNCOMPRESSIBLE_ENCRYPTION: NOTE_COMPRESS_ENCRYPTION,
}

// uncompressibleReason is why err says nothing of a file can be compressed, empty when it
// says another thing or no reason.
func uncompressibleReason(err error) string {
	switch {
	case errors.Is(err, switchfs.ErrSparseStorage):
		return UNCOMPRESSIBLE_SPARSE
	case errors.Is(err, switchfs.ErrAlreadyCompressed):
		return UNCOMPRESSIBLE_ALREADY
	case errors.Is(err, switchfs.ErrNotCompressible) && strings.Contains(err.Error(), "encryption the app does not read"):
		return UNCOMPRESSIBLE_ENCRYPTION
	case errors.Is(err, switchfs.ErrNotCompressible) && (strings.Contains(err.Error(), "holds no game data") || strings.Contains(err.Error(), "no NCA file but metadata")):
		return UNCOMPRESSIBLE_NO_DATA
	}
	return ""
}

type uncompressibleStore struct {
	mutex sync.Mutex
	path  string
	files map[string]uncompressibleFile
	// the files without a reason looked at since the app started, by path and size
	looked map[string]bool
	// a look for the reasons is running
	looking atomic.Bool
}

type uncompressibleFile struct {
	Size int64 `json:"size"`
	// the size is the one of the file; before 1.41.4 it was the one of the progress, the size
	// times the passes of the task (see get)
	Exact bool `json:"exact,omitempty"`
	// a game in sparse storage, the only reason older versions kept
	Sparse bool `json:"sparse,omitempty"`
	// why nothing of it can be compressed, one of UNCOMPRESSIBLE_*; empty when not known
	Reason string `json:"reason,omitempty"`
}

// reason is why nothing of the file can be compressed, empty when it is not known.
func (f uncompressibleFile) reason() string {
	if f.Reason == "" && f.Sparse {
		return UNCOMPRESSIBLE_SPARSE
	}
	return f.Reason
}

// UncompressibleEntry is a file of the library the Compress page says cannot be compressed.
type UncompressibleEntry struct {
	Name string
	Path string
	Size int64
	// why, in a few words to translate; empty when it is not known
	Label string
}

func (web *Web) uncompressible() *uncompressibleStore {
	web.uncompressibleOnce.Do(func() {
		store := &uncompressibleStore{path: filepath.Join(web.dataFolder, UNCOMPRESSIBLE_FILENAME), files: map[string]uncompressibleFile{}, looked: map[string]bool{}}
		if data, ok := readStore(store.path); ok {
			parseStore(store.path, data, &store.files)
		}
		if store.files == nil {
			store.files = map[string]uncompressibleFile{}
		}
		web.uncompressibleStore = store
	})
	return web.uncompressibleStore
}

// add remembers a file that could not be compressed, with its size and why.
func (s *uncompressibleStore) add(path string, size int64, reason string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.files[path] = uncompressibleFile{Size: size, Sparse: reason == UNCOMPRESSIBLE_SPARSE, Exact: true, Reason: reason}
	s.save()
}

// setReason keeps why a file noted before could not be compressed, while it keeps its size.
func (s *uncompressibleStore) setReason(path string, size int64, reason string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	file, ok := s.files[path]
	if !ok || file.Size != size && file.Exact {
		return
	}
	file.Reason, file.Sparse = reason, reason == UNCOMPRESSIBLE_SPARSE
	s.files[path] = file
	s.save()
}

// save must be called with the mutex held.
func (s *uncompressibleStore) save() {
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
// first. The reason of the ones noted without it, by an older version, is looked for in the
// background, once while the app runs: the page shows it once it is known.
func (web *Web) uncompressibleFiles() []UncompressibleEntry {
	store := web.uncompressible()
	entries := []UncompressibleEntry{}
	unknown := []UncompressibleEntry{}
	for _, candidate := range web.libraryFiles(".nsp", ".xci") {
		if file, ok := store.get(candidate.Path, candidate.Size); ok {
			entry := UncompressibleEntry{Name: candidate.Name, Path: candidate.Path, Size: candidate.Size, Label: uncompressibleLabels[file.reason()]}
			entries = append(entries, entry)
			if file.reason() == "" {
				unknown = append(unknown, entry)
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Size > entries[j].Size })
	web.lookForUncompressibleReasons(unknown)
	return entries
}

// lookForUncompressibleReasons finds why the files noted without a reason cannot be
// compressed, reading their headers as a compression would, without writing anything.
func (web *Web) lookForUncompressibleReasons(entries []UncompressibleEntry) {
	store := web.uncompressible()
	if len(entries) == 0 || !store.looking.CompareAndSwap(false, true) {
		return
	}
	todo := []UncompressibleEntry{}
	store.mutex.Lock()
	for _, entry := range entries {
		key := entry.Path + "|" + strconv.FormatInt(entry.Size, 10)
		if !store.looked[key] {
			store.looked[key] = true
			todo = append(todo, entry)
		}
	}
	store.mutex.Unlock()
	if len(todo) == 0 {
		store.looking.Store(false)
		return
	}
	web.backgroundWork.Add(1)
	web.backgroundStarted.Store(true)
	go func() {
		defer web.backgroundWork.Add(-1)
		defer store.looking.Store(false)
		found := false
		for _, entry := range todo {
			if reason := uncompressibleReason(switchfs.NotCompressible(entry.Path)); reason != "" {
				store.setReason(entry.Path, entry.Size, reason)
				found = true
			}
		}
		if found {
			web.invalidateDerived()
		}
	}()
}
