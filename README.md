# sql-not-so-lite

SQLite-as-a-service daemon and local database explorer. sql-not-so-lite manages one SQLite file per application, exposes gRPC and REST APIs, and embeds a React web GUI for working with managed data and inspecting existing SQLite files.

**Managed databases** are created in the daemon's data directory and support writes. **Discovered databases** stay at their original paths and have a separate read-only inspector; discovery does not import or move them. Optional local replicas and stored snapshots provide another way to work with discovered data.

```text
Applications -- gRPC :50051 --> sqnsl -- managed *.sqlite + catalog.sqlite
Browser ------ HTTP  :9147 -->       -- discovery of existing SQLite files
                                    -- local replicas + stored snapshots
```

<img width="1710" height="949" alt="sql-not-so-lite web interface" src="https://github.com/user-attachments/assets/eee13914-f61f-483f-b58c-dd04d950286a" />

## Features

- **Application APIs**: Create, list, and delete managed databases; execute parameterized SQL and query data through gRPC or REST.
- **Web GUI**: Browse and insert rows, create tables, edit supported column definitions, run SQL with Monaco, and export query results as CSV or JSON.
- **Local discovery**: Search configured roots for SQLite files, inspect them read-only, find GitHub repository associations, and preserve favorites and missing-file state across restarts.
- **Local replication**: Create consistent baseline copies, periodically synchronize changed tables, resume active replicas after restart, and restore stored snapshots.
- **Single-binary deployment**: Pure-Go SQLite needs no CGO or separate database server. The built GUI is embedded, and idle database connections are closed automatically.

## Requirements

- **Native platforms**: macOS and Linux, AMD64 or ARM64. Service integration uses launchd on macOS and per-user systemd on Linux. Windows is not a supported build/release target.
- **Source builds**: Git, Make, Go **1.27.3+**, and Node.js with npm. Use **Node.js 22.13+ within 22.x, or 24+** to satisfy the frontend build and lint tool requirements.
- **Optional tools**: Docker with Compose for containers; `grpcurl` for the gRPC examples; `protoc`, `protoc-gen-go`, and `protoc-gen-go-grpc` on `PATH` only when regenerating protobuf code.

> **Trusted environments only:** HTTP and gRPC listen on all network interfaces, not just localhost, and have no built-in authentication or TLS. Restrict access with a firewall or an authenticated proxy. Run as your normal user, not root; the default discovery root is your entire home directory.

## Quick start

### Build and run from source

The existing `v0.0.1` and `v0.0.2` releases are source-only. Building the current default branch is the available installation path until a packaged release is published.

```bash
git clone https://github.com/mbianchidev/sql-not-so-lite.git
cd sql-not-so-lite
make all

# Use a small, explicit discovery root for the first run.
mkdir -p "$HOME/sqnsl-scan"
SQNSL_SCAN_ROOT="$HOME/sqnsl-scan" ./sqnsl start
```

`make all` installs the locked frontend dependencies, builds the GUI, then compiles `./sqnsl`. `make build` or `go build` alone on a clean checkout embeds only the placeholder page.

Leave the daemon running and open [http://localhost:9147](http://localhost:9147). In another terminal, verify the API:

```bash
curl -fsS http://localhost:9147/api/health
# {"status":"ok","version":"dev"}
```

Stop the foreground daemon with **Ctrl+C**. Without `SQNSL_SCAN_ROOT`, startup immediately scans `$HOME` recursively and repeats every 30 minutes. The override above applies only to that process; configure a persistent scan root before enabling a service.

### Install the built binary and optional user service

```bash
mkdir -p "$HOME/.local/bin"
install -m 755 sqnsl "$HOME/.local/bin/sqnsl"
export PATH="$HOME/.local/bin:$PATH"
sqnsl version
```

Add that directory to your shell's `PATH` permanently if needed. Use a stable binary location before installing a service:

```bash
sqnsl config init   # Once, if the config file does not already exist
# Edit ~/.sql-not-so-lite/config.toml before enabling discovery.
sqnsl install       # Normal user: launchd or per-user systemd
# Linux additionally needs:
systemctl --user start sqnsl
```

On macOS, the launchd agent loads during installation and starts on login. Use `sqnsl uninstall` to remove this integration. Do not run a foreground daemon and a service on the same ports.

### Packaged releases, when published

Release automation is configured for macOS/Linux `.tar.gz` archives, Linux `.deb`/`.rpm` packages, Homebrew, and multiarch GHCR images. These are distribution targets, not assets in the existing source-only releases. Check [GitHub Releases](https://github.com/mbianchidev/sql-not-so-lite/releases) for actual assets before following package instructions.

Once the Homebrew formula is published:

```bash
brew install mbianchidev/tap/sql-not-so-lite
brew services start mbianchidev/tap/sql-not-so-lite
```

Built release binaries include the GUI and need neither Go nor Node.js at runtime. See [installation, checksums, packages, and upgrades](docs/releases.md). Use only one service manager: Homebrew services, a packaged Linux user unit, or `sqnsl install`.

### Docker from source

```bash
make docker-build  # No-cache build; default image tag is dev
docker run --rm -p 127.0.0.1:9147:9147 -p 127.0.0.1:50051:50051 \
  -v sqnsl-data:/data sql-not-so-lite:dev
```

Alternatively, `make docker-up` builds without cache and starts Compose in the background; `make docker-down` stops it. **The checked-in Compose file publishes both ports on all host interfaces**, unlike the localhost-only `docker run` example above.

Containers see only mounted files. Use the native binary for desktop-wide discovery, or mount a source directory and set `SQNSL_SCAN_ROOT` to its container path. A read-only source mount cannot support in-place source restoration.

Configuration, catalog, managed databases, replicas, and snapshots live under `/data`; retain the volume across upgrades. [Container documentation](docs/releases.md#containers) covers mounts and GHCR tags to use after an image has been published.

## CLI Reference

```text
sqnsl start [-d]                 Start daemon (-d for background)
sqnsl stop                       Signal the running daemon to stop
sqnsl status                     Show daemon status
sqnsl list                       List database files in the data directory
sqnsl gui                        Open the running daemon's GUI in a browser
sqnsl install                    Install a per-user service
sqnsl uninstall                  Remove that service
sqnsl config                     Show core configuration
sqnsl config init                Create the default config file
sqnsl version                    Print version
sqnsl scan [path...]             Scan for SQLite databases
sqnsl discovered                 List discovered databases
sqnsl replicate <name>           Start or resume local replication
sqnsl replicate stop <name>      Pause replication
sqnsl restore <name>             Restore latest snapshot to original source
sqnsl restore <name> -v N        Restore a specific snapshot to source
sqnsl restore <name> --to <path>  Restore a snapshot to an alternate file
sqnsl versions <name>            List stored schema versions
sqnsl transitions <name>         List stored schema transitions
```

Use `sqnsl <command> --help` for flags. The GUI and APIs need a running daemon; the discovery/replication CLI commands also access local files and the catalog directly.

## Configuration

Run `sqnsl config init` once to write `~/.sql-not-so-lite/config.toml`, then edit it and restart the daemon. Omitted settings use defaults. The generated file contains absolute paths; **TOML path strings do not expand `~` or `$HOME`**.

For example, these overrides limit discovery to a chosen directory while keeping the other storage defaults:

```toml
[server]
grpc_port = 50051
http_port = 9147

[idle]
connection_timeout = "5m"
check_interval = "30s"

[limits]
max_databases = 100

[scanner]
scan_root = "/absolute/path/to/databases"
file_extensions = [".sqlite", ".db", ".sqlite3", ".sqlitedb"]
exclude_patterns = ["node_modules", ".git/objects", "*.tmp"]
scan_interval = "30m"

[replicator]
enabled = true
sync_interval = "5s"
```

Default storage is under `~/.sql-not-so-lite/`: `databases/` holds managed files and `catalog.sqlite`, `replicas/` holds current local copies, and `snapshots/` holds stored baselines. Set `server.data_dir`, `replicator.replica_dir`, and `replicator.snapshot_dir` to absolute paths to relocate them.

Environment variables override file settings when present:

| Variable | Description |
|----------|-------------|
| `SQNSL_DATA_DIR` | Catalog and managed database directory only; does not relocate replicas, snapshots, or config |
| `SQNSL_SCAN_ROOT` | Recursive scan root; also resets discovery's path-priority defaults |

Foreground logs go to stderr. The generated `logging.level`/`logging.file`, `limits.max_query_size`/`limits.max_result_rows`, and `replicator.snapshot_retention` settings are not currently wired to runtime enforcement. Do not rely on them for logging, request-size protection, or automatic snapshot pruning.

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

Create a managed database and run a query without a gRPC client:

```bash
curl -fsS http://localhost:9147/api/databases \
  -H 'Content-Type: application/json' -d '{"name":"demo"}'
curl -fsS http://localhost:9147/api/databases/demo/query \
  -H 'Content-Type: application/json' -d '{"sql":"SELECT 1 AS ready"}'
```

The query response contains `"Rows":[["1"]]`. Select `demo` in the GUI to create tables or run further SQL. Repeating the create request with the same name returns an error rather than replacing the file.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/databases` | List all databases |
| POST | `/api/databases` | Create database `{"name":"..."}` |
| GET | `/api/databases/:name` | Get database info |
| DELETE | `/api/databases/:name` | Drop database |
| GET | `/api/databases/:name/schema` | Full schema |
| GET | `/api/databases/:name/tables` | List table names |
| POST | `/api/databases/:name/tables` | Create a table with its initial fields |
| GET | `/api/databases/:name/tables/:table?limit=N&offset=N` | Table data |
| POST | `/api/databases/:name/tables/:table/columns` | Add a field to an existing table |
| PUT | `/api/databases/:name/tables/:table/columns` | Edit a field's name, type, nullability, and default |
| POST | `/api/databases/:name/tables/:table/rows` | Insert a row with explicit values, NULLs, or field defaults |
| POST | `/api/databases/:name/query` | Execute SQL `{"sql":"..."}` |
| GET | `/api/health` | Health check |
| GET | `/api/stats` | Memory, connections, uptime |
| GET | `/api/scan` | Current scan status |
| POST | `/api/scan` | Scan the default root or `{"paths":["/absolute/path"]}` |
| GET | `/api/discovered` | List discovered databases |
| GET | `/api/discovered/:id` | Get discovered DB details |
| PATCH | `/api/discovered/:id` | Set favorite state `{"favorite":true}` |
| GET | `/api/discovered/:id/schema` | Inspect the current source schema read-only |
| GET | `/api/discovered/:id/table?name=TABLE&limit=N&offset=N` | Preview source table rows read-only |
| POST | `/api/discovered/:id/query` | Run one read-only SELECT query `{"sql":"SELECT ..."}` |
| POST | `/api/discovered/:id/replicate` | Start replication |
| DELETE | `/api/discovered/:id/replicate` | Stop replication |
| POST | `/api/discovered/:id/restore` | Restore a snapshot to the local replica `{"version":N}` |
| GET | `/api/discovered/:id/snapshots` | List snapshots |
| GET | `/api/discovered/:id/versions` | Schema versions |
| GET | `/api/discovered/:id/transitions` | Schema transitions |

## Web GUI

Access at `http://localhost:9147` when the daemon is running.

- **Sidebar**: Lists all databases with active/idle status
- **Table Browser**: Browse rows and add records with explicit values, NULLs, or field defaults
- **Schema Viewer**: Create tables, reorder fields before creation, and edit existing field names, types, nullability, and defaults
- **SQL Editor**: Monaco-based with syntax highlighting and Ctrl+Enter execution
- **Results**: Sortable table with CSV/JSON export
- **Dark/Light theme**: Toggle in header
- **Discovered databases**: Dedicated menu with search by name, path, repository, status, and database metadata
- **Read-only inspector**: Explore discovered schemas, fields, indexes, table rows, and SELECT results without opening the source for writes
- **Replication status**: Live indicators for replication state per database
- **Schema timeline**: View stored schema versions and transition records; see the current tracking limits below
- **Snapshot restore**: Restore an available snapshot to the local replica, not the original source

Field edits rebuild the table in one transaction while preserving column order, data, indexes, and triggers. The editor rejects schemas with constraints it cannot safely reconstruct instead of silently dropping them.

## Database Discovery & Replication

### Scanning

The scanner walks `$HOME` by default, including hidden app-data locations such as `~/Library/Application Support`, `~/.config`, and `~/.local`. Set `scanner.scan_root` or `SQNSL_SCAN_ROOT` to narrow the scope. Files must match a configured extension and have the SQLite magic header (`SQLite format 3\000`); encrypted database formats are not supported by this discovery path.

The catalog records path, size, modification time, priority tier, repository association, favorite state, and availability. The daemon scans at startup and every 30 minutes by default. Set `scanner.scan_interval` to a positive Go duration such as `"15m"` or `"2h"`. GUI/API scans update metadata and mark missing files without deleting their history or favorites.

Use the GUI's Discovered menu, `POST /api/scan`, or the CLI for an explicit scan:

```bash
sqnsl scan /absolute/path/to/databases
sqnsl discovered
```

### Priority tiers

Discovered databases are classified by location to surface the most relevant ones first:

| Priority | Location pattern | Examples |
|----------|-----------------|----------|
| 1: Docker/OrbStack | `~/.docker/`, `~/.orbstack/` | Visible container-related SQLite files |
| 2: Workspace | `~/workspace/`, `~/projects/` | Active development databases |
| 3: Copilot CLI | `~/.copilot/` | Copilot session databases |
| 4: App Data | `~/Library/Application Support/`, `~/.config/`, `~/.local/` | Application state databases |
| 5: Other | Everything else under the scan root | Miscellaneous databases |

These priorities classify visible files; they do not grant access to databases inside an unmounted container or VM.

### GitHub repo detection

For each discovered database, the scanner walks up the directory tree looking for a `.git/config` file. If found, it parses the remote URL to extract the GitHub repository (e.g., `owner/repo`) and stores the association in the catalog. This lets you see which project each database belongs to.

### Replication

Replication creates and maintains a copy of a discovered database under `replica_dir`:

1. **Initial sync**: `sqnsl replicate <name>` or the GUI uses `VACUUM INTO` to create a consistent baseline copy of the source database.
2. **Differential sync**: The running daemon checks for source-file/WAL changes every `sync_interval` (default 5 seconds), compares table definitions and rows, then transactionally replaces changed tables and removes tables deleted from the source.
3. **Automatic resume**: Databases left in the replicating state resume when the daemon starts again. Paused or failed replicas can be resumed without recreating version-one metadata.
4. **Snapshots**: Initial baselines and rebuilt baselines are versioned in `snapshot_dir`. Ordinary differential cycles do not create snapshots, and configured retention is not automatically enforced.

Each differential cycle reads the source through one consistent transaction and serializes replica writes within its process. This is local, table-level polling, not streaming WAL replication, remote replication, or high availability. Comparing row sets can be expensive for large databases.

### Schema versioning

Schema SQL and hashes are recorded when a replication baseline is created, starting at version 0 (`v0`). A later baseline rebuild can record a new schema version.

`sqnsl versions <name>`, `sqnsl transitions <name>`, the REST endpoints, and the GUI expose stored history. **Continuous schema-change tracking is not implemented:** scans and ordinary differential syncs do not record new schema versions or transitions, and the runtime does not currently populate transition history.

### Snapshot recovery

Recovery is limited to stored snapshots, not arbitrary timestamps or every replication tick. Pause replication before restoring, and prefer a new file first:

```bash
sqnsl replicate stop mydb
sqnsl restore mydb --to ./mydb-restored.sqlite
sqnsl restore mydb -v 3 --to ./mydb-v3.sqlite
```

**CLI restore overwrites the original source unless `--to` is supplied. GUI/REST restore overwrites the local replica instead.** Stop applications using the target before any in-place restore and keep an independent backup. Restore copies directly into the target and removes its WAL/SHM sidecars; it is not an atomic rename. Resuming replication can overwrite a restored replica with current source data.

### Query result limitations

Query cells are returned as strings, with SQL `NULL` represented by the string `"NULL"`. The query helper defaults to 1,000 rows and caps results at 100,000 independently of the TOML limit setting. `total_count` in gRPC (`TotalCount` in REST JSON) counts rows returned in that page, not all matching rows. Use explicit SQL for counts and do not treat GUI exports as full database backups.

## Architecture

```text
sql-not-so-lite/
├── cmd/sqnsl/           # CLI entrypoint (cobra)
├── internal/
│   ├── catalog/         # Internal meta-database (catalog.sqlite)
│   ├── scanner/         # Filesystem SQLite discovery
│   ├── replicator/      # Baseline snapshots and table-level differential sync
│   │   └── wal/         # WAL parser helpers, not the running sync path
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
├── docs/                # Installation and release operations
├── scripts/             # Release generation and installation checks
├── .goreleaser.yaml     # Cross-platform release artifacts
└── Makefile
```

**Key design decisions:**

- **Pure Go SQLite** (`modernc.org/sqlite`): No CGO, cross-compiles to single binary
- **File-per-database**: Each app gets `{name}.sqlite`, readable by other SQLite tools even when the daemon is off
- **Embedded GUI**: `go:embed` bundles React assets into the binary
- **Separate ports**: gRPC (:50051) for apps, HTTP (:9147) for GUI + REST

## Development

Run `make all` once to prepare the frontend dependencies and embedded assets. For hot reload, use separate terminals:

```bash
# Run backend in one terminal
SQNSL_SCAN_ROOT="$HOME/sqnsl-scan" make dev-backend

# Run GUI dev server in another (hot reload, proxied to backend)
make dev-gui
```

Vite normally serves [http://localhost:5173](http://localhost:5173) and proxies `/api` to port 9147. If you change the backend HTTP port, update [`web/vite.config.ts`](web/vite.config.ts) accordingly.

```bash
# Go tests
make test

# Frontend lint and complete production build
npm --prefix web run lint
make all

# Test release automation without publishing anything
make test-release

# Regenerate protobuf after editing sqnsl.proto (requires compiler/plugins)
make proto
```

## Publishing a release

Maintainers can use [`./release.sh`](release.sh) with a new `X.Y.Z` or `vX.Y.Z` version from a clean checkout of current `origin/main`. This publishes a real annotated tag and follows the Release workflow; it does not edit the private frontend's version or push a commit to `main`. `./release.sh --help` is safe to inspect without publishing.

If GitHub authentication is missing or lacks access, an interactive terminal prompts for a hidden token used only for this invocation, without changing the saved `gh` login.

Read the [release prerequisites, rollback, and recovery procedure](docs/releases.md#publishing) first. Failures before publication can remove invocation-owned tags; once publication may have started, resources are retained for explicit recovery.

## Documentation and contributing

- [Installation, containers, services, and release operations](docs/releases.md)
- [gRPC service definitions and message types](api/proto/sqnsl.proto)
- [Build and development targets](Makefile)

Report bugs or propose improvements through [issues](https://github.com/mbianchidev/sql-not-so-lite/issues) and [pull requests](https://github.com/mbianchidev/sql-not-so-lite/pulls). Include reproduction steps and relevant checks; use synthetic databases for examples and tests, not private application data.

## License

MIT. See [LICENSE](LICENSE).
