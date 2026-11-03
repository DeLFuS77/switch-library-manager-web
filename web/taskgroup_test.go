package web

import "github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
import "testing"

func TestRepeatedTasksAreGrouped(t *testing.T) {
	tasks := []tasklog.Task{
		{Id: 5, Kind: tasklog.TASK_SCAN, Status: tasklog.TASK_SUCCESS, Trigger: "startup"},
		{Id: 4, Kind: tasklog.TASK_SCAN, Status: tasklog.TASK_SUCCESS, Trigger: "startup"},
		{Id: 3, Kind: tasklog.TASK_SCAN, Status: tasklog.TASK_SUCCESS, Trigger: "startup", Warnings: []tasklog.Note{{Text: "x"}}},
		{Id: 2, Kind: tasklog.TASK_SCAN, Status: tasklog.TASK_SUCCESS, Trigger: "manual"},
		{Id: 1, Kind: tasklog.TASK_SCAN, Status: tasklog.TASK_SUCCESS, Trigger: "manual"},
	}
	groups := groupTasks(tasks)
	if len(groups) != 3 || groups[0].Count != 2 || groups[0].Ids != "5,4" || groups[1].Count != 1 || groups[2].Ids != "2,1" {
		t.Fatalf("the quiet tasks of the same kind and trigger are grouped: %+v", groups)
	}
}
