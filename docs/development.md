# Developer guide

[Documentation index](README.md) | [Installation](installation.md) | [Release operations](releases.md#publishing)

## Setup

Use Git, Make, Go **1.27.3+**, and Node.js **26.12.1** with npm, as pinned in [`.node-version`](../.node-version). CI and the Docker GUI builder use this same stable Node.js version. The minimum source-build requirement remains **22.13+ within 22.x, or 24+**. The application combines Go, pure-Go SQLite, gRPC/Protobuf, and a React/TypeScript/Vite frontend.

From a clone of the repository:

```bash
make all
mkdir -p "$HOME/sqnsl-scan"
```

The GUI build hook uses `npm ci` against `web/package-lock.json`; the production output goes directly to `internal/server/static` for `go:embed`. The frontend postinstall hook adapts the lint tooling to its supported TypeScript runtime.

Monaco's SQL editor, grammar, and worker are bundled locally and loaded when needed. No editor CDN is required. The `minimatch` override keeps the lint dependency tree on a patched release; run lint after changing that override.

## Hot reload

Run the backend in one terminal:

```bash
SQNSL_SCAN_ROOT="$HOME/sqnsl-scan" make dev-backend
```

In another terminal:

```bash
make dev-gui
```

Vite normally serves [http://localhost:5173](http://localhost:5173) and proxies `/api` to port 9147. If you change the backend HTTP port, update [`web/vite.config.ts`](../web/vite.config.ts) accordingly.

The daemon scans its configured root at startup. Use an isolated directory with synthetic databases rather than your whole home directory during development. Foreground logs go to stderr; `/api/health` and `/api/stats` help distinguish startup/listener issues from GUI behavior.

## Checks

```bash
# Go tests
make test

# Frontend lint and complete production build
npm --prefix web run lint
make all

# Mocked browser workflows, including desktop/mobile navigation and local SQL editing
(cd web && npx playwright install chromium && npm run build && npm run test:e2e)

# Release automation with mocked GitHub and disposable repositories
make test-release
```

Release tests use synthetic tokens and real pseudo-terminals; they additionally need **Python 3.9+** with its standard-library `pty` module on macOS or Linux. Publishing a release does not require Python.

Use focused Go package tests while iterating, and run the relevant build before submitting changes. There is no repository-wide format command: Go changes should use `gofmt`, while frontend changes follow the existing TypeScript/ESLint conventions.

Browser tests serve the production build on port 4178, intercept every API request with synthetic fixtures, and block Monaco's former CDN. Build first; the tests do not use the running daemon or application databases. Linux may need `npx playwright install --with-deps chromium` to install browser system libraries. CI runs these workflows in the web job and retains failure traces for three days.

CRUD/authentication tests exercise real disposable SQLite fixtures through the HTTP router and gRPC's in-memory transport. They check native JSON, parameterized filters, single-row rollback, scoped-token rotation/revocation, source write opt-ins, and management/reflection authentication. GUI sign-in and exposure flows use synthetic tokens; tokens are never written into browser storage.

Request-editor browser tests verify actual method/URL/header/body dispatch, exact large integer preservation, 204 and error responses, same-origin credential boundaries, cancellation, response-size limits, and responsive layouts. The sender uses a separate fetch path so a bad scoped token is shown as an HTTP response rather than signing out the administrator.

Etcqlite tests use synthetic HTTP producers and disposable SQLite files. They cover raw-token/mTLS boundaries, identity ETags, strict projection schemas, binary KV consent/revocation, protected native/renamed files, stale/disconnected/missing state, cache isolation and generic-service behavior. Browser fixtures cover the same consent controls through the existing inspector. Focused checks: `go test ./internal/etcqlite ./internal/server ./internal/catalog ./internal/config` and `(cd web && npm run test:e2e -- tests/etcqlite.spec.ts)` after the GUI build. Do not run Go compilation concurrently with Vite's build: Vite replaces the directory consumed by `go:embed`.

To verify a separately installed Etcqlite producer, run `SQNSL_ETCQLITE_BINARY=/absolute/path/to/etcqlite go test ./internal/etcqlite -run TestInstalledProducerProjectionContract`. This opt-in test initializes only disposable synthetic storage and verifies metadata/data projections and native-store exclusion. It never installs or starts a service.

## Protobuf changes

Install `protoc`, `protoc-gen-go`, and `protoc-gen-go-grpc` on `PATH` before regenerating:

```bash
make proto
```

The contract and generated Go files live under [`api/proto/`](../api/proto/). Regenerate after changing `sqnsl.proto`, and keep client/server field use consistent with the contract.

## Architecture

The daemon connects application clients, the embedded web UI, filesystem discovery, and local database copies:

```text
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

Paths are schematic: `catalog.sqlite` shares `server.data_dir` with managed databases, defaulting to `~/.sql-not-so-lite/databases/`. Replicas and snapshots use their separately configured directories. See [storage configuration](configuration.md#server-and-storage).

### Components

| Component | Responsibility |
|-----------|----------------|
| [`cmd/sqnsl`](../cmd/sqnsl/) | Cobra commands and native user-service integration |
| [`internal/config`](../internal/config/) | TOML defaults, loading, and environment overrides |
| [`internal/daemon`](../internal/daemon/) | Lifecycle, signals, PID files, scan and replication loops |
| [`internal/server`](../internal/server/) | gRPC, HTTP/REST, read-only discovery inspection, and embedded GUI |
| [`internal/service`](../internal/service/) | Managed SQL operations and supported schema mutations |
| [`internal/store`](../internal/store/) | File-per-database connections and idle resource release |
| [`internal/idle`](../internal/idle/) | Idle-connection checks |
| [`internal/catalog`](../internal/catalog/) | Persistent discovery, schema, snapshot, and replication metadata |
| [`internal/scanner`](../internal/scanner/) | Filesystem discovery, priorities, and repository heuristics |
| [`internal/etcqlite`](../internal/etcqlite/) | Authenticated opt-in discovery/projection client, immutable validation, and protected-store schema classification |
| [`internal/replicator`](../internal/replicator/) | Baseline snapshots and table-level differential sync |
| [`internal/replicator/wal`](../internal/replicator/wal/) | WAL parser helpers, not the active synchronization path |
| [`internal/schema`](../internal/schema/) | Schema extraction, hashing, and diff helpers |
| [`web`](../web/) | React UI, API client, and Vite build |
| [`deploy`](../deploy/) | Docker, launchd, and systemd definitions |
| [`scripts`](../scripts/) | GUI build, release automation, package/formula generation, and checks |

Managed databases remain standard SQLite files even when the daemon is stopped. GUI assets are embedded in the Go binary rather than served by a separate Node process. The gRPC and GUI/REST listeners use separate ports.

The optional Etcqlite path is `authenticated inspector -> private immutable projection -> existing catalog/read-only REST inspector`. Projection files live under `server.data_dir/inspections`, outside the manager's flat database list and scanner results. No daemon startup, internal SQL access, native snapshot request, or generic replication/recovery is part of that path. No additional Go/frontend dependencies are required.

Do not confuse schema/diff helpers with continuous runtime schema history, or WAL parsing helpers with streaming replication. [Current behavior and limitations](usage.md#replication-and-recovery) are part of the user-facing contract.

## Contributing

Search [existing issues](https://github.com/mbianchidev/sql-not-so-lite/issues) before reporting a bug or proposing a change. Include the environment, reproduction steps, expected behavior, and relevant logs without secrets or private database contents.

Keep pull requests focused. Follow repository and any more-specific agent instructions, update the affected documentation, and run checks covering the change. Use synthetic fixtures for tests, examples, and screenshots; do not include real customer or personal data.

Contributors remain responsible for all submitted work, including AI-assisted contributions. Review and test changes, and be able to explain their behavior and purpose.

## Releases

[`release.sh`](../release.sh) delegates to the existing release automation; it is a real publication command, not a local test. Use `./release.sh --help` for safe usage inspection.

Read the [release guide](releases.md#publishing) for clean-checkout requirements, automatic fast-forward updates of `main`, version selection, hidden one-off credentials, safe retries of unpublished versions, exact-run monitoring, and recovery. Documentation checks do not require creating tags, publishing releases, pushing images, or changing saved authentication.
