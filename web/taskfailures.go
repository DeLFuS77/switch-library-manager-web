package web

import (
	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/internal/notify"
	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// A task that fails (a compression, a check, a synchronization...) is told by the channels of
// the notifications, so it is known without looking at Tasks. A task cancelled by the user is
// not a failure to tell.

// taskKindNames are the names of the kinds of tasks, translated by the interface (the same as
// the Tasks page).
var taskKindNames = map[string]string{
	tasklog.TASK_SYNC: "Synchronization", tasklog.TASK_SCAN: "Library scan", tasklog.TASK_ORGANIZE: "Organize files", tasklog.TASK_CLEANUP: "Delete old updates",
	tasklog.TASK_COMPRESS: "Compression", tasklog.TASK_DECOMPRESS: "Decompression", tasklog.TASK_VERIFY: "File check", tasklog.TASK_COVERS: "Cover downloads",
	tasklog.TASK_THUMBNAILS: "Thumbnails", tasklog.TASK_COPY: "Copy to the card", tasklog.TASK_CONVERT: "Conversion", tasklog.TASK_PACK: "Combine",
	tasklog.TASK_SPLIT: "Split files", TASK_TIMETOBEAT: "Times to beat (IGDB)", TASK_UPLOAD: "Upload",
}

func (web *Web) notifyTaskFailed(task tasklog.Task) {
	if isDemoMode() || task.Error == nil || tasklog.NotWorthTelling[task.Error.Text] {
		return
	}
	settingsObj := settings.ReadSettings(web.dataFolder)
	options := settingsObj.Notifications
	if !notify.Configured(options) || !options.NotifyTaskFailures {
		return
	}
	lang := settingsObj.Language
	if !i18n.IsSupportedLanguage(lang) {
		lang = i18n.DEFAULT_LANGUAGE
	}
	kind := task.Kind
	if name, ok := taskKindNames[kind]; ok {
		kind = i18n.Translate(lang, name)
	}
	reason := i18n.Translate(lang, task.Error.Text)
	if task.Error.Detail != "" {
		reason += "\n" + task.Error.Detail
	}
	title := i18n.Translate(lang, "A task failed")
	text := i18n.Translatef(lang, "%v failed: %v", kind, reason)
	item := map[string]any{"kind": task.Kind, "id": task.Id, "reason": task.Error.Text, "detail": task.Error.Detail}
	if err := notify.Send(options, title, text, []any{item}); err != nil {
		web.sugarLogger.Warnf("Failed to send the notification of a failed task: %v", err)
	}
}
