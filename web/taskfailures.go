package web

import (
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// A task that fails (a compression, a check, a synchronization...) is told by the channels of
// the notifications, so it is known without looking at Tasks. A task cancelled by the user is
// not a failure to tell.

// taskKindNames are the names of the kinds of tasks, translated by the interface (the same as
// the Tasks page).
var taskKindNames = map[string]string{
	TASK_SYNC: "Synchronization", TASK_SCAN: "Library scan", TASK_ORGANIZE: "Organize files", TASK_CLEANUP: "Delete old updates",
	TASK_COMPRESS: "Compression", TASK_DECOMPRESS: "Decompression", TASK_VERIFY: "File check", TASK_COVERS: "Cover downloads",
	TASK_THUMBNAILS: "Thumbnails", TASK_COPY: "Copy to the card", TASK_CONVERT: "Conversion", TASK_PACK: "Complete pack",
	TASK_SPLIT: "Split files", TASK_TIMETOBEAT: "Times to beat (IGDB)", TASK_UPLOAD: "Upload",
}

// notWorthTelling are the ends that are not failures to tell: what the user stopped.
var notWorthTelling = map[string]bool{NOTE_COMPRESS_CANCELED: true, NOTE_COMPRESS_CANCELED_OLD: true, NOTE_UPLOAD_CANCELED: true, NOTE_UPLOAD_STOPPED: true}

func (web *Web) notifyTaskFailed(task Task) {
	if isDemoMode() || task.Error == nil || notWorthTelling[task.Error.Text] {
		return
	}
	settingsObj := settings.ReadSettings(web.dataFolder)
	options := settingsObj.Notifications
	if !notificationsConfigured(options) || !options.NotifyTaskFailures {
		return
	}
	lang := settingsObj.Language
	if !isSupportedLanguage(lang) {
		lang = DEFAULT_LANGUAGE
	}
	kind := task.Kind
	if name, ok := taskKindNames[kind]; ok {
		kind = translate(lang, name)
	}
	reason := translate(lang, task.Error.Text)
	if task.Error.Detail != "" {
		reason += "\n" + task.Error.Detail
	}
	title := translate(lang, "A task failed")
	text := translatef(lang, "%v failed: %v", kind, reason)
	item := map[string]any{"kind": task.Kind, "id": task.Id, "reason": task.Error.Text, "detail": task.Error.Detail}
	if err := sendMessage(options, title, text, []any{item}); err != nil {
		web.sugarLogger.Warnf("Failed to send the notification of a failed task: %v", err)
	}
}
