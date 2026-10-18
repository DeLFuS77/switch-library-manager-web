package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// COMPRESSION_RATIOS_FILENAME holds what the compressions saved, by kind of content and format
// of file, so the Compress page can tell how much a file will likely save before it is
// compressed. It is kept apart from the history of the tasks, which can be cleared.
const COMPRESSION_RATIOS_FILENAME = "compression_ratios.json"

// minRatioFiles is how many files of a kind must have been compressed before an estimate is
// shown: fewer say little.
const minRatioFiles = 3

type ratioStore struct {
	mutex sync.Mutex
	path  string
	kinds map[string]ratioTotal
}

type ratioTotal struct {
	Before int64 `json:"before"`
	After  int64 `json:"after"`
	Files  int   `json:"files"`
}

func (t ratioTotal) percent() int {
	if t.Before <= 0 {
		return 0
	}
	return int((t.Before - t.After) * 100 / t.Before)
}

var titleIdInName = regexp.MustCompile(`\[([0-9A-Fa-f]{16})\]`)

// contentKind tells from the title ID in the name of a file whether it is a game, an update or
// a DLC, as the Compress page names them; "" when the name has none.
func contentKind(path string) string {
	match := titleIdInName.FindStringSubmatch(filepath.Base(path))
	if match == nil {
		return ""
	}
	id := strings.ToLower(match[1])
	switch {
	case strings.HasSuffix(id, "000"):
		return "Base"
	case strings.HasSuffix(id, "800"):
		return "Update"
	}
	return "DLC"
}

// ratioKeys are the totals a file counts in: its kind with its format, and its kind alone.
func ratioKeys(kind string, path string) []string {
	format := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	return []string{kind + "/" + format, kind}
}

func (web *Web) ratios() *ratioStore {
	web.ratiosOnce.Do(func() {
		store := &ratioStore{path: filepath.Join(web.dataFolder, COMPRESSION_RATIOS_FILENAME), kinds: map[string]ratioTotal{}}
		if data, err := os.ReadFile(store.path); err == nil {
			parseStore(store.path, data, &store.kinds)
		}
		if store.kinds == nil {
			store.kinds = map[string]ratioTotal{}
		}
		web.ratiosStore = store
	})
	return web.ratiosStore
}

// add counts a file compressed, with its size before and after.
func (s *ratioStore) add(kind string, path string, before int64, after int64) {
	if kind == "" || before <= 0 || after <= 0 || after > before {
		return
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for _, key := range ratioKeys(kind, path) {
		total := s.kinds[key]
		total.Before += before
		total.After += after
		total.Files++
		s.kinds[key] = total
	}
	if data, err := json.Marshal(s.kinds); err == nil {
		writeFileAtomic(s.path, data)
	}
}

// estimate is the share a file of this kind and format will likely save, from the files
// compressed before: of the same format when there are enough, else of the same kind.
func (s *ratioStore) estimate(kind string, path string) (int, bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for _, key := range ratioKeys(kind, path) {
		if total, ok := s.kinds[key]; ok && total.Files >= minRatioFiles {
			return total.percent(), true
		}
	}
	return 0, false
}

// RatioSummary is what a kind of content saved on average, for the Compress page.
type RatioSummary struct {
	Kind    string
	Percent int
	Files   int
}

func (s *ratioStore) summary() []RatioSummary {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	summary := []RatioSummary{}
	for _, kind := range []string{"Base", "Update", "DLC"} {
		if total, ok := s.kinds[kind]; ok && total.Files >= minRatioFiles {
			summary = append(summary, RatioSummary{Kind: kind, Percent: total.percent(), Files: total.Files})
		}
	}
	return summary
}

// EstimatePercent is the share a candidate will likely save, as "17 %", or "" when there is no
// estimate yet.
func (c CompressCandidate) EstimatePercent() string {
	if !c.HasEstimate {
		return ""
	}
	return fmt.Sprintf("%d %%", c.Estimate)
}

// EstimateBytes is what a candidate will likely save, or 0 when there is no estimate yet.
func (c CompressCandidate) EstimateBytes() int64 {
	if !c.HasEstimate {
		return 0
	}
	return c.Size * int64(c.Estimate) / 100
}
