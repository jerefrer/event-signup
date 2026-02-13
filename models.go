package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Event struct {
	ID            int64
	Slug          string
	TitleFR       string
	TitleEN       string
	DescriptionFR string
	DescriptionEN string
	EventDate     string
	EventTime     string
	EventType     string // "tasks" or "attendance"
	CreatedAt     time.Time
	RegCount      int
	AttendanceYes int
	AttendanceNo  int
}

type TaskGroup struct {
	ID            int64
	EventID       int64
	ParentGroupID sql.NullInt64
	TitleFR       string
	TitleEN       string
	Position      int
}

type Task struct {
	ID            int64
	EventID       int64
	GroupID       sql.NullInt64
	TitleFR       string
	TitleEN       string
	DescriptionFR string
	DescriptionEN string
	MaxSlots      sql.NullInt64
	Position      int
}

type Registration struct {
	ID        int64
	TaskID    int64
	FirstName string
	LastName  string
	Email     string
	Phone     string
	Token     string
	CreatedAt time.Time
}

type TaskView struct {
	Task
	RegCount      int
	SlotsLeft     int // -1 means unlimited
	IsFull        bool
	Registrations []Registration
}

// TreeNode represents either a group or a task in a mixed tree.
type TreeNode struct {
	Type     string     // "group" or "task"
	Group    *TaskGroup // non-nil if Type == "group"
	Task     *TaskView  // non-nil if Type == "task"
	Children []TreeNode // children (only meaningful for groups)
}

// FlatGroup is used for parent-selection dropdowns.
type FlatGroup struct {
	ID      int64
	TitleFR string
	TitleEN string
	Depth   int
}

// ReorderNode is the JSON structure for the reorder API.
type ReorderNode struct {
	Type     string        `json:"type"` // "group" or "task"
	ID       int64         `json:"id"`
	Children []ReorderNode `json:"children,omitempty"`
}

func InitDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	// Migrations run first so that existing tables gain new columns
	// before schema.sql tries to create indexes on them.
	// For new DBs, migrateColumn safely no-ops when the table doesn't exist yet.
	migrateColumn(db, "events", "event_time", "ALTER TABLE events ADD COLUMN event_time TEXT NOT NULL DEFAULT ''")
	migrateColumn(db, "task_groups", "parent_group_id", "ALTER TABLE task_groups ADD COLUMN parent_group_id INTEGER REFERENCES task_groups(id) ON DELETE SET NULL")

	// Migrate registrations: name → first_name + last_name
	migrateColumn(db, "events", "event_type", "ALTER TABLE events ADD COLUMN event_type TEXT NOT NULL DEFAULT 'tasks'")

	migrateColumn(db, "registrations", "first_name", "ALTER TABLE registrations ADD COLUMN first_name TEXT NOT NULL DEFAULT ''")
	migrateColumn(db, "registrations", "last_name", "ALTER TABLE registrations ADD COLUMN last_name TEXT NOT NULL DEFAULT ''")
	// Copy old name to last_name for existing records
	db.Exec("UPDATE registrations SET last_name = name WHERE last_name = '' AND name IS NOT NULL AND name != ''")
	// Drop the old name column so its NOT NULL constraint doesn't block new INSERTs
	migrateDropColumn(db, "registrations", "name")

	if _, err := db.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("schema init: %w", err)
	}

	return db, nil
}

func migrateColumn(db *sql.DB, table, column, ddl string) {
	var found bool
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var cid int
			var name, typ string
			var notnull int
			var dflt sql.NullString
			var pk int
			rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk)
			if name == column {
				found = true
			}
		}
	}
	if !found {
		db.Exec(ddl)
	}
}

// migrateDropColumn drops a column if it exists (SQLite 3.35.0+).
func migrateDropColumn(db *sql.DB, table, column string) {
	var found bool
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var cid int
			var name, typ string
			var notnull int
			var dflt sql.NullString
			var pk int
			rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk)
			if name == column {
				found = true
			}
		}
	}
	if found {
		db.Exec(fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", table, column))
	}
}

var accentMap = map[rune]rune{
	'à': 'a', 'â': 'a', 'ä': 'a', 'á': 'a', 'ã': 'a',
	'è': 'e', 'ê': 'e', 'ë': 'e', 'é': 'e',
	'ì': 'i', 'î': 'i', 'ï': 'i', 'í': 'i',
	'ò': 'o', 'ô': 'o', 'ö': 'o', 'ó': 'o', 'õ': 'o',
	'ù': 'u', 'û': 'u', 'ü': 'u', 'ú': 'u',
	'ç': 'c', 'ñ': 'n', 'ÿ': 'y', 'ý': 'y',
	'æ': 'a', 'œ': 'o', 'ø': 'o', 'å': 'a',
	'ß': 's',
}

func GenerateSlug(title string) string {
	s := strings.ToLower(title)
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if mapped, ok := accentMap[r]; ok {
			b.WriteRune(mapped)
		} else if r == ' ' || r == '-' || r == '_' {
			b.WriteRune('-')
		}
	}
	slug := strings.Trim(b.String(), "-")
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	if slug == "" {
		slug = "event"
	}
	return slug
}

func GenerateToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func EnsureUniqueSlug(db *sql.DB, slug string, excludeID int64) (string, error) {
	base := slug
	for i := 0; ; i++ {
		candidate := base
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", base, i)
		}
		var count int
		err := db.QueryRow("SELECT COUNT(*) FROM events WHERE slug = ? AND id != ?", candidate, excludeID).Scan(&count)
		if err != nil {
			return "", err
		}
		if count == 0 {
			return candidate, nil
		}
	}
}

// ---- Event CRUD ----

const eventCols = "id, slug, title_fr, title_en, description_fr, description_en, event_date, event_time, event_type, created_at"

func scanEvent(row interface{ Scan(...any) error }) (*Event, error) {
	e := &Event{}
	err := row.Scan(&e.ID, &e.Slug, &e.TitleFR, &e.TitleEN, &e.DescriptionFR, &e.DescriptionEN, &e.EventDate, &e.EventTime, &e.EventType, &e.CreatedAt)
	return e, err
}

func CreateEvent(db *sql.DB, e *Event) error {
	slug, err := EnsureUniqueSlug(db, GenerateSlug(e.TitleFR), 0)
	if err != nil {
		return err
	}
	e.Slug = slug
	if e.EventType == "" {
		e.EventType = "tasks"
	}
	res, err := db.Exec(
		"INSERT INTO events (slug, title_fr, title_en, description_fr, description_en, event_date, event_time, event_type) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		e.Slug, e.TitleFR, e.TitleEN, e.DescriptionFR, e.DescriptionEN, e.EventDate, e.EventTime, e.EventType,
	)
	if err != nil {
		return err
	}
	e.ID, _ = res.LastInsertId()
	return nil
}

func UpdateEvent(db *sql.DB, e *Event) error {
	_, err := db.Exec(
		"UPDATE events SET title_fr=?, title_en=?, description_fr=?, description_en=?, event_date=?, event_time=?, event_type=? WHERE id=?",
		e.TitleFR, e.TitleEN, e.DescriptionFR, e.DescriptionEN, e.EventDate, e.EventTime, e.EventType, e.ID,
	)
	return err
}

func DeleteEvent(db *sql.DB, id int64) error {
	_, err := db.Exec("DELETE FROM events WHERE id=?", id)
	return err
}

func GetEvent(db *sql.DB, id int64) (*Event, error) {
	return scanEvent(db.QueryRow("SELECT "+eventCols+" FROM events WHERE id=?", id))
}

func GetEventBySlug(db *sql.DB, slug string) (*Event, error) {
	return scanEvent(db.QueryRow("SELECT "+eventCols+" FROM events WHERE slug=?", slug))
}

func ListEvents(db *sql.DB) ([]Event, error) {
	rows, err := db.Query("SELECT " + eventCols + " FROM events ORDER BY event_date DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, *e)
	}
	return events, rows.Err()
}

// ---- TaskGroup CRUD ----

const groupCols = "id, event_id, parent_group_id, title_fr, title_en, position"

func scanGroup(row interface{ Scan(...any) error }) (*TaskGroup, error) {
	g := &TaskGroup{}
	err := row.Scan(&g.ID, &g.EventID, &g.ParentGroupID, &g.TitleFR, &g.TitleEN, &g.Position)
	return g, err
}

func CreateTaskGroup(db *sql.DB, g *TaskGroup) error {
	// Auto-assign position at end of siblings
	var maxPos int
	if g.ParentGroupID.Valid {
		db.QueryRow("SELECT COALESCE(MAX(position), -1) FROM task_groups WHERE event_id=? AND parent_group_id=?", g.EventID, g.ParentGroupID.Int64).Scan(&maxPos)
		maxTask := -1
		db.QueryRow("SELECT COALESCE(MAX(position), -1) FROM tasks WHERE event_id=? AND group_id=?", g.EventID, g.ParentGroupID.Int64).Scan(&maxTask)
		if maxTask > maxPos {
			maxPos = maxTask
		}
	} else {
		db.QueryRow("SELECT COALESCE(MAX(position), -1) FROM task_groups WHERE event_id=? AND parent_group_id IS NULL", g.EventID).Scan(&maxPos)
		maxTask := -1
		db.QueryRow("SELECT COALESCE(MAX(position), -1) FROM tasks WHERE event_id=? AND group_id IS NULL", g.EventID).Scan(&maxTask)
		if maxTask > maxPos {
			maxPos = maxTask
		}
	}
	g.Position = maxPos + 1

	res, err := db.Exec(
		"INSERT INTO task_groups (event_id, parent_group_id, title_fr, title_en, position) VALUES (?, ?, ?, ?, ?)",
		g.EventID, g.ParentGroupID, g.TitleFR, g.TitleEN, g.Position,
	)
	if err != nil {
		return err
	}
	g.ID, _ = res.LastInsertId()
	return nil
}

func UpdateTaskGroup(db *sql.DB, g *TaskGroup) error {
	_, err := db.Exec(
		"UPDATE task_groups SET title_fr=?, title_en=? WHERE id=?",
		g.TitleFR, g.TitleEN, g.ID,
	)
	return err
}

func DeleteTaskGroup(db *sql.DB, id int64) error {
	// Get parent of this group to promote children
	var parentID sql.NullInt64
	db.QueryRow("SELECT parent_group_id FROM task_groups WHERE id=?", id).Scan(&parentID)

	// Promote child tasks and child groups to the deleted group's parent
	db.Exec("UPDATE tasks SET group_id=? WHERE group_id=?", parentID, id)
	db.Exec("UPDATE task_groups SET parent_group_id=? WHERE parent_group_id=?", parentID, id)

	_, err := db.Exec("DELETE FROM task_groups WHERE id=?", id)
	return err
}

func GetTaskGroup(db *sql.DB, id int64) (*TaskGroup, error) {
	return scanGroup(db.QueryRow("SELECT " + groupCols + " FROM task_groups WHERE id=?", id))
}

func ListTaskGroups(db *sql.DB, eventID int64) ([]TaskGroup, error) {
	rows, err := db.Query("SELECT "+groupCols+" FROM task_groups WHERE event_id=? ORDER BY position", eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []TaskGroup
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, *g)
	}
	return groups, rows.Err()
}

// ---- Task CRUD ----

func CreateTask(db *sql.DB, t *Task) error {
	// Auto-assign position at end of siblings
	var maxPos int
	if t.GroupID.Valid {
		db.QueryRow("SELECT COALESCE(MAX(position), -1) FROM tasks WHERE event_id=? AND group_id=?", t.EventID, t.GroupID.Int64).Scan(&maxPos)
		maxGroup := -1
		db.QueryRow("SELECT COALESCE(MAX(position), -1) FROM task_groups WHERE event_id=? AND parent_group_id=?", t.EventID, t.GroupID.Int64).Scan(&maxGroup)
		if maxGroup > maxPos {
			maxPos = maxGroup
		}
	} else {
		db.QueryRow("SELECT COALESCE(MAX(position), -1) FROM tasks WHERE event_id=? AND group_id IS NULL", t.EventID).Scan(&maxPos)
		maxGroup := -1
		db.QueryRow("SELECT COALESCE(MAX(position), -1) FROM task_groups WHERE event_id=? AND parent_group_id IS NULL", t.EventID).Scan(&maxGroup)
		if maxGroup > maxPos {
			maxPos = maxGroup
		}
	}
	t.Position = maxPos + 1

	res, err := db.Exec(
		"INSERT INTO tasks (event_id, group_id, title_fr, title_en, description_fr, description_en, max_slots, position) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		t.EventID, t.GroupID, t.TitleFR, t.TitleEN, t.DescriptionFR, t.DescriptionEN, t.MaxSlots, t.Position,
	)
	if err != nil {
		return err
	}
	t.ID, _ = res.LastInsertId()
	return nil
}

func UpdateTask(db *sql.DB, t *Task) error {
	// group_id is managed exclusively by the reorder API (drag-and-drop)
	_, err := db.Exec(
		"UPDATE tasks SET title_fr=?, title_en=?, description_fr=?, description_en=?, max_slots=? WHERE id=?",
		t.TitleFR, t.TitleEN, t.DescriptionFR, t.DescriptionEN, t.MaxSlots, t.ID,
	)
	return err
}

func DeleteTask(db *sql.DB, id int64) error {
	_, err := db.Exec("DELETE FROM tasks WHERE id=?", id)
	return err
}

func GetTask(db *sql.DB, id int64) (*Task, error) {
	t := &Task{}
	err := db.QueryRow(
		"SELECT id, event_id, group_id, title_fr, title_en, description_fr, description_en, max_slots, position FROM tasks WHERE id=?", id,
	).Scan(&t.ID, &t.EventID, &t.GroupID, &t.TitleFR, &t.TitleEN, &t.DescriptionFR, &t.DescriptionEN, &t.MaxSlots, &t.Position)
	return t, err
}

func ListTasks(db *sql.DB, eventID int64) ([]Task, error) {
	rows, err := db.Query(
		"SELECT id, event_id, group_id, title_fr, title_en, description_fr, description_en, max_slots, position FROM tasks WHERE event_id=? ORDER BY position",
		eventID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []Task
	for rows.Next() {
		var t Task
		rows.Scan(&t.ID, &t.EventID, &t.GroupID, &t.TitleFR, &t.TitleEN, &t.DescriptionFR, &t.DescriptionEN, &t.MaxSlots, &t.Position)
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// ---- Tree building ----

func GetTaskViews(db *sql.DB, eventID int64) ([]TaskView, error) {
	tasks, err := ListTasks(db, eventID)
	if err != nil {
		return nil, err
	}
	var views []TaskView
	for _, t := range tasks {
		var count int
		db.QueryRow("SELECT COUNT(*) FROM registrations WHERE task_id=?", t.ID).Scan(&count)
		v := TaskView{Task: t, RegCount: count}
		if t.MaxSlots.Valid {
			v.SlotsLeft = int(t.MaxSlots.Int64) - count
			if v.SlotsLeft < 0 {
				v.SlotsLeft = 0
			}
			v.IsFull = v.SlotsLeft == 0
		} else {
			v.SlotsLeft = -1
		}
		views = append(views, v)
	}
	return views, nil
}

// BuildEventTree builds a mixed tree of groups and tasks for an event.
func BuildEventTree(db *sql.DB, eventID int64) ([]TreeNode, error) {
	groups, err := ListTaskGroups(db, eventID)
	if err != nil {
		return nil, err
	}
	views, err := GetTaskViews(db, eventID)
	if err != nil {
		return nil, err
	}

	// Index by parent
	groupsByParent := map[int64][]TaskGroup{}
	for _, g := range groups {
		pid := int64(0)
		if g.ParentGroupID.Valid {
			pid = g.ParentGroupID.Int64
		}
		groupsByParent[pid] = append(groupsByParent[pid], g)
	}

	tasksByParent := map[int64][]TaskView{}
	for _, t := range views {
		pid := int64(0)
		if t.GroupID.Valid {
			pid = t.GroupID.Int64
		}
		tasksByParent[pid] = append(tasksByParent[pid], t)
	}

	var build func(parentID int64) []TreeNode
	build = func(parentID int64) []TreeNode {
		type posItem struct {
			pos  int
			node TreeNode
		}
		var items []posItem

		for _, g := range groupsByParent[parentID] {
			gCopy := g
			node := TreeNode{Type: "group", Group: &gCopy, Children: build(g.ID)}
			items = append(items, posItem{g.Position, node})
		}
		for _, t := range tasksByParent[parentID] {
			tCopy := t
			node := TreeNode{Type: "task", Task: &tCopy}
			items = append(items, posItem{t.Position, node})
		}

		sort.Slice(items, func(i, j int) bool { return items[i].pos < items[j].pos })

		result := make([]TreeNode, len(items))
		for i, item := range items {
			result[i] = item.node
		}
		return result
	}

	return build(0), nil
}

// BuildFlatGroupList returns groups in tree order with depth info for dropdowns.
func BuildFlatGroupList(db *sql.DB, eventID int64) ([]FlatGroup, error) {
	groups, err := ListTaskGroups(db, eventID)
	if err != nil {
		return nil, err
	}

	byParent := map[int64][]TaskGroup{}
	for _, g := range groups {
		pid := int64(0)
		if g.ParentGroupID.Valid {
			pid = g.ParentGroupID.Int64
		}
		byParent[pid] = append(byParent[pid], g)
	}

	var result []FlatGroup
	var walk func(pid int64, depth int)
	walk = func(pid int64, depth int) {
		children := byParent[pid]
		sort.Slice(children, func(i, j int) bool { return children[i].Position < children[j].Position })
		for _, g := range children {
			result = append(result, FlatGroup{ID: g.ID, TitleFR: g.TitleFR, TitleEN: g.TitleEN, Depth: depth})
			walk(g.ID, depth+1)
		}
	}
	walk(0, 0)
	return result, nil
}

// ---- Reorder (recursive) ----

// ApplyReorder recursively sets positions and parent IDs from a tree structure.
func ApplyReorder(db *sql.DB, nodes []ReorderNode, parentGroupID sql.NullInt64) error {
	for i, node := range nodes {
		switch node.Type {
		case "group":
			if _, err := db.Exec("UPDATE task_groups SET position=?, parent_group_id=? WHERE id=?", i, parentGroupID, node.ID); err != nil {
				return err
			}
			childParent := sql.NullInt64{Int64: node.ID, Valid: true}
			if err := ApplyReorder(db, node.Children, childParent); err != nil {
				return err
			}
		case "task":
			if _, err := db.Exec("UPDATE tasks SET position=?, group_id=? WHERE id=?", i, parentGroupID, node.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---- Registration ----

func RegisterForTask(db *sql.DB, taskID int64, firstName, lastName, email, phone string) (*Registration, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var maxSlots sql.NullInt64
	err = tx.QueryRow("SELECT max_slots FROM tasks WHERE id=?", taskID).Scan(&maxSlots)
	if err != nil {
		return nil, fmt.Errorf("task not found: %w", err)
	}

	if maxSlots.Valid {
		var count int
		tx.QueryRow("SELECT COUNT(*) FROM registrations WHERE task_id=?", taskID).Scan(&count)
		if count >= int(maxSlots.Int64) {
			return nil, fmt.Errorf("task_full")
		}
	}

	token := GenerateToken()
	res, err := tx.Exec(
		"INSERT INTO registrations (task_id, first_name, last_name, email, phone, token) VALUES (?, ?, ?, ?, ?, ?)",
		taskID, firstName, lastName, email, phone, token,
	)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	id, _ := res.LastInsertId()
	return &Registration{ID: id, TaskID: taskID, FirstName: firstName, LastName: lastName, Email: email, Phone: phone, Token: token}, nil
}

func GetRegistrationByToken(db *sql.DB, token string) (*Registration, error) {
	r := &Registration{}
	err := db.QueryRow(
		"SELECT id, task_id, first_name, last_name, email, phone, token, created_at FROM registrations WHERE token=?", token,
	).Scan(&r.ID, &r.TaskID, &r.FirstName, &r.LastName, &r.Email, &r.Phone, &r.Token, &r.CreatedAt)
	return r, err
}

func GetRegistrationByEmailAndEvent(db *sql.DB, email string, eventID int64) (*Registration, error) {
	r := &Registration{}
	err := db.QueryRow(
		`SELECT r.id, r.task_id, r.first_name, r.last_name, r.email, r.phone, r.token, r.created_at
		FROM registrations r JOIN tasks t ON r.task_id = t.id
		WHERE LOWER(r.email) = LOWER(?) AND t.event_id = ?`, email, eventID,
	).Scan(&r.ID, &r.TaskID, &r.FirstName, &r.LastName, &r.Email, &r.Phone, &r.Token, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func DeleteRegistration(db *sql.DB, id int64) error {
	_, err := db.Exec("DELETE FROM registrations WHERE id=?", id)
	return err
}

func DeleteRegistrationByToken(db *sql.DB, token string) error {
	_, err := db.Exec("DELETE FROM registrations WHERE token=?", token)
	return err
}

func ListRegistrations(db *sql.DB, taskID int64) ([]Registration, error) {
	rows, err := db.Query("SELECT id, task_id, first_name, last_name, email, phone, token, created_at FROM registrations WHERE task_id=? ORDER BY created_at", taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var regs []Registration
	for rows.Next() {
		var r Registration
		rows.Scan(&r.ID, &r.TaskID, &r.FirstName, &r.LastName, &r.Email, &r.Phone, &r.Token, &r.CreatedAt)
		regs = append(regs, r)
	}
	return regs, rows.Err()
}

type RegistrationExport struct {
	ID           int64
	GroupTitle   string
	GroupTitleEN string
	TaskTitle    string
	TaskTitleEN  string
	FirstName    string
	LastName     string
	Email        string
	Phone        string
	CreatedAt    time.Time
}

func ListAllRegistrations(db *sql.DB, eventID int64) ([]RegistrationExport, error) {
	rows, err := db.Query(`
		WITH RECURSIVE root_group AS (
			SELECT id, id AS root_id, title_fr, title_en
			FROM task_groups WHERE parent_group_id IS NULL
			UNION ALL
			SELECT tg.id, rg.root_id, rg.title_fr, rg.title_en
			FROM task_groups tg JOIN root_group rg ON tg.parent_group_id = rg.id
		)
		SELECT r.id, COALESCE(rg.title_fr, ''), COALESCE(rg.title_en, ''), t.title_fr, t.title_en, r.first_name, r.last_name, r.email, r.phone, r.created_at
		FROM registrations r
		JOIN tasks t ON r.task_id = t.id
		LEFT JOIN root_group rg ON t.group_id = rg.id
		WHERE t.event_id = ?
		ORDER BY CASE WHEN rg.title_fr IS NOT NULL THEN 0 ELSE 1 END, rg.title_fr, r.last_name, r.first_name
	`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var exports []RegistrationExport
	for rows.Next() {
		var e RegistrationExport
		rows.Scan(&e.ID, &e.GroupTitle, &e.GroupTitleEN, &e.TaskTitle, &e.TaskTitleEN, &e.FirstName, &e.LastName, &e.Email, &e.Phone, &e.CreatedAt)
		exports = append(exports, e)
	}
	return exports, rows.Err()
}

func CountRegistrations(db *sql.DB, eventID int64) int {
	var count int
	db.QueryRow("SELECT COUNT(*) FROM registrations r JOIN tasks t ON r.task_id=t.id WHERE t.event_id=?", eventID).Scan(&count)
	return count
}

// collectTaskViews collects all TaskViews with registrations from a tree.
func CollectTaskViews(tree []TreeNode) []TaskView {
	var result []TaskView
	for _, n := range tree {
		if n.Type == "task" && n.Task != nil {
			result = append(result, *n.Task)
		}
		if n.Type == "group" {
			result = append(result, CollectTaskViews(n.Children)...)
		}
	}
	return result
}

// ---- Attendance (RSVP) ----

type Attendance struct {
	ID        int64
	EventID   int64
	FirstName string
	LastName  string
	Email     string
	Phone     string
	Attending bool
	Message   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func UpsertAttendance(db *sql.DB, eventID int64, firstName, lastName, email, phone string, attending bool, message string) (*Attendance, error) {
	attendingInt := 0
	if attending {
		attendingInt = 1
	}
	// Try to find existing attendance by email for this event
	var existingID int64
	err := db.QueryRow("SELECT id FROM attendances WHERE event_id=? AND LOWER(email)=LOWER(?)", eventID, email).Scan(&existingID)
	if err == nil {
		// Update existing
		_, err = db.Exec(
			"UPDATE attendances SET first_name=?, last_name=?, phone=?, attending=?, message=?, updated_at=CURRENT_TIMESTAMP WHERE id=?",
			firstName, lastName, phone, attendingInt, message, existingID,
		)
		if err != nil {
			return nil, err
		}
		return GetAttendance(db, existingID)
	}
	// Insert new
	res, err := db.Exec(
		"INSERT INTO attendances (event_id, first_name, last_name, email, phone, attending, message) VALUES (?, ?, ?, ?, ?, ?, ?)",
		eventID, firstName, lastName, email, phone, attendingInt, message,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return GetAttendance(db, id)
}

func GetAttendance(db *sql.DB, id int64) (*Attendance, error) {
	a := &Attendance{}
	var attendingInt int
	err := db.QueryRow(
		"SELECT id, event_id, first_name, last_name, email, phone, attending, message, created_at, updated_at FROM attendances WHERE id=?", id,
	).Scan(&a.ID, &a.EventID, &a.FirstName, &a.LastName, &a.Email, &a.Phone, &attendingInt, &a.Message, &a.CreatedAt, &a.UpdatedAt)
	a.Attending = attendingInt != 0
	return a, err
}

func GetAttendanceByEmail(db *sql.DB, email string, eventID int64) (*Attendance, error) {
	a := &Attendance{}
	var attendingInt int
	err := db.QueryRow(
		"SELECT id, event_id, first_name, last_name, email, phone, attending, message, created_at, updated_at FROM attendances WHERE LOWER(email)=LOWER(?) AND event_id=?", email, eventID,
	).Scan(&a.ID, &a.EventID, &a.FirstName, &a.LastName, &a.Email, &a.Phone, &attendingInt, &a.Message, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	a.Attending = attendingInt != 0
	return a, nil
}

func ListAttendances(db *sql.DB, eventID int64) ([]Attendance, error) {
	rows, err := db.Query(
		"SELECT id, event_id, first_name, last_name, email, phone, attending, message, created_at, updated_at FROM attendances WHERE event_id=? ORDER BY last_name, first_name", eventID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var attendances []Attendance
	for rows.Next() {
		var a Attendance
		var attendingInt int
		rows.Scan(&a.ID, &a.EventID, &a.FirstName, &a.LastName, &a.Email, &a.Phone, &attendingInt, &a.Message, &a.CreatedAt, &a.UpdatedAt)
		a.Attending = attendingInt != 0
		attendances = append(attendances, a)
	}
	return attendances, rows.Err()
}

func CountAttendances(db *sql.DB, eventID int64) (yesCount, totalCount int) {
	db.QueryRow("SELECT COUNT(*) FROM attendances WHERE event_id=? AND attending=1", eventID).Scan(&yesCount)
	db.QueryRow("SELECT COUNT(*) FROM attendances WHERE event_id=?", eventID).Scan(&totalCount)
	return
}

func DeleteAttendance(db *sql.DB, id int64) error {
	_, err := db.Exec("DELETE FROM attendances WHERE id=?", id)
	return err
}
