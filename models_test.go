package main

import (
	"database/sql"
	"strings"
	"testing"
)

// ---- Slug generation ----

func TestGenerateSlug(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"Préparation pour l'Hosar", "preparation-pour-lhosar"},
		{"Simple Title", "simple-title"},
		{"  Spaces  Everywhere  ", "spaces-everywhere"},
		{"Événements Chanteloube", "evenements-chanteloube"},
		{"", "event"},
		{"---", "event"},
		{"Café & Résumé", "cafe-resume"},
	}
	for _, tt := range tests {
		got := GenerateSlug(tt.input)
		if got != tt.want {
			t.Errorf("GenerateSlug(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestEnsureUniqueSlug(t *testing.T) {
	db := testDB(t)
	e := &Event{TitleFR: "Test", EventDate: "2026-01-01"}
	CreateEvent(db, e)

	slug, err := EnsureUniqueSlug(db, e.Slug, 0)
	if err != nil {
		t.Fatal(err)
	}
	if slug == e.Slug {
		t.Errorf("expected different slug, got same %q", slug)
	}
	if !strings.HasPrefix(slug, e.Slug) {
		t.Errorf("expected slug starting with %q, got %q", e.Slug, slug)
	}
}

// ---- Event CRUD ----

func TestEventCRUD(t *testing.T) {
	db := testDB(t)

	// Create
	e := &Event{TitleFR: "Mon événement", TitleEN: "My event", EventDate: "2026-03-15", EventTime: "14:00"}
	if err := CreateEvent(db, e); err != nil {
		t.Fatalf("create: %v", err)
	}
	if e.ID == 0 {
		t.Fatal("expected non-zero ID")
	}
	if e.Slug == "" {
		t.Fatal("expected non-empty slug")
	}

	// Read by ID
	got, err := GetEvent(db, e.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.TitleFR != "Mon événement" {
		t.Errorf("title = %q", got.TitleFR)
	}

	// Read by slug
	got2, err := GetEventBySlug(db, e.Slug)
	if err != nil {
		t.Fatalf("get by slug: %v", err)
	}
	if got2.ID != e.ID {
		t.Errorf("slug lookup returned different ID")
	}

	// Update
	e.TitleEN = "Updated"
	if err := UpdateEvent(db, e); err != nil {
		t.Fatalf("update: %v", err)
	}
	got3, _ := GetEvent(db, e.ID)
	if got3.TitleEN != "Updated" {
		t.Errorf("update didn't persist")
	}

	// List
	events, err := ListEvents(db)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("expected 1 event, got %d", len(events))
	}

	// Delete
	if err := DeleteEvent(db, e.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = GetEvent(db, e.ID)
	if err == nil {
		t.Error("expected error after delete")
	}
}

// ---- Task & Group CRUD ----

func TestTaskGroupCRUD(t *testing.T) {
	db := testDB(t)
	e := seedEvent(t, db)

	// Create group
	g := &TaskGroup{EventID: e.ID, TitleFR: "Groupe A", TitleEN: "Group A"}
	if err := CreateTaskGroup(db, g); err != nil {
		t.Fatalf("create group: %v", err)
	}
	if g.ID == 0 {
		t.Fatal("expected non-zero group ID")
	}

	// Create task in group
	tk := seedTask(t, db, e.ID, "Tâche 1", int64Ptr(5))
	if tk.ID == 0 {
		t.Fatal("expected non-zero task ID")
	}

	// Update group
	g.TitleFR = "Groupe B"
	if err := UpdateTaskGroup(db, g); err != nil {
		t.Fatalf("update group: %v", err)
	}

	// List
	groups, _ := ListTaskGroups(db, e.ID)
	if len(groups) != 1 || groups[0].TitleFR != "Groupe B" {
		t.Errorf("unexpected groups: %+v", groups)
	}

	tasks, _ := ListTasks(db, e.ID)
	if len(tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(tasks))
	}

	// Delete group promotes children
	DeleteTaskGroup(db, g.ID)
	groups2, _ := ListTaskGroups(db, e.ID)
	if len(groups2) != 0 {
		t.Errorf("group not deleted")
	}

	// Delete task
	DeleteTask(db, tk.ID)
	tasks2, _ := ListTasks(db, e.ID)
	if len(tasks2) != 0 {
		t.Errorf("task not deleted")
	}
}

// ---- Registration ----

func TestRegistration(t *testing.T) {
	db := testDB(t)
	e := seedEvent(t, db)
	tk := seedTask(t, db, e.ID, "Cuisine", int64Ptr(2))

	// Register
	reg, err := RegisterForTask(db, tk.ID, "Alice", "Dupont", "alice@test.com", "0601")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if reg.Token == "" {
		t.Fatal("expected non-empty token")
	}

	// Get by token
	got, err := GetRegistrationByToken(db, reg.Token)
	if err != nil {
		t.Fatalf("get by token: %v", err)
	}
	if got.Email != "alice@test.com" {
		t.Errorf("email = %q", got.Email)
	}

	// List
	regs, _ := ListRegistrations(db, tk.ID)
	if len(regs) != 1 {
		t.Errorf("expected 1 reg, got %d", len(regs))
	}

	// Count
	count := CountRegistrations(db, e.ID)
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}

	// Delete by token
	DeleteRegistrationByToken(db, reg.Token)
	_, err = GetRegistrationByToken(db, reg.Token)
	if err == nil {
		t.Error("expected error after delete by token")
	}
}

func TestRegistrationSlotLimit(t *testing.T) {
	db := testDB(t)
	e := seedEvent(t, db)
	tk := seedTask(t, db, e.ID, "Limited", int64Ptr(1))

	_, err := RegisterForTask(db, tk.ID, "Alice", "Dupont", "alice@test.com", "0601")
	if err != nil {
		t.Fatalf("first registration: %v", err)
	}

	_, err = RegisterForTask(db, tk.ID, "Bob", "Martin", "bob@test.com", "0602")
	if err == nil {
		t.Fatal("expected task_full error")
	}
	if !strings.Contains(err.Error(), "task_full") {
		t.Errorf("expected task_full, got: %v", err)
	}
}

func TestRegistrationUnlimited(t *testing.T) {
	db := testDB(t)
	e := seedEvent(t, db)
	tk := seedTask(t, db, e.ID, "Unlimited", nil)

	for i := 0; i < 10; i++ {
		_, err := RegisterForTask(db, tk.ID, "User", "Test", "user@test.com", "0600")
		if err != nil {
			t.Fatalf("registration %d: %v", i, err)
		}
	}
}

// ---- Duplicate email detection ----

func TestGetRegistrationByEmailAndEvent(t *testing.T) {
	db := testDB(t)
	e := seedEvent(t, db)
	tk := seedTask(t, db, e.ID, "Task A", int64Ptr(5))

	// No registration yet
	_, err := GetRegistrationByEmailAndEvent(db, "alice@test.com", e.ID)
	if err == nil {
		t.Fatal("expected no result")
	}

	// Register
	reg, _ := RegisterForTask(db, tk.ID, "Alice", "Dupont", "alice@test.com", "0601")

	// Find by exact email
	found, err := GetRegistrationByEmailAndEvent(db, "alice@test.com", e.ID)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if found.ID != reg.ID {
		t.Errorf("wrong registration found")
	}

	// Case-insensitive
	found2, err := GetRegistrationByEmailAndEvent(db, "Alice@Test.COM", e.ID)
	if err != nil {
		t.Fatalf("case-insensitive lookup: %v", err)
	}
	if found2.ID != reg.ID {
		t.Error("case-insensitive lookup failed")
	}

	// Different event returns nothing
	e2 := &Event{TitleFR: "Other", EventDate: "2026-07-01"}
	CreateEvent(db, e2)
	_, err = GetRegistrationByEmailAndEvent(db, "alice@test.com", e2.ID)
	if err == nil {
		t.Error("should not find registration for different event")
	}
}

// ---- TaskView / slots ----

func TestGetTaskViews(t *testing.T) {
	db := testDB(t)
	e := seedEvent(t, db)
	tk1 := seedTask(t, db, e.ID, "Limited", int64Ptr(2))
	tk2 := seedTask(t, db, e.ID, "Unlimited", nil)

	RegisterForTask(db, tk1.ID, "A", "A", "a@t.com", "01")
	RegisterForTask(db, tk1.ID, "B", "B", "b@t.com", "02")

	views, err := GetTaskViews(db, e.ID)
	if err != nil {
		t.Fatalf("get views: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("expected 2 views, got %d", len(views))
	}

	var limited, unlimited TaskView
	for _, v := range views {
		if v.ID == tk1.ID {
			limited = v
		}
		if v.ID == tk2.ID {
			unlimited = v
		}
	}

	if !limited.IsFull {
		t.Error("limited task should be full")
	}
	if limited.SlotsLeft != 0 {
		t.Errorf("limited slots = %d, want 0", limited.SlotsLeft)
	}
	if limited.RegCount != 2 {
		t.Errorf("limited regcount = %d, want 2", limited.RegCount)
	}

	if unlimited.IsFull {
		t.Error("unlimited task should not be full")
	}
	if unlimited.SlotsLeft != -1 {
		t.Errorf("unlimited slots = %d, want -1", unlimited.SlotsLeft)
	}
}

// ---- Tree building ----

func TestBuildEventTree(t *testing.T) {
	db := testDB(t)
	e := seedEvent(t, db)

	g := &TaskGroup{EventID: e.ID, TitleFR: "Group 1"}
	CreateTaskGroup(db, g)

	tk1 := &Task{EventID: e.ID, TitleFR: "Ungrouped", MaxSlots: sql.NullInt64{Int64: 3, Valid: true}}
	CreateTask(db, tk1)

	tk2 := &Task{EventID: e.ID, GroupID: sql.NullInt64{Int64: g.ID, Valid: true}, TitleFR: "In group"}
	CreateTask(db, tk2)

	tree, err := BuildEventTree(db, e.ID)
	if err != nil {
		t.Fatalf("build tree: %v", err)
	}
	if len(tree) == 0 {
		t.Fatal("empty tree")
	}

	// Should contain both groups and tasks at various levels
	var foundGroup, foundTask bool
	for _, node := range tree {
		if node.Type == "group" {
			foundGroup = true
		}
		if node.Type == "task" {
			foundTask = true
		}
	}
	if !foundGroup {
		t.Error("expected a group node in tree")
	}
	if !foundTask {
		t.Error("expected a task node in tree")
	}
}

// ---- Migration: old DB with name column ----

func TestMigrationFromOldSchema(t *testing.T) {
	db := testOldDB(t)

	// Seed an old-style registration with the name column
	db.Exec("INSERT INTO events (slug, title_fr, event_date) VALUES ('test', 'Test', '2026-01-01')")
	db.Exec("INSERT INTO tasks (event_id, title_fr, position) VALUES (1, 'Task', 0)")
	db.Exec("INSERT INTO registrations (task_id, name, email, phone, token) VALUES (1, 'OldUser', 'old@test.com', '0600', 'oldtoken')")

	// Run migrations (same logic as InitDB, but on our in-memory DB)
	migrateColumn(db, "registrations", "first_name", "ALTER TABLE registrations ADD COLUMN first_name TEXT NOT NULL DEFAULT ''")
	migrateColumn(db, "registrations", "last_name", "ALTER TABLE registrations ADD COLUMN last_name TEXT NOT NULL DEFAULT ''")
	db.Exec("UPDATE registrations SET last_name = name WHERE last_name = '' AND name IS NOT NULL AND name != ''")
	migrateDropColumn(db, "registrations", "name")

	// Re-apply schema (CREATE TABLE IF NOT EXISTS is a no-op for existing tables)
	if _, err := db.Exec(schemaSQL); err != nil {
		t.Fatalf("apply new schema: %v", err)
	}

	// Verify old data was migrated
	reg, err := GetRegistrationByToken(db, "oldtoken")
	if err != nil {
		t.Fatalf("old registration not found after migration: %v", err)
	}
	if reg.LastName != "OldUser" {
		t.Errorf("expected last_name='OldUser', got %q", reg.LastName)
	}

	// Now register a new user — this is what was failing with NOT NULL on name
	newReg, err := RegisterForTask(db, 1, "New", "User", "new@test.com", "0601")
	if err != nil {
		t.Fatalf("register after migration: %v", err)
	}
	if newReg.FirstName != "New" || newReg.LastName != "User" {
		t.Errorf("new reg = %q %q", newReg.FirstName, newReg.LastName)
	}
}
