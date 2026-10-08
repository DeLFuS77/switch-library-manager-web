package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// Automations look after the files that appear in the library, when the user turns them on
// in Settings (all off by default). The new files wait in a queue, kept in a file so a restart
// does not lose them, and then go through the chosen steps, one after the other: check them,
// compress them, delete the updates they replace, organize the library and send a message with
// what was done. Each step is a task of its own on the Tasks page.

const (
	AUTOMATION_FILENAME = "automation.json"
	// how often the queue looks whether it can go on (no scan or file task running, within
	// the background hours if asked)
	automationPoll = 5 * time.Second
)

// automationQueue holds the files that appeared and wait for the automation.
type automationQueue struct {
	mutex sync.Mutex
	path  string
	// paths of the new files, and the names of the new games and DLC for the message
	Paths   []string `json:"paths"`
	Names   []string `json:"names"`
	Updates int      `json:"updates"`
	running bool
}

func (web *Web) automation() *automationQueue {
	web.automationOnce.Do(func() {
		queue := &automationQueue{path: filepath.Join(web.dataFolder, AUTOMATION_FILENAME)}
		if data, err := os.ReadFile(queue.path); err == nil {
			json.Unmarshal(data, queue)
		}
		web.automationQueue = queue
	})
	return web.automationQueue
}

// save writes the queue; the mutex must be held.
func (q *automationQueue) save() {
	if len(q.Paths) == 0 && len(q.Names) == 0 {
		os.Remove(q.path)
		return
	}
	if data, err := json.Marshal(q); err == nil {
		writeFileAtomic(q.path, data)
	}
}

// libraryContentPaths are the files of the library by content key (see libraryContents).
func libraryContentPaths(localDB *db.LocalSwitchFilesDB) map[string]string {
	paths := map[string]string{}
	if localDB == nil {
		return paths
	}
	path := func(info db.ExtendedFileInfo) string { return filepath.Join(info.BaseFolder, info.FileName) }
	for prefix, local := range localDB.TitlesMap {
		if local.BaseExist && local.File.Metadata != nil {
			paths["game:"+strings.ToUpper(local.File.Metadata.TitleId)] = path(local.File.ExtendedInfo)
		}
		for version, update := range local.Updates {
			id := strings.ToUpper(prefix + "800")
			if update.Metadata != nil {
				id = strings.ToUpper(update.Metadata.TitleId)
			}
			paths["update:"+id+":"+strconv.Itoa(version)] = path(update.ExtendedInfo)
		}
		for dlcId, dlc := range local.Dlc {
			paths["dlc:"+strings.ToUpper(dlcId)] = path(dlc.ExtendedInfo)
		}
	}
	return paths
}

// queueAutomation adds the contents that appeared in a scan to the queue, and starts it.
func (web *Web) queueAutomation(events []HistoryEvent) {
	options := settings.ReadSettings(web.dataFolder).Automation
	if !options.Any() || isDemoMode() {
		return
	}
	_, localDB := web.state.get()
	paths := libraryContentPaths(localDB)
	queue := web.automation()
	queue.mutex.Lock()
	known := map[string]struct{}{}
	for _, path := range queue.Paths {
		known[path] = struct{}{}
	}
	added := 0
	for _, event := range events {
		if !event.Added {
			continue
		}
		key := event.Kind + ":" + event.Id
		if event.Kind == HISTORY_UPDATE {
			key += ":" + strconv.Itoa(event.Version)
			queue.Updates++
		} else if event.Name != "" {
			queue.Names = append(queue.Names, event.Name)
		}
		if path, ok := paths[key]; ok {
			if _, seen := known[path]; !seen {
				known[path] = struct{}{}
				queue.Paths = append(queue.Paths, path)
				added++
			}
		}
	}
	queue.save()
	queue.mutex.Unlock()
	if added > 0 {
		web.startAutomation()
	}
}

// automationPending is the number of files waiting for the automations.
func (web *Web) automationPending() int {
	queue := web.automation()
	queue.mutex.Lock()
	defer queue.mutex.Unlock()
	return len(queue.Paths)
}

// resumeAutomation goes on with the files left in the queue when the app stopped.
func (web *Web) resumeAutomation() {
	if !settings.ReadSettings(web.dataFolder).Automation.Any() || isDemoMode() {
		return
	}
	queue := web.automation()
	queue.mutex.Lock()
	pending := len(queue.Paths) > 0
	queue.mutex.Unlock()
	if pending {
		web.startAutomation()
	}
}

// startAutomation runs the queue in the background, once at a time.
func (web *Web) startAutomation() {
	queue := web.automation()
	queue.mutex.Lock()
	if queue.running {
		queue.mutex.Unlock()
		return
	}
	queue.running = true
	queue.mutex.Unlock()
	go func() {
		defer func() {
			queue.mutex.Lock()
			queue.running = false
			queue.mutex.Unlock()
		}()
		for web.runAutomationOnce() {
		}
	}()
}

// automationIdle reports whether a step can start: no scan and no file task running, and
// within the background hours when asked.
func (web *Web) automationIdle(options settings.AutomationOptions) bool {
	return !web.state.IsSynchronizing() && !web.compressionRunning() && (!options.BackgroundHoursOnly || web.backgroundAllowed())
}

// waitForAutomation waits until a step can start. It gives up when the automation is turned off.
func (web *Web) waitForAutomation() (settings.AutomationOptions, bool) {
	for {
		options := settings.ReadSettings(web.dataFolder).Automation
		if !options.Any() {
			return options, false
		}
		if web.automationIdle(options) {
			// a scan or a task may start right after another one ends
			time.Sleep(web.automationSettle())
			if web.automationIdle(options) {
				return options, true
			}
		}
		time.Sleep(web.automationPollInterval())
	}
}

// the waits of the automation, shorter in the tests (read by the queue while it runs)
var automationPollOverride atomic.Int64

func (web *Web) automationPollInterval() time.Duration {
	if override := time.Duration(automationPollOverride.Load()); override > 0 {
		return override
	}
	return automationPoll
}

func (web *Web) automationSettle() time.Duration {
	if override := time.Duration(automationPollOverride.Load()); override > 0 {
		return override
	}
	return time.Second
}

// AutomationResult is what one run of the automation did, for the message.
type AutomationResult struct {
	Games      []string
	Updates    int
	Checked    int
	Damaged    int
	Compressed int
	Saved      int64
	Removed    int
	Moved      int
}

// runAutomationOnce takes the files of the queue through the steps. It returns whether files
// were waiting, so the caller runs it again for the ones that appeared meanwhile.
func (web *Web) runAutomationOnce() bool {
	options, ok := web.waitForAutomation()
	if !ok {
		return false
	}
	queue := web.automation()
	queue.mutex.Lock()
	paths, names, updates := queue.Paths, queue.Names, queue.Updates
	queue.Paths, queue.Names, queue.Updates = nil, nil, 0
	queue.save()
	queue.mutex.Unlock()
	if len(paths) == 0 && len(names) == 0 {
		return false
	}
	existing := []string{}
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			existing = append(existing, path)
		}
	}
	sort.Strings(existing)
	result := AutomationResult{Games: names, Updates: updates}

	// 1. check the new files
	damaged := map[string]bool{}
	if options.Verify && len(existing) > 0 {
		if web.startVerificationOf(existing, verifyRecheckChanged, TRIGGER_AUTOMATION) {
			web.waitForFileTask()
		}
		store := web.verifications()
		for _, path := range existing {
			if info, err := os.Stat(path); err == nil {
				if record, ok := store.current(path, info); ok {
					result.Checked++
					if !record.OK {
						result.Damaged++
						damaged[path] = true
					}
				}
			}
		}
	}

	// 2. compress them, except the damaged ones; the originals go unless kept
	if options.Compress && len(existing) > 0 {
		if keys, _ := settings.SwitchKeys(); keys != nil && keys.GetKey("header_key") != "" {
			candidates := map[string]struct{}{}
			for _, candidate := range web.compressCandidates() {
				candidates[candidate.Path] = struct{}{}
			}
			toCompress := []string{}
			for _, path := range existing {
				if _, ok := candidates[path]; ok && !damaged[path] {
					toCompress = append(toCompress, path)
				}
			}
			if len(toCompress) > 0 {
				if _, ok := web.waitForAutomation(); !ok {
					return false
				}
				before := web.latestTaskId(TASK_COMPRESS)
				if web.compressPaths(toCompress, TRIGGER_AUTOMATION) {
					web.waitForFileTask()
					if task, ok := web.latestTask(TASK_COMPRESS); ok && task.Id != before {
						result.Compressed, result.Saved = task.Files, task.Saved
					}
				}
			}
		}
	}

	// 3. delete the updates the new ones replace, and 4. organize; each one rescans after
	steps := []string{}
	if options.CleanupUpdates && updates > 0 {
		steps = append(steps, ORGANIZE_ACTION_CLEANUP)
	}
	if options.Organize {
		steps = append(steps, ORGANIZE_ACTION_ORGANIZE)
	}
	for _, action := range steps {
		if _, ok := web.waitForAutomation(); !ok {
			return false
		}
		if !web.state.startSync() {
			// a scan started meanwhile: wait for it
			if _, ok := web.waitForAutomation(); !ok || !web.state.startSync() {
				continue
			}
		}
		operations, err := web.runOrganizeAction(action, TRIGGER_AUTOMATION)
		web.state.endSync()
		if err != nil {
			web.sugarLogger.Warnf("Automation: %s failed: %v", action, err)
			continue
		}
		if action == ORGANIZE_ACTION_CLEANUP {
			result.Removed = len(operations)
		} else {
			result.Moved = len(operations)
		}
		if len(operations) > 0 {
			web.Rescan(TRIGGER_AUTOMATION)
		}
	}

	// 5. a message with what was done
	if options.Notify {
		web.notifyAutomation(result)
	}
	web.sugarLogger.Infof("Automation: %d new games and DLC, %d updates, %d files checked (%d damaged), %d compressed, %d old updates deleted, %d files organized",
		len(result.Games), result.Updates, result.Checked, result.Damaged, result.Compressed, result.Removed, result.Moved)
	return true
}

// waitForFileTask waits for the verification or compression that just started.
func (web *Web) waitForFileTask() {
	for web.compressionRunning() {
		time.Sleep(web.automationPollInterval() / 5)
	}
}

// latestTask is the newest task of a kind.
func (web *Web) latestTask(kind string) (Task, bool) {
	var latest Task
	found := false
	for _, task := range web.taskLog().Snapshot() {
		if task.Kind == kind && (!found || task.Id > latest.Id) {
			latest, found = task, true
		}
	}
	return latest, found
}

func (web *Web) latestTaskId(kind string) int64 {
	task, _ := web.latestTask(kind)
	return task.Id
}

// automationMessage is the text of the message: what came, and what was done with it.
func automationMessage(lang string, result AutomationResult) (string, string) {
	title := translate(lang, "Switch Library Manager: new files taken care of")
	lines := []string{}
	for i, name := range result.Games {
		if i == maxNotificationLines {
			lines = append(lines, translatef(lang, "and %v more", len(result.Games)-maxNotificationLines))
			break
		}
		lines = append(lines, "• "+translatef(lang, "New in your library: %v", name))
	}
	if result.Updates > 0 {
		lines = append(lines, "• "+translatef(lang, "%v new updates", result.Updates))
	}
	if result.Checked > 0 {
		lines = append(lines, "✓ "+translatef(lang, "%v files checked, %v damaged", result.Checked, result.Damaged))
	}
	if result.Compressed > 0 {
		lines = append(lines, "✓ "+translatef(lang, "%v files compressed, %v saved", result.Compressed, formatSize(result.Saved)))
	}
	if result.Removed > 0 {
		lines = append(lines, "✓ "+translatef(lang, "%v old updates deleted", result.Removed))
	}
	if result.Moved > 0 {
		lines = append(lines, "✓ "+translatef(lang, "%v files organized", result.Moved))
	}
	return title, strings.Join(lines, "\n")
}

func (web *Web) notifyAutomation(result AutomationResult) {
	settingsObj := settings.ReadSettings(web.dataFolder)
	if !notificationsConfigured(settingsObj.Notifications) {
		return
	}
	lang := settingsObj.Language
	if !isSupportedLanguage(lang) {
		lang = DEFAULT_LANGUAGE
	}
	title, text := automationMessage(lang, result)
	if err := sendMessage(settingsObj.Notifications, title, text, result); err != nil {
		web.sugarLogger.Warnf("Failed to send the automation message: %v", err)
	}
}
