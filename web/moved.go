package web

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"time"

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
	// the new path of a file, and when it moved, by its old one
	to map[string]movedFile
}

type movedFile struct {
	path string
	at   time.Time
}

// record keeps the moves of Organize that went through. A file moved to a path is another file
// than the one that was there before: what that one's move said is forgotten.
func (m *movedFiles) record(operations []process.Operation) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	now := time.Now()
	for _, op := range operations {
		if op.Error != "" || op.Kind != process.OP_MOVE {
			continue
		}
		if m.to == nil || len(m.to) >= movedFilesLimit {
			m.to = map[string]movedFile{}
		}
		delete(m.to, op.To)
		if op.From != op.To {
			m.to[op.From] = movedFile{op.To, now}
		}
	}
}

// replaced keeps a file the app replaced with the one it made of it (an NSP deleted once its
// NSZ was checked): a task asked for the original before works on what holds it now.
func (m *movedFiles) replaced(original string, made string) {
	m.record([]process.Operation{{Kind: process.OP_MOVE, From: original, To: made}})
}

// forget forgets the moves made before a scan started: the library has the new paths, and an
// old path may hold another file by now.
func (m *movedFiles) forget(before time.Time) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	for from, moved := range m.to {
		if moved.at.Before(before) {
			delete(m.to, from)
		}
	}
}

// resolve is where the file at path is now, as far as the app moved it. A file at the old path
// is the file of that path (a new file under the name of one moved), unless it is the very file
// moved (a name changed in its case only).
func (m *movedFiles) resolve(path string) string {
	for hop := 0; hop < movedFilesHops; hop++ {
		m.mutex.Lock()
		next, ok := m.to[path]
		m.mutex.Unlock()
		if !ok || next.path == path {
			break
		}
		if here, err := os.Stat(path); err == nil {
			if there, err := os.Stat(next.path); err != nil || !os.SameFile(here, there) {
				break
			}
		}
		path = next.path
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
