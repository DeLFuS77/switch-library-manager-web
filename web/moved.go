package web

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"

	"github.com/dtrunk90/switch-library-manager-web/process"
)

// Organize renames and moves files; the library knows only after the scan that follows, which
// takes minutes on a NAS. A page drawn before, or a task started meanwhile (or resumed after a
// restart), still has the old paths: the tasks look here where each file went.

// movedFilesLimit bounds the memory: past it the oldest moves go, the scans having long
// caught up with them
const movedFilesLimit = 200000

// how many moves are followed one after the other: Organize run again on files it moved
const movedFilesHops = 16

type movedFiles struct {
	mutex sync.Mutex
	// the new path of a file, by its old one
	to map[string]string
}

// record keeps the moves of Organize that went through.
func (m *movedFiles) record(operations []process.Operation) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	for _, op := range operations {
		if op.Error != "" || op.Kind != process.OP_MOVE {
			continue
		}
		if m.to == nil || len(m.to) >= movedFilesLimit {
			m.to = map[string]string{}
		}
		m.to[op.From] = op.To
	}
}

// resolve is where the file at path is now, as far as the app moved it.
func (m *movedFiles) resolve(path string) string {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	for hop := 0; hop < movedFilesHops; hop++ {
		next, ok := m.to[path]
		if !ok || next == path {
			break
		}
		path = next
	}
	return path
}

// resolveAll is resolve for each path, in a new list.
func (m *movedFiles) resolveAll(paths []string) []string {
	resolved := make([]string, len(paths))
	for i, path := range paths {
		resolved[i] = m.resolve(path)
	}
	return resolved
}

// resolveKeys is a map by path with each path where its file is now: the tasks look their
// files up by the paths they work on, resolved.
func (m *movedFiles) resolveKeys(byPath map[string]string) map[string]string {
	resolved := make(map[string]string, len(byPath))
	for path, value := range byPath {
		resolved[m.resolve(path)] = value
	}
	return resolved
}

// fileThere tells whether the file of a task is where its list has it: errFileGone when it
// is not (deleted, or moved by something else than the app).
func (web *Web) fileThere(path string) error {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %s", errFileGone, path)
	}
	return nil
}

// libraryPaths are the requested paths that are files among the candidates, each one once, as
// the library has them and never as they were sent: only files of the library are touched. A
// page drawn before Organize moved files sends their old paths, and the library may still list
// them until its next scan: both are taken where the files are now.
func (web *Web) libraryPaths(requested []string, candidates []CompressCandidate) []string {
	known := map[string]string{}
	for _, candidate := range candidates {
		current := web.moves.resolve(candidate.Path)
		known[current] = current
	}
	paths := []string{}
	seen := map[string]bool{}
	for _, path := range requested {
		if library, ok := known[web.moves.resolve(path)]; ok && !seen[library] {
			seen[library] = true
			paths = append(paths, library)
		}
	}
	return paths
}
