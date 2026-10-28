package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The quarantine: a file the app deletes from the library goes first to a hidden folder of
// the library folder it was in, on the same disk, so it moves at once without a copy. It can
// be put back until it expires, the quarantine is too big, or a task needs its room.
const (
	QUARANTINE_FOLDER   = db.QUARANTINE_FOLDER
	QUARANTINE_FILENAME = "quarantine.json"
)

// how long the files are kept
const (
	QUARANTINE_OFF   = ""
	QUARANTINE_48H   = "48h"
	QUARANTINE_WEEK  = "7d"
	QUARANTINE_MONTH = "30d"
	QUARANTINE_NEVER = "never"
)

var quarantineKeeps = map[string]time.Duration{QUARANTINE_OFF: 0, QUARANTINE_48H: 48 * time.Hour, QUARANTINE_WEEK: 7 * 24 * time.Hour, QUARANTINE_MONTH: 30 * 24 * time.Hour, QUARANTINE_NEVER: 0}

// the sizes the quarantine can hold, in GB; 0 for no limit
var quarantineLimits = []int{0, 50, 100, 250}

// why a file went to the quarantine
const (
	QUARANTINE_DUPLICATE    = "duplicate"
	QUARANTINE_OLD_UPDATE   = "old_update"
	QUARANTINE_REGION       = "region"
	QUARANTINE_COMPRESSED   = "compressed"
	QUARANTINE_CONVERTED    = "converted"
	QUARANTINE_DECOMPRESSED = "decompressed"
	QUARANTINE_SPLIT        = "split"
	QUARANTINE_PACK         = "pack"
	QUARANTINE_DELETED      = "deleted"
)

// what the Quarantine section says of each reason
var quarantineReasons = map[string]string{
	QUARANTINE_DUPLICATE:    "Duplicate",
	QUARANTINE_OLD_UPDATE:   "Old update",
	QUARANTINE_REGION:       "Copy of another region",
	QUARANTINE_COMPRESSED:   "Original of a compressed file",
	QUARANTINE_CONVERTED:    "Converted to NSP",
	QUARANTINE_DECOMPRESSED: "Decompressed to NSP",
	QUARANTINE_SPLIT:        "Split into parts",
	QUARANTINE_PACK:         "Joined in a pack",
	QUARANTINE_DELETED:      "Deleted from the library",
}

var errQuarantineTaken = errors.New("Another file has its name in its folder now.")

// QuarantineItem is a file in the quarantine.
type QuarantineItem struct {
	Id string `json:"id"`
	// where it was, and where it waits
	Path   string    `json:"path"`
	Stored string    `json:"stored"`
	Root   string    `json:"root"`
	Size   int64     `json:"size"`
	Reason string    `json:"reason"`
	Time   time.Time `json:"time"`
}

type quarantineStore struct {
	mutex sync.Mutex
	path  string
	items []QuarantineItem
}

func (web *Web) quarantine() *quarantineStore {
	web.quarantineOnce.Do(func() {
		if web.quarantineItems == nil {
			web.quarantineItems = loadQuarantine(web.dataFolder)
		}
	})
	return web.quarantineItems
}

func loadQuarantine(dataFolder string) *quarantineStore {
	store := &quarantineStore{path: filepath.Join(dataFolder, QUARANTINE_FILENAME)}
	if data, err := os.ReadFile(store.path); err == nil {
		parseStore(store.path, data, &store.items)
	}
	return store
}

// save writes the list; the mutex must be held.
func (q *quarantineStore) save() {
	if len(q.items) == 0 {
		os.Remove(q.path)
		return
	}
	if data, err := json.Marshal(q.items); err == nil {
		writeFileAtomic(q.path, data)
	}
}

// list returns the files, the newest first.
func (q *quarantineStore) list() []QuarantineItem {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	items := append([]QuarantineItem{}, q.items...)
	sort.SliceStable(items, func(i, j int) bool { return items[i].Time.After(items[j].Time) })
	return items
}

// take removes a file from the list; the mutex must be held.
func (q *quarantineStore) take(id string) (QuarantineItem, bool) {
	for i, item := range q.items {
		if item.Id == id {
			q.items = append(q.items[:i], q.items[i+1:]...)
			return item, true
		}
	}
	return QuarantineItem{}, false
}

// erase deletes a file of the quarantine from the disk, with the folder of its own it waited in.
func (item QuarantineItem) erase() error {
	err := os.RemoveAll(filepath.Dir(item.Stored))
	// the quarantine folder goes too once it is empty
	os.Remove(filepath.Dir(filepath.Dir(item.Stored)))
	return err
}

// quarantineOn tells whether deleted files go to the quarantine.
func quarantineOn(options settings.QuarantineOptions) bool {
	return options.Keep != QUARANTINE_OFF
}

// quarantineRoot is the library folder a path is in, where its quarantine is; the folder of
// the file when it is in none.
func quarantineRoot(path string, folders []string) string {
	root := ""
	for _, folder := range folders {
		if insideFolder(path, folder) && len(folder) > len(root) {
			root = filepath.Clean(folder)
		}
	}
	if root == "" {
		return filepath.Dir(path)
	}
	return root
}

func insideFolder(path string, folder string) bool {
	rel, err := filepath.Rel(folder, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// quarantineReason tells why the library deletes a file from what it gives as its reason: the
// group of the Space page, or the text of the scan.
func quarantineReason(reason string) string {
	lower := strings.ToLower(reason)
	switch {
	case reason == SPACE_OLD_UPDATES || strings.Contains(lower, "old update"):
		return QUARANTINE_OLD_UPDATE
	case reason == SPACE_DUPLICATES || strings.Contains(lower, "duplicate"):
		return QUARANTINE_DUPLICATE
	case reason == SPACE_COMPRESSED:
		return QUARANTINE_COMPRESSED
	case reason == "other region":
		return QUARANTINE_REGION
	}
	return QUARANTINE_DELETED
}

// discard takes a file the library no longer wants off its folder: to the quarantine when it
// is on, deleted otherwise.
func (web *Web) discard(path string, reason string) error {
	appSettings := settings.ReadSettings(web.dataFolder)
	if !quarantineOn(appSettings.Quarantine) {
		return os.Remove(path)
	}
	return web.putInQuarantine(appSettings, path, quarantineReason(reason))
}

// discardOriginal deletes the original of a file just written and checked (compressed,
// converted, split...): to the quarantine only when it keeps originals too.
func (web *Web) discardOriginal(path string, reason string) error {
	appSettings := settings.ReadSettings(web.dataFolder)
	if !quarantineOn(appSettings.Quarantine) || !appSettings.Quarantine.Originals {
		return os.Remove(path)
	}
	return web.putInQuarantine(appSettings, path, reason)
}

func (web *Web) putInQuarantine(appSettings *settings.AppSettings, path string, reason string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	root := quarantineRoot(path, scanFolders(appSettings))
	now := time.Now()
	q := web.quarantine()
	q.mutex.Lock()
	id := fmt.Sprintf("%x", now.UnixNano())
	for _, item := range q.items {
		if item.Id == id {
			id = fmt.Sprintf("%x", now.UnixNano()+1)
		}
	}
	folder := filepath.Join(root, QUARANTINE_FOLDER, id)
	stored := filepath.Join(folder, filepath.Base(path))
	err = os.MkdirAll(folder, 0o755)
	if err == nil {
		err = os.Rename(path, stored)
	}
	if err != nil {
		q.mutex.Unlock()
		os.Remove(folder)
		// another disk, or a folder that cannot be written: it is deleted, as asked
		web.sugarLogger.Warnf("[Quarantine] %s could not be kept, it is deleted: %v", path, err)
		return os.Remove(path)
	}
	syncFolder(folder)
	q.items = append(q.items, QuarantineItem{Id: id, Path: path, Stored: stored, Root: root, Size: info.Size(), Reason: reason, Time: now})
	q.trim(appSettings.Quarantine.LimitGB)
	q.save()
	q.mutex.Unlock()
	web.sugarLogger.Infof("[Quarantine] %s kept (%s)", path, reason)
	return nil
}

// trim deletes the oldest files over the size the quarantine can hold; the mutex must be held.
func (q *quarantineStore) trim(limitGB int) {
	if limitGB <= 0 {
		return
	}
	limit := int64(limitGB) << 30
	var total int64
	for _, item := range q.items {
		total += item.Size
	}
	sort.SliceStable(q.items, func(i, j int) bool { return q.items[i].Time.Before(q.items[j].Time) })
	for total > limit && len(q.items) > 0 {
		oldest := q.items[0]
		oldest.erase()
		q.items = q.items[1:]
		total -= oldest.Size
	}
}

// expire deletes the files kept longer than asked, and forgets those no longer there.
func (q *quarantineStore) expire(options settings.QuarantineOptions, now time.Time) int {
	keep := quarantineKeeps[options.Keep]
	q.mutex.Lock()
	defer q.mutex.Unlock()
	kept := q.items[:0]
	gone := 0
	for _, item := range q.items {
		if keep > 0 && now.Sub(item.Time) >= keep {
			item.erase()
			gone++
			continue
		}
		if _, err := os.Stat(item.Stored); err != nil {
			gone++
			continue
		}
		kept = append(kept, item)
	}
	q.items = kept
	if gone > 0 {
		q.save()
	}
	return gone
}

// restore puts a file back where it was.
func (q *quarantineStore) restore(id string) (QuarantineItem, error) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	for _, item := range q.items {
		if item.Id != id {
			continue
		}
		if _, err := os.Lstat(item.Path); err == nil {
			return item, errQuarantineTaken
		}
		if err := os.MkdirAll(filepath.Dir(item.Path), 0o755); err != nil {
			return item, err
		}
		if err := os.Rename(item.Stored, item.Path); err != nil {
			return item, err
		}
		syncFolder(filepath.Dir(item.Path))
		item.erase()
		q.take(id)
		q.save()
		return item, nil
	}
	return QuarantineItem{}, os.ErrNotExist
}

// remove deletes one file of the quarantine for good.
func (q *quarantineStore) remove(id string) error {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	item, ok := q.take(id)
	if !ok {
		return os.ErrNotExist
	}
	q.save()
	return item.erase()
}

// empty deletes every file of the quarantine for good, and returns how many and their size.
func (q *quarantineStore) empty() (int, int64) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	var size int64
	count := 0
	for _, item := range q.items {
		if item.erase() == nil {
			count++
			size += item.Size
		}
	}
	q.items = nil
	q.save()
	return count, size
}

// makeRoom deletes the oldest files kept on the disk of folder until it has need bytes free,
// when the quarantine may; it tells whether it freed any.
func (web *Web) makeRoom(folder string, need int64) bool {
	appSettings := settings.ReadSettings(web.dataFolder)
	if !appSettings.Quarantine.MakeRoom {
		return false
	}
	root := quarantineRoot(filepath.Join(folder, "x"), scanFolders(appSettings))
	q := web.quarantine()
	q.mutex.Lock()
	defer q.mutex.Unlock()
	sort.SliceStable(q.items, func(i, j int) bool { return q.items[i].Time.Before(q.items[j].Time) })
	freed := false
	for i := 0; i < len(q.items); {
		if free, err := diskFree(folder); err != nil || int64(free) >= need {
			break
		}
		item := q.items[i]
		if item.Root != root {
			i++
			continue
		}
		item.erase()
		q.items = append(q.items[:i], q.items[i+1:]...)
		freed = true
		web.sugarLogger.Infof("[Quarantine] %s deleted to make room", item.Path)
	}
	if freed {
		q.save()
	}
	return freed
}

// quarantineSummary is what the Quarantine section shows.
type quarantineSummary struct {
	Items []quarantineRow
	Size  int64
	// the share of the size it can hold, in percent; 0 without a limit
	Percent int
	// when the oldest file goes, if they expire
	NextExpiry time.Time
}

// quarantineRow is a file of the list: why it went (to translate), and when it goes for good.
type quarantineRow struct {
	QuarantineItem
	Label   string
	Folder  string
	Expires time.Time
}

func (web *Web) quarantineSummary() quarantineSummary {
	appSettings := settings.ReadSettings(web.dataFolder)
	summary := quarantineSummary{}
	keep := quarantineKeeps[appSettings.Quarantine.Keep]
	for _, item := range web.quarantine().list() {
		row := quarantineRow{QuarantineItem: item, Label: quarantineReasons[item.Reason]}
		// the folder it was in, inside its library folder ("" at the top of it)
		if folder, err := filepath.Rel(item.Root, filepath.Dir(item.Path)); err == nil && folder != "." {
			row.Folder = folder
		}
		if row.Label == "" {
			row.Label = quarantineReasons[QUARANTINE_DELETED]
		}
		if keep > 0 {
			row.Expires = item.Time.Add(keep)
			if summary.NextExpiry.IsZero() || row.Expires.Before(summary.NextExpiry) {
				summary.NextExpiry = row.Expires
			}
		}
		summary.Items = append(summary.Items, row)
		summary.Size += item.Size
	}
	if limit := int64(appSettings.Quarantine.LimitGB) << 30; limit > 0 {
		summary.Percent = int(min(100, summary.Size*100/limit))
	}
	return summary
}

func (web *Web) HandleQuarantine() {
	web.router.HandleFunc("/quarantine/restore", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		item, err := web.quarantine().restore(r.FormValue("id"))
		switch {
		case errors.Is(err, os.ErrNotExist):
			writeGlobalError(w, http.StatusNotFound, lang, "That file is no longer in the quarantine.")
			return
		case errors.Is(err, errQuarantineTaken):
			writeGlobalError(w, http.StatusConflict, lang, errQuarantineTaken.Error())
			return
		case err != nil:
			web.sugarLogger.Errorf("[Quarantine] %s could not be put back: %v", item.Path, err)
			writeGlobalError(w, http.StatusInternalServerError, lang, "The file could not be put back.")
			return
		}
		web.sugarLogger.Infof("[Quarantine] %s put back", item.Path)
		// the library has the file again
		web.Rescan(TRIGGER_ORGANIZE)
		writeJSON(w, http.StatusOK, map[string]string{"message": translatef(lang, "%v is back in its folder.", filepath.Base(item.Path))})
	}).Methods("POST")

	web.router.HandleFunc("/quarantine/delete", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		if err := web.quarantine().remove(r.FormValue("id")); err != nil && !errors.Is(err, os.ErrNotExist) {
			web.sugarLogger.Errorf("[Quarantine] a file could not be deleted: %v", err)
			writeGlobalError(w, http.StatusInternalServerError, lang, "The file could not be deleted.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"message": translate(lang, "Deleted for good.")})
	}).Methods("POST")

	web.router.HandleFunc("/quarantine/empty", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		count, size := web.quarantine().empty()
		web.sugarLogger.Infof("[Quarantine] emptied: %d files, %d bytes", count, size)
		writeJSON(w, http.StatusOK, map[string]string{"message": translatef(lang, "%v files deleted for good (%v).", count, formatSize(size))})
	}).Methods("POST")
}
