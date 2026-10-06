package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

// The library history remembers when each game, update and DLC appeared in the folders and
// when it left them, and how big the library was each day. It gives the date a game was
// added, the recent changes and the chart of the Statistics page.

const (
	HISTORY_FILENAME = "history.json"
	maxHistoryEvents = 1000
	maxHistoryDays   = 730
)

// kinds of content in the history
const (
	HISTORY_GAME   = "game"
	HISTORY_UPDATE = "update"
	HISTORY_DLC    = "dlc"
)

// HistoryEvent is a game, update or DLC that appeared in the folders or left them.
type HistoryEvent struct {
	Time    time.Time `json:"time"`
	Added   bool      `json:"added"`
	Kind    string    `json:"kind"`
	Id      string    `json:"id"`
	Name    string    `json:"name,omitempty"`
	Version int       `json:"version,omitempty"`
}

// HistoryDay is the size of the library at the end of a day.
type HistoryDay struct {
	Date    string `json:"date"`
	Games   int    `json:"games"`
	Updates int    `json:"updates"`
	Dlc     int    `json:"dlc"`
	Size    int64  `json:"size"`
}

// historyEntry is a content of the library: when it was found and its name, for the event
// when it leaves.
type historyEntry struct {
	Time time.Time `json:"t"`
	Name string    `json:"n,omitempty"`
}

type historyData struct {
	// by content key (see libraryContents)
	Contents map[string]historyEntry `json:"contents"`
	// newest first
	Events []HistoryEvent `json:"events"`
	Days   []HistoryDay   `json:"days"`
}

type libraryHistory struct {
	mutex sync.Mutex
	path  string
	data  historyData
}

func (web *Web) history() *libraryHistory {
	web.historyOnce.Do(func() {
		history := &libraryHistory{path: filepath.Join(web.dataFolder, HISTORY_FILENAME)}
		history.reload()
		web.hist = history
	})
	return web.hist
}

// reload reads the history again, also after a backup was restored.
func (h *libraryHistory) reload() {
	data := historyData{}
	if bytes, err := os.ReadFile(h.path); err == nil {
		json.Unmarshal(bytes, &data)
	}
	h.mutex.Lock()
	h.data = data
	h.mutex.Unlock()
}

// libraryContent is a game, update or DLC of the library.
type libraryContent struct {
	kind    string
	id      string
	name    string
	version int
	size    int64
}

// libraryContents lists the games, updates and DLC of the library by a key that stays the
// same between scans: "game:ID", "update:ID:VERSION" or "dlc:ID".
func libraryContents(switchDB *db.SwitchTitlesDB, localDB *db.LocalSwitchFilesDB) map[string]libraryContent {
	contents := map[string]libraryContent{}
	for prefix, local := range localDB.TitlesMap {
		var title *db.SwitchTitle
		if switchDB != nil {
			title = switchDB.TitlesMap[prefix]
		}
		gameName := ""
		if local.BaseExist && local.File.Metadata != nil {
			gameName = getLocalTitleName(title, local)
			id := strings.ToUpper(local.File.Metadata.TitleId)
			contents["game:"+id] = libraryContent{kind: HISTORY_GAME, id: id, name: gameName, size: local.File.ExtendedInfo.Size}
		} else if title != nil {
			gameName = title.Attributes.Name
		}
		for version, update := range local.Updates {
			id := strings.ToUpper(prefix + "800")
			if update.Metadata != nil {
				id = strings.ToUpper(update.Metadata.TitleId)
			}
			name := gameName
			if name == "" {
				name = strings.TrimSpace(db.ParseTitleNameFromFileName(update.ExtendedInfo.FileName))
			}
			contents["update:"+id+":"+strconv.Itoa(version)] = libraryContent{kind: HISTORY_UPDATE, id: id, name: name, version: version, size: update.ExtendedInfo.Size}
		}
		for dlcId, dlc := range local.Dlc {
			id := strings.ToUpper(dlcId)
			name := ""
			if title != nil {
				if attributes, ok := title.Dlc[dlcId]; ok {
					name = attributes.Name
				} else if attributes, ok := title.Dlc[strings.ToLower(dlcId)]; ok {
					name = attributes.Name
				}
			}
			if name == "" {
				name = strings.TrimSpace(db.ParseTitleNameFromFileName(dlc.ExtendedInfo.FileName))
			}
			contents["dlc:"+id] = libraryContent{kind: HISTORY_DLC, id: id, name: name, size: dlc.ExtendedInfo.Size}
		}
	}
	return contents
}

// historyKind returns the kind, title ID and version of a content key.
func historyKind(key string) (string, string, int) {
	parts := strings.Split(key, ":")
	if len(parts) < 2 {
		return "", "", 0
	}
	version := 0
	if len(parts) > 2 {
		version, _ = strconv.Atoi(parts[2])
	}
	return parts[0], parts[1], version
}

var historyKindOrder = map[string]int{HISTORY_GAME: 0, HISTORY_DLC: 1, HISTORY_UPDATE: 2}

// record compares the library with the previous scan and remembers the changes. The first
// scan only remembers what is there, so a whole library is not reported as new. It returns
// the changes, and whether anything was remembered for the first time.
func (h *libraryHistory) record(contents map[string]libraryContent, now time.Time) ([]HistoryEvent, bool, error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	first := h.data.Contents == nil
	if first {
		h.data.Contents = map[string]historyEntry{}
	}
	events := []HistoryEvent{}
	for key, content := range contents {
		if _, ok := h.data.Contents[key]; ok {
			continue
		}
		h.data.Contents[key] = historyEntry{Time: now, Name: content.name}
		if !first {
			events = append(events, HistoryEvent{Time: now, Added: true, Kind: content.kind, Id: content.id, Name: content.name, Version: content.version})
		}
	}
	for key, entry := range h.data.Contents {
		if _, ok := contents[key]; ok {
			continue
		}
		delete(h.data.Contents, key)
		kind, id, version := historyKind(key)
		events = append(events, HistoryEvent{Time: now, Kind: kind, Id: id, Name: entry.Name, Version: version})
	}
	sort.Slice(events, func(i, j int) bool {
		a, b := events[i], events[j]
		if a.Added != b.Added {
			return a.Added
		}
		if a.Kind != b.Kind {
			return historyKindOrder[a.Kind] < historyKindOrder[b.Kind]
		}
		if a.Name != b.Name {
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		}
		return a.Version < b.Version
	})
	h.data.Events = append(events, h.data.Events...)
	if len(h.data.Events) > maxHistoryEvents {
		h.data.Events = h.data.Events[:maxHistoryEvents]
	}

	day := HistoryDay{Date: now.Format("2006-01-02")}
	for _, content := range contents {
		switch content.kind {
		case HISTORY_GAME:
			day.Games++
		case HISTORY_UPDATE:
			day.Updates++
		case HISTORY_DLC:
			day.Dlc++
		}
		day.Size += content.size
	}
	daysChanged := len(h.data.Days) == 0 || h.data.Days[len(h.data.Days)-1] != day
	if len(h.data.Days) > 0 && h.data.Days[len(h.data.Days)-1].Date == day.Date {
		h.data.Days[len(h.data.Days)-1] = day
	} else {
		h.data.Days = append(h.data.Days, day)
	}
	if len(h.data.Days) > maxHistoryDays {
		h.data.Days = h.data.Days[len(h.data.Days)-maxHistoryDays:]
	}

	if !first && len(events) == 0 && !daysChanged {
		return events, false, nil
	}
	bytes, err := json.Marshal(h.data)
	if err != nil {
		return events, first, err
	}
	return events, first, os.WriteFile(h.path, bytes, 0644)
}

// added returns when a content was found in the folders, or the zero time.
func (h *libraryHistory) added(key string) time.Time {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	return h.data.Contents[key].Time
}

// addedTimes returns when each content was found, by content key.
func (h *libraryHistory) addedTimes() map[string]time.Time {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	times := make(map[string]time.Time, len(h.data.Contents))
	for key, entry := range h.data.Contents {
		times[key] = entry.Time
	}
	return times
}

func (h *libraryHistory) recent(count int) []HistoryEvent {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	if count > len(h.data.Events) {
		count = len(h.data.Events)
	}
	return append([]HistoryEvent(nil), h.data.Events[:count]...)
}

func (h *libraryHistory) days() []HistoryDay {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	return append([]HistoryDay(nil), h.data.Days...)
}

// recordHistory remembers the changes of the library after a scan.
func (web *Web) recordHistory() {
	switchDB, localDB := web.state.get()
	if localDB == nil || web.state.IsSynchronizing() {
		return
	}
	events, first, err := web.history().record(libraryContents(switchDB, localDB), time.Now())
	if err != nil {
		web.sugarLogger.Warnf("Failed to save the library history: %v", err)
	}
	if first || len(events) > 0 {
		// the lists show the dates the games were added
		web.invalidateDerived()
	}
	if len(events) > 0 {
		web.notifyNewContent(events)
	}
}

// HistoryChart is the number of games over time, drawn as a line.
type HistoryChart struct {
	Points string
	Max    int
	Last   int
	From   string
	To     string
}

const (
	historyChartWidth  = 600
	historyChartHeight = 120
)

// historyChart draws the number of games of each day; it needs two days at least.
func historyChart(days []HistoryDay) *HistoryChart {
	if len(days) < 2 {
		return nil
	}
	chart := &HistoryChart{From: days[0].Date, To: days[len(days)-1].Date, Last: days[len(days)-1].Games}
	for _, day := range days {
		if day.Games > chart.Max {
			chart.Max = day.Games
		}
	}
	scale := chart.Max
	if scale == 0 {
		scale = 1
	}
	points := make([]string, len(days))
	for i, day := range days {
		x := float64(i) * historyChartWidth / float64(len(days)-1)
		y := historyChartHeight - 4 - float64(day.Games)*(historyChartHeight-8)/float64(scale)
		points[i] = fmt.Sprintf("%.1f,%.1f", x, y)
	}
	chart.Points = strings.Join(points, " ")
	return chart
}
