CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    slug TEXT NOT NULL UNIQUE,
    title_fr TEXT NOT NULL,
    title_en TEXT NOT NULL DEFAULT '',
    description_fr TEXT NOT NULL DEFAULT '',
    description_en TEXT NOT NULL DEFAULT '',
    event_date TEXT NOT NULL,
    event_time TEXT NOT NULL DEFAULT '',
    event_type TEXT NOT NULL DEFAULT 'tasks',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS task_groups (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    parent_group_id INTEGER REFERENCES task_groups(id) ON DELETE SET NULL,
    title_fr TEXT NOT NULL,
    title_en TEXT NOT NULL DEFAULT '',
    position INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    group_id INTEGER REFERENCES task_groups(id) ON DELETE SET NULL,
    title_fr TEXT NOT NULL,
    title_en TEXT NOT NULL DEFAULT '',
    description_fr TEXT NOT NULL DEFAULT '',
    description_en TEXT NOT NULL DEFAULT '',
    max_slots INTEGER,
    position INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS registrations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    first_name TEXT NOT NULL DEFAULT '',
    last_name TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL,
    phone TEXT NOT NULL,
    token TEXT NOT NULL UNIQUE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_task_groups_event ON task_groups(event_id);
CREATE INDEX IF NOT EXISTS idx_task_groups_parent ON task_groups(parent_group_id);
CREATE INDEX IF NOT EXISTS idx_tasks_event ON tasks(event_id);
CREATE INDEX IF NOT EXISTS idx_tasks_group ON tasks(group_id);
CREATE INDEX IF NOT EXISTS idx_registrations_task ON registrations(task_id);
CREATE INDEX IF NOT EXISTS idx_registrations_token ON registrations(token);

CREATE TABLE IF NOT EXISTS attendances (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    first_name TEXT NOT NULL DEFAULT '',
    last_name TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL,
    phone TEXT NOT NULL DEFAULT '',
    attending INTEGER NOT NULL DEFAULT 1,
    message TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_attendances_event ON attendances(event_id);
