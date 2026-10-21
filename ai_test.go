package main

import (
	"database/sql"
	"testing"
	"time"
)

func TestAIClientHasTimeout(t *testing.T) {
	if aiHTTPClient.Timeout != 90*time.Second {
		t.Errorf("aiHTTPClient.Timeout = %v, want 90s", aiHTTPClient.Timeout)
	}
}

// AI-returned IDs are untrusted: they must never reach rows of another event.
func TestApplyAINodesCannotTouchOtherEvents(t *testing.T) {
	db := testDB(t)
	mine := seedEvent(t, db)
	theirs := seedEvent(t, db)

	theirGroup := &TaskGroup{EventID: theirs.ID, TitleFR: "Leur groupe", TitleEN: "Their group"}
	if err := CreateTaskGroup(db, theirGroup); err != nil {
		t.Fatal(err)
	}
	theirTask := seedTask(t, db, theirs.ID, "Leur tâche", int64Ptr(3))

	nodes := []AINode{
		{Type: "group", ID: &theirGroup.ID, TitleFR: "Piraté", Children: []AINode{
			{Type: "task", ID: &theirTask.ID, TitleFR: "Piraté aussi"},
		}},
	}
	pos := 0
	if err := applyAINodes(db, mine.ID, nodes, sql.NullInt64{}, &pos); err != nil {
		t.Fatalf("applyAINodes: %v", err)
	}

	g, _ := GetTaskGroup(db, theirGroup.ID)
	if g.TitleFR != "Leur groupe" {
		t.Errorf("another event's group was modified: %q", g.TitleFR)
	}
	tk, _ := GetTask(db, theirTask.ID)
	if tk.TitleFR != "Leur tâche" || tk.EventID != theirs.ID {
		t.Errorf("another event's task was modified: %+v", tk)
	}

	// The foreign IDs are treated as "new" items, created in the current event.
	groups, _ := ListTaskGroups(db, mine.ID)
	tasks, _ := ListTasks(db, mine.ID)
	if len(groups) != 1 || groups[0].TitleFR != "Piraté" {
		t.Errorf("expected the group to be created in the current event, got %+v", groups)
	}
	if len(tasks) != 1 || !tasks[0].GroupID.Valid || tasks[0].GroupID.Int64 != groups[0].ID {
		t.Errorf("the task must hang under the new group of the current event: %+v", tasks)
	}
}

func TestApplyAINodesStillUpdatesOwnRows(t *testing.T) {
	db := testDB(t)
	e := seedEvent(t, db)
	tk := seedTask(t, db, e.ID, "Avant", int64Ptr(2))
	nodes := []AINode{{Type: "task", ID: &tk.ID, TitleFR: "Après"}}
	pos := 0
	if err := applyAINodes(db, e.ID, nodes, sql.NullInt64{}, &pos); err != nil {
		t.Fatal(err)
	}
	got, _ := GetTask(db, tk.ID)
	if got.TitleFR != "Après" {
		t.Errorf("own task not updated: %q", got.TitleFR)
	}
	if all, _ := ListTasks(db, e.ID); len(all) != 1 {
		t.Errorf("an update must not create a duplicate, got %d tasks", len(all))
	}
}
