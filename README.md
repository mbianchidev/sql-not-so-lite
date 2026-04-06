# sql-not-so-lite

Lightweight SQLite-as-a-service daemon. Manages multiple SQLite databases as files, provides a gRPC API for applications and a web GUI for debugging.

```
┌──────────┐  gRPC   ┌─────────────────┐    ┌────────────────┐
│  App A   │────────▶│                 │───▶│ app_a.sqlite   │
└──────────┘         │ sql-not-so-lite │    ├────────────────┤
┌──────────┐  gRPC   │    daemon       │───▶│ app_b.sqlite   │
│  App B   │────────▶│                 │    └────────────────┘
└──────────┘         │ :50051 (gRPC)   │    ~/.sql-not-so-lite/
                     │ :9147  (HTTP)   │         databases/
┌──────────┐  HTTP   │                 │
│  Web GUI │────────▶│                 │    ┌────────────────┐
└──────────┘         │   ┌──────────┐  │───▶│ catalog.sqlite │
                     │   │ scanner  │  │    └────────────────┘
  ~/                 │   │ catalog  │  │    ┌────────────────┐
  ├── .docker/       │   │ replica- │  │───▶│   replicas/    │
  ├── workspace/     │   │   tor    │  │    │   snapshots/   │
  ├── app-data/  ◀───│   │ schema   │  │    └────────────────┘
  └── .../*.sqlite   │   └──────────┘  │    ~/.sql-not-so-lite/
                     └─────────────────┘
```

## Features

- **Multi-database**: Each app creates its own `.sqlite` file
- **gRPC API**: Typed, high-performance API for applications
- **Web GUI**: Browse tables, view schemas, run SQL queries with Monaco editor
- **Idle management**: Closes idle connections automatically, stays dormant when unused (~5-10MB RSS)
- **Single binary**: GUI embedded via `go:embed` — one file to run
- **Cross-platform**: macOS (launchd), Linux (systemd), Docker
- **Database discovery**: Scans `$HOME` for SQLite databases across containers, app data dirs, and workspaces
- **WAL-aware replication**: Replicates discovered databases with WAL-optimized change detection
- **Point-in-time recovery**: Restore any discovered database from snapshots
- **Schema versioning**: Tracks schema changes with transition history (v0 = initial creation)
- **GitHub repo detection**: Links discovered databases to their upstream GitHub repositories

## Quick Start

### Build from source

```bash
# Prerequisites: Go 1.25+, Node.js 22+, protoc (for proto changes)
make all        # Build GUI + Go binary
./sqnsl start   # Start daemon (foreground)
```

### Install as service

```bash
make install       # Copies binary to /usr/local/bin
sqnsl install      # Installs as launchd (macOS) or systemd (Linux) service
sqnsl gui          # Opens web GUI in browser
```

### Docker

```bash
make docker-up     # Start with docker compose
# GUI at http://localhost:9147
# gRPC at localhost:50051
```

## CLI Reference

```
sqnsl start [-d]    Start daemon (-d for background)
sqnsl stop          Stop running daemon
sqnsl status        Show daemon status
sqnsl list          List all databases
sqnsl gui           Open web GUI in browser
sqnsl install       Install as system service
sqnsl uninstall     Remove system service
sqnsl config        Show configuration
sqnsl config init   Create default config file
sqnsl version       Print version
sqnsl scan [path...]       Scan for SQLite databases
sqnsl discovered           List discovered databases
sqnsl replicate <name>     Start replicating a discovered database
sqnsl replicate stop <name> Stop replication
sqnsl restore <name>       Restore from replica snapshot
sqnsl restore <name> -v N  Restore specific snapshot version
sqnsl restore <name> --to <path>  Restore to alternate path
sqnsl versions <name>      List schema versions
sqnsl transitions <name>   Show schema transition history
```

## Configuration

Config file: `~/.sql-not-so-lite/config.toml`

```toml
[server]
grpc_port = 50051
http_port = 9147
data_dir = "~/.sql-not-so-lite/databases"

[idle]
connection_timeout = "5m"    # Close DB after 5 min idle
check_interval = "30s"       # Check for idle DBs every 30s

[limits]
max_databases = 100
max_query_size = 10485760    # 10MB
max_result_rows = 100000

[logging]
level = "info"
file = "~/.sql-not-so-lite/sqnsl.log"

[scanner]
scan_root = "~/"
file_extensions = [".sqlite", ".db", ".sqlite3", ".sqlitedb"]
exclude_patterns = ["node_modules", ".git/objects", "*.tmp"]
scan_interval = "1h"

[replicator]
enabled = true
sync_interval = "5s"
snapshot_retention = 10
replica_dir = "~/.sql-not-so-lite/replicas"
snapshot_dir = "~/.sql-not-so-lite/snapshots"
```

Run `sqnsl config init` to create the default config file.

## gRPC API

Connect to `localhost:50051`. Proto definition: [`api/proto/sqnsl.proto`](api/proto/sqnsl.proto)

### Services

| RPC | Description |
|-----|-------------|
| `CreateDatabase(name)` | Create a new `.sqlite` database |
| `ListDatabases()` | List all databases with metadata |
| `DropDatabase(name)` | Delete a database and its file |
| `GetDatabaseInfo(name)` | Get details about a database |
| `Execute(db, sql, params)` | Run INSERT/UPDATE/DELETE |
| `Query(db, sql, params, limit, offset)` | Run SELECT with pagination |
| `GetSchema(db)` | Get tables, columns, indexes |
| `Ping()` | Health check with stats |

### Example (grpcurl)

```bash
# List databases
grpcurl -plaintext localhost:50051 sqnsl.v1.SqlNotSoLite/ListDatabases

# Create a database
grpcurl -plaintext -d '{"name":"myapp"}' localhost:50051 sqnsl.v1.SqlNotSoLite/CreateDatabase

# Execute SQL
grpcurl -plaintext -d '{"database":"myapp","sql":"CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)"}' \
  localhost:50051 sqnsl.v1.SqlNotSoLite/Execute

# Query
grpcurl -plaintext -d '{"database":"myapp","sql":"SELECT * FROM users","limit":10}' \
  localhost:50051 sqnsl.v1.SqlNotSoLite/Query
```

## REST API (for GUI)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/databases` | List all databases |
| POST | `/api/databases` | Create database `{"name":"..."}` |
| GET | `/api/databases/:name` | Get database info |
| DELETE | `/api/databases/:name` | Drop database |
| GET | `/api/databases/:name/schema` | Full schema |
| GET | `/api/databases/:name/tables` | List table names |
| GET | `/api/databases/:name/tables/:table?limit=N&offset=N` | Table data |
| POST | `/api/databases/:name/query` | Execute SQL `{"sql":"..."}` |
| GET | `/api/health` | Health check |
| GET | `/api/stats` | Memory, connections, uptime |
| POST | `/api/scan` | Trigger filesystem scan |
| GET | `/api/discovered` | List discovered databases |
| GET | `/api/discovered/:id` | Get discovered DB details |
| POST | `/api/discovered/:id/replicate` | Start replication |
| DELETE | `/api/discovered/:id/replicate` | Stop replication |
| POST | `/api/discovered/:id/restore` | Restore `{"version":N}` |
| GET | `/api/discovered/:id/snapshots` | List snapshots |
| GET | `/api/discovered/:id/versions` | Schema versions |
| GET | `/api/discovered/:id/transitions` | Schema transitions |

## Web GUI

Access at `http://localhost:9147` when the daemon is running.

- **Sidebar**: Lists all databases with active/idle status
- **Table Browser**: Click a database → see tables → click to browse rows
- **Schema Viewer**: Column types, constraints, indexes, foreign keys
- **SQL Editor**: Monaco-based with syntax highlighting and Ctrl+Enter execution
- **Results**: Sortable table with CSV/JSON export
- **Dark/Light theme**: Toggle in header
- **Discovered databases**: Panel showing all discovered SQLite databases with priority badges and GitHub repo links
- **Replication status**: Live indicators for replication state per database
- **Schema timeline**: Version history viewer with schema transition diffs
- **Snapshot restore**: One-click restore from any available snapshot

## Database Discovery & Replication

### Scanning

The scanner walks `$HOME` (configurable via `scan_root`) looking for SQLite files. Each candidate is validated by checking the first 16 bytes for the SQLite magic header (`SQLite format 3\000`). Discovered databases are recorded in an internal catalog (`catalog.sqlite`) with metadata like path, size, modification time, and priority tier.

Scanning runs on a configurable interval (`scan_interval`) and can also be triggered on-demand via `sqnsl scan` or `POST /api/scan`.

### Priority tiers

Discovered databases are classified by location to surface the most relevant ones first:

| Priority | Location pattern | Examples |
|----------|-----------------|----------|
| 1 — Docker/OrbStack | `~/.docker/`, `~/.orbstack/` | Container-managed databases |
| 2 — Workspace | `~/workspace/`, `~/projects/` | Active development databases |
| 3 — Copilot CLI | `~/.copilot/` | Copilot session databases |
| 4 — App Data | `~/Library/`, `~/.config/`, `~/.local/` | Application state databases |
| 5 — Other | Everything else under `$HOME` | Miscellaneous databases |

### GitHub repo detection

For each discovered database, the scanner walks up the directory tree looking for a `.git/config` file. If found, it parses the remote URL to extract the GitHub repository (e.g., `owner/repo`) and stores the association in the catalog. This lets you see which project each database belongs to.

### Replication

Replication creates and maintains a copy of a discovered database under `replica_dir`:

1. **Initial sync**: Uses `VACUUM INTO` to create a consistent baseline copy of the source database.
2. **Incremental sync**: On each `sync_interval` tick, the replicator checks the source WAL for new frames. If changes are detected, a new snapshot is taken and the replica is updated.
3. **Snapshots**: Each sync creates a timestamped snapshot in `snapshot_dir`. Old snapshots are pruned based on `snapshot_retention`.

WAL-aware change detection avoids unnecessary copies when a database hasn't changed.

### Schema versioning

Every discovered database has its schema tracked. Version 0 (`v0`) represents the initial schema at discovery time. Each subsequent schema change increments the version and records a transition containing the before/after DDL diff.

View schema history with `sqnsl versions <name>` and transition details with `sqnsl transitions <name>`.

### Point-in-time recovery

Restore a discovered database from any retained snapshot:

```bash
sqnsl restore mydb              # Restore latest snapshot (in-place)
sqnsl restore mydb -v 3         # Restore snapshot version 3
sqnsl restore mydb --to ./copy  # Restore to alternate path
```

Restores are atomic — the target file is written via a temporary file and renamed.

## Architecture

```
sql-not-so-lite/
├── cmd/sqnsl/           # CLI entrypoint (cobra)
├── internal/
│   ├── catalog/         # Internal meta-database (catalog.sqlite)
│   ├── scanner/         # Filesystem SQLite discovery
│   ├── replicator/      # WAL-aware replication engine
│   │   └── wal/         # WAL frame parser
│   ├── schema/          # Schema version tracking
│   ├── config/          # TOML config loading
│   ├── daemon/          # Lifecycle, PID file, signals
│   ├── server/          # gRPC + HTTP servers
│   ├── service/         # Core business logic
│   ├── store/           # SQLite connection manager
│   └── idle/            # Idle detection & resource release
├── api/proto/           # Protobuf definitions + generated code
├── web/                 # React + Vite GUI source
├── deploy/              # launchd, systemd, Docker
└── Makefile
```

**Key design decisions:**

- **Pure Go SQLite** (`modernc.org/sqlite`): No CGO, cross-compiles to single binary
- **File-per-database**: Each app gets `{name}.sqlite` — readable by any SQLite tool even when the daemon is off
- **Embedded GUI**: `go:embed` bundles React assets into the binary
- **Separate ports**: gRPC (:50051) for apps, HTTP (:9147) for GUI + REST

## Development

```bash
# Run backend in one terminal
make dev-backend

# Run GUI dev server in another (hot reload, proxied to backend)
make dev-gui

# Run tests
make test

# Regenerate protobuf (after editing sqnsl.proto)
make proto
```

## License

MIT
