# Event Signup Platform

## Build & Check

- **Do NOT run `go build ./...`** — it produces an `event-signup` binary in the project root. Use `go vet ./...` instead to check compilation without producing artifacts.
- To actually run the app: `go run .`
- Tests: `go test ./...`
- Single dependency: `github.com/mattn/go-sqlite3` (requires CGo)

## Architecture

Single Go module, all source files at root level:

| File | Purpose |
|------|---------|
| `main.go` | Entry point, route registration, middleware, template loading |
| `handlers.go` | All HTTP handlers (public pages, admin CRUD, API endpoints) |
| `models.go` | Data structs, SQLite CRUD, migrations (`migrateColumn` pattern) |
| `i18n.go` | FR/EN bilingual translations |
| `ai.go` | AI-powered task import from natural language |
| `schema.sql` | SQLite schema (embedded via `//go:embed`) |

## Database

- SQLite with WAL mode
- Schema in `schema.sql`, embedded at compile time
- Migrations use `migrateColumn()` — adds columns to existing DBs, no-ops on fresh ones
- When adding a new column: update BOTH `schema.sql` (for fresh DBs) AND add a migration in `models.go` (for existing DBs)

## Event Types

- `tasks` — users register for specific tasks within groups
- `attendance` — RSVP yes/no with optional message, email-based lookup to update

## Templates

- Go `html/template` with layout pattern (`{{define "content"}}` + `{{template "layout" .}}`)
- Template functions: `t` (translate), `loc` (pick FR/EN field), `lang`, `isAdmin`, `formatDate`, `formatTime`, `formatDateTime`, `nl2br`, `json`
- Static assets in `static/` directory, embedded via `//go:embed`

## Key Patterns

- Admin routes protected by `app.requireAdmin()` middleware
- Inline API editing: `admin.js` auto-saves via `/admin/api/event/save`
- Client-side: localStorage for user convenience (prefilling forms on return visits)
- CSV export available for both event types
