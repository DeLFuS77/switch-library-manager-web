package web

import (
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/process"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// Organize tells in its task each file it could not change and each one it left as it is,
// with why, in the language of the app, and counts only the files changed.
func TestOrganizeTellsTheFilesLeft(t *testing.T) {
	web := newTestWeb(t)
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) {
		s.Language = "es"
		s.Folder = "/games"
	})
	taskId := web.taskLog().Start(TASK_ORGANIZE, TRIGGER_MANUAL)
	operations := []process.Operation{
		{Kind: process.OP_MOVE, From: "/games/a.nsp", To: "/games/A/a.nsp"},
		{Kind: process.OP_MOVE, From: "/games/b.nsp", To: "/games/B/b.nsp", Error: "another file is moved to the same destination"},
		{Kind: process.OP_SKIP, From: "/games/orphan.nsp", Reason: process.REASON_NO_BASE},
		{Kind: process.OP_CLEANUP, From: "/games", Reason: "empty folders will be deleted"},
	}
	if changed := web.noteOrganized(taskId, operations); changed != 1 {
		t.Fatalf("changed: %d", changed)
	}
	var task Task
	for _, each := range web.taskLog().Snapshot() {
		if each.Id == taskId {
			task = each
		}
	}
	if len(task.Warnings) != 2 || task.Warnings[0].Text != NOTE_ORGANIZE_FILE_FAILED || !strings.HasPrefix(task.Warnings[0].Detail, "b.nsp: ") ||
		task.Warnings[1].Text != NOTE_ORGANIZE_FILE_KEPT || !strings.Contains(task.Warnings[1].Detail, "Su juego no está en la biblioteca") {
		t.Fatalf("the notes: %+v", task.Warnings)
	}

	// past the notes shown one by one, the others are counted
	many := []process.Operation{}
	for i := 0; i < organizeNotesShown+5; i++ {
		many = append(many, process.Operation{Kind: process.OP_SKIP, From: "/games/x.nsp", Reason: process.REASON_SPLIT})
	}
	other := web.taskLog().Start(TASK_ORGANIZE, TRIGGER_MANUAL)
	web.noteOrganized(other, many)
	for _, each := range web.taskLog().Snapshot() {
		if each.Id == other {
			last := each.Warnings[len(each.Warnings)-1]
			if len(each.Warnings) != organizeNotesShown+1 || last.Text != NOTE_ORGANIZE_MORE || last.Detail != "5" {
				t.Fatalf("%d notes, the last %+v", len(each.Warnings), last)
			}
		}
	}
}
