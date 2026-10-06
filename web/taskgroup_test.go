package web

import "testing"

func TestRepeatedTasksAreGrouped(t *testing.T) {
	tasks := []Task{
		{Id: 5, Kind: TASK_SCAN, Status: TASK_SUCCESS, Trigger: "startup"},
		{Id: 4, Kind: TASK_SCAN, Status: TASK_SUCCESS, Trigger: "startup"},
		{Id: 3, Kind: TASK_SCAN, Status: TASK_SUCCESS, Trigger: "startup", Warnings: []TaskNote{{Text: "x"}}},
		{Id: 2, Kind: TASK_SCAN, Status: TASK_SUCCESS, Trigger: "manual"},
		{Id: 1, Kind: TASK_SCAN, Status: TASK_SUCCESS, Trigger: "manual"},
	}
	groups := groupTasks(tasks)
	if len(groups) != 3 || groups[0].Count != 2 || groups[0].Ids != "5,4" || groups[1].Count != 1 || groups[2].Ids != "2,1" {
		t.Fatalf("the quiet tasks of the same kind and trigger are grouped: %+v", groups)
	}
}
