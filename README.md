# sql-not-so-lite

Lightweight SQLite-as-a-service daemon. Manages multiple SQLite databases as files, provides a gRPC API for applications and a web GUI for debugging.

```
┌──────────┐  gRPC   ┌─────────────────┐    ┌────────────────┐
│  App A   │────────▶│                 │───▶│ app_a.sqlite   │
└──────────┘         │ sql-not-so-lite │    ├────────────────┤
┌──────────┐  gRPC   │    daemon       │───▶│ app_b.sqlite   │
│  App B   │────────▶│                 │    └────────────────┘
└──────────┘         │ :50051 (gRPC)   │    ~/.sql-not-so-lite/
                     │ :8080  (HTTP)   │         databases/
┌──────────┐  HTTP   │                 │
│  Web GUI │────────▶│                 │
└──────────┘         └─────────────────┘
```

## Features

- **Multi-database**: Each app creates its own `.sqlite` file
- **gRPC API**: Typed, high-performance API for applications
- **Web GUI**: Browse tables, view schemas, run SQL queries with Monaco editor
- **Idle management**: Closes idle connections automatically, stays dormant when unused (~5-10MB RSS)
- **Single binary**: GUI embedded via `go:embed` — one file to run
- **Cross-platform**: macOS (launchd), Linux (systemd), Docker

## Quick Start

### Build from source

```bash
# Prerequisites: Go 1.21+, Node.js 18+, protoc (for proto changes)
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
# GUI at http://localhost:8080
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
```

## Configuration

Config file: `~/.sql-not-so-lite/config.toml`

```toml
[server]
grpc_port = 50051
http_port = 8080
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

## Web GUI

Access at `http://localhost:8080` when the daemon is running.

- **Sidebar**: Lists all databases with active/idle status
- **Table Browser**: Click a database → see tables → click to browse rows
- **Schema Viewer**: Column types, constraints, indexes, foreign keys
- **SQL Editor**: Monaco-based with syntax highlighting and Ctrl+Enter execution
- **Results**: Sortable table with CSV/JSON export
- **Dark/Light theme**: Toggle in header

## Architecture

```
sql-not-so-lite/
├── cmd/sqnsl/           # CLI entrypoint (cobra)
├── internal/
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
- **Separate ports**: gRPC (:50051) for apps, HTTP (:8080) for GUI + REST

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
