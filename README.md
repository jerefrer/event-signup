# event-signup

A small bilingual (FR/EN) event signup web app in Go, backed by SQLite. Supports task signups, RSVP, and Secret Santa events.

## Setup

```bash
cp .env.example .env
# edit .env — at minimum, set EVENT_SIGNUP_ADMIN_PASSWORD
direnv allow            # loads .env and exports AWS_PROFILE
```

Without direnv: `set -a && source .env && set +a` before running.

Dependencies:

- Go with CGo enabled (`github.com/mattn/go-sqlite3` needs it — default on macOS/Linux).
- [Air](https://github.com/air-verse/air) for live-reload during dev.

## Run

```bash
air                     # dev mode — live-reload on every .go / .html / .css / .js / .sql change
go run .                # one-off run, no reload
```

Air rebuilds, kills the running server, and restarts on every save. The **browser still needs a manual refresh** (Cmd-R / F5) — Air only restarts the server.

Admin UI: <http://localhost:8090/admin>.

Email previews (admin-only) at <http://localhost:8090/dev/emails> — renders the same HTML the app would email, using real data from the latest Secret Santa event. Handy for iterating on email design without sending anything.

## Test

```bash
go test .                       # run the full suite
go test -run TestName .         # run one test
go vet ./...                    # check compilation without producing a binary
```

> **Never run `go build ./...`** — it drops an unwanted `event-signup` binary at the repo root. Use `go vet ./...` to verify compilation.

## Project structure

Single Go module, source at the root level:

| File | Purpose |
|------|---------|
| `main.go` | Entry point, route registration |
| `handlers.go` | HTTP handlers |
| `models.go` | Data structs, SQLite CRUD, migrations |
| `email.go` | Email sending (`LogSender` in dev, `SESSender` in prod) |
| `webhook.go` | SES delivery-event SNS webhook |
| `i18n.go` | FR/EN translations |
| `schema.sql` | SQLite schema (embedded via `//go:embed`) |
| `templates/*.html` | Go `html/template` views |
| `static/*` | CSS, JS, images |

See [CLAUDE.md](CLAUDE.md) for the detailed architecture notes and conventions.
