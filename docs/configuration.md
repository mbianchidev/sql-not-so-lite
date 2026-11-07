# Configuration

[Documentation index](README.md) | [Installation](installation.md)

## Create and edit the config

Run this once if the file does not already exist:

```bash
sqnsl config init
```

The config file is `~/.sql-not-so-lite/config.toml`. Omitted settings use defaults; environment variables below override file values. Restart the daemon after changes.

The generated file contains absolute paths. **TOML strings do not expand `~` or `$HOME`.** Paths in the reference tables use `~` only as shorthand for the user's home directory, not as values to paste into TOML.

For example, keep the default storage locations but limit scanning to one directory:

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

`sqnsl config` prints the core server, idle, limits, logging, and non-secret Etcqlite settings. Read the TOML file for scanner, replicator, and credential-file settings.

## Server and storage

| Setting | Default | Meaning |
|---------|---------|---------|
| `server.grpc_port` | `50051` | gRPC listener port |
| `server.http_port` | `9147` | GUI and REST listener port |
| `server.data_dir` | `~/.sql-not-so-lite/databases` | Managed `.sqlite` files and internal `catalog.sqlite` |
| `server.bind_address` | `"127.0.0.1"` | Shared HTTP/gRPC bind address; external IPs require an admin token |
| `server.auth_token_file` | `""` | File containing an admin bearer token, at least 32 characters |
| `idle.connection_timeout` | `"5m"` | Close database connections after this idle duration |
| `idle.check_interval` | `"30s"` | Interval between idle checks |
| `limits.max_databases` | `100` | Store-manager limit used when creating managed databases; not a filesystem quota |
| `replicator.replica_dir` | `~/.sql-not-so-lite/replicas` | Current local copies of discovered databases |
| `replicator.snapshot_dir` | `~/.sql-not-so-lite/snapshots` | Stored replication baselines |

## Authentication and network access

HTTP and gRPC now default to **loopback**, not all interfaces. Local operation without an admin token remains supported on a trusted machine. A non-loopback `bind_address` is rejected unless an admin token is configured.

Use `SQNSL_AUTH_TOKEN` for an ephemeral token, or `server.auth_token_file` for a persistent token file. Environment tokens take precedence over the file. Tokens must contain at least 32 characters; use cryptographically random values and protect token files with restrictive permissions. The token value is not saved by `sqnsl config init`/`Save` or printed by `sqnsl config`.

```toml
[server]
bind_address = "0.0.0.0"
auth_token_file = "/absolute/path/to/admin.token"
```

When enabled, admin authentication protects all management/SQL REST endpoints and all gRPC calls, including reflection. The GUI asks for the token and keeps it only in memory. `GET /api/auth` and static GUI assets remain public; the endpoint returns only whether authentication is required.

**Bearer authentication does not provide TLS.** Use HTTPS and gRPC TLS through a trusted terminating proxy and a firewall; do not send tokens over an untrusted plaintext connection. CRUD APIs have independent scoped tokens and table/write opt-ins, described in the [CRUD guide](crud.md).

## Scanner

| Setting | Default | Meaning |
|---------|---------|---------|
| `scanner.scan_root` | Home directory | Recursive discovery root |
| `scanner.file_extensions` | `[".sqlite", ".db", ".sqlite3", ".sqlitedb"]` | Candidate filename extensions |
| `scanner.exclude_patterns` | `["node_modules", ".git/objects", "*.tmp"]` | Discovery exclusions |
| `scanner.scan_interval` | `"30m"` | Time between daemon scans; a scan also runs at startup |
| `scanner.priority_paths_docker` | `~/.orbstack`, `~/.docker`, `/var/lib/docker/volumes` | Highest-priority container-related path prefixes |
| `scanner.priority_paths_workspace` | `~/workspace`, `~/projects` | Workspace path prefixes |
| `scanner.priority_paths_copilot` | `~/.copilot` | Copilot CLI data path prefixes |
| `scanner.priority_paths_app_data` | `~/Library/Application Support`, `~/.config`, `~/.local` | App-data path prefixes |
| `scanner.app_data_dotdir_pattern` | `~/.{repo-name}/data` | Additional app-data directory classification pattern |

Path priorities classify files the process can already see. They do not mount container/VM filesystems or change permissions. See [discovery behavior](usage.md#database-discovery).

## Replication

| Setting | Default | Meaning |
|---------|---------|---------|
| `replicator.enabled` | `true` | Enable the daemon's periodic replication loop |
| `replicator.sync_interval` | `"5s"` | Check active replicas for source-file/WAL changes |
| `replicator.snapshot_retention` | `10` | Stored setting only; automatic pruning is not wired into the running daemon |

Replication starts only for databases selected through the GUI/API or `sqnsl replicate`. Disabling the periodic loop is not a security boundary for the manual replication commands. See [replication and recovery](usage.md#replication-and-recovery).

Use positive Go durations such as `"15m"` or `"2h"` for scan intervals. Invalid or nonpositive scan/replication intervals produce a warning and fall back to 30 minutes/5 seconds. Unparseable idle-duration strings fall back to the idle defaults.

## Optional Etcqlite inspection

Etcqlite integration is **disabled by default**. It connects to one explicitly configured, already-running inspector endpoint. Enabling it does not install/start Etcqlite, scan its storage, contact its endpoint before inspection consent, or enable replication.

Configure a workspace admin token **even on loopback**, plus a separate Etcqlite etcd token or TLS client certificate:

```toml
[server]
bind_address = "127.0.0.1"
auth_token_file = "/absolute/private/workspace-admin.token"

[etcqlite]
enabled = true
endpoint = "http://127.0.0.1:2379"
auth_token_file = "/absolute/private/etcd.token"
```

Keep token files and client private keys owner-only (`chmod 600`). `SQNSL_ETCQLITE_TOKEN` overrides the Etcqlite token file; the raw token is never saved or printed. Workspace clients use the normal admin **Bearer** token. Upstream requests use the **raw opaque etcd token**, without a Bearer prefix.

| Setting | Default | Meaning |
|---------|---------|---------|
| `etcqlite.enabled` | `false` | Show the optional, consent-gated inspector connection |
| `etcqlite.endpoint` | `"http://127.0.0.1:2379"` | HTTP(S) origin only, without credentials, paths, queries or fragments |
| `etcqlite.auth_token_file` | `""` | Owner-only file containing an etcd token |
| `etcqlite.tls_ca_file` | `""` | PEM CA bundle; otherwise use system trust |
| `etcqlite.tls_cert_file` | `""` | PEM TLS client certificate |
| `etcqlite.tls_key_file` | `""` | Owner-only PEM client private key |

**Non-loopback Etcqlite traffic requires HTTPS and mTLS**, not just a token or caller-identity headers. Configure both client certificate and key; certificate verification cannot be disabled. Redirects and environment HTTP proxies are not used.

```toml
[etcqlite]
enabled = true
endpoint = "https://etcd-inspector.example:2379"
tls_ca_file = "/absolute/private/ca.pem"
tls_cert_file = "/absolute/private/client.pem"
tls_key_file = "/absolute/private/client.key"
```

After explicit metadata consent, discovery checks can report source revision lag or a disconnected endpoint. Projection refreshes are manual. Files go under `server.data_dir/inspections` in a mode-`0700` directory, with mode-`0400` projections opened read-only/immutable. This private subdirectory is not a managed database, replica or recovery-snapshot directory.

Raw keys/values require a separate explicit consent on each sensitive projection fetch. Metadata refresh, catalog removal, shutdown and restart revoke that process-local permission and remove the sensitive projection. Startup also cleans abandoned projection files after a crash. Limits are 64 MiB per projection and 1 MiB of query cell payload. See the [inspection workflow and safety boundaries](usage.md#optional-etcqlite-inspection).

## Environment overrides

| Variable | Effect |
|----------|--------|
| `SQNSL_DATA_DIR` | Replace only the managed database/catalog directory. Does not relocate replicas, snapshots, config, or PID files. |
| `SQNSL_SCAN_ROOT` | Replace the recursive scan root and reset the scanner's path-priority defaults under that root. |
| `SQNSL_BIND_ADDRESS` | Override the shared HTTP/gRPC bind IP address. |
| `SQNSL_AUTH_TOKEN` | Supply the admin bearer token without storing it in TOML. Empty or weak values are rejected. |
| `SQNSL_ETCQLITE_TOKEN` | Supply the raw upstream etcd token, overriding its token file. Ignored while the integration is disabled; empty values are rejected when enabled. |

Use absolute paths for these overrides. Container images set `HOME=/data` and keep configuration and storage under the persistent `/data` volume; see [containers](releases.md#containers).

## Settings not enforced at runtime

The generated config also includes these values:

| Setting | Default | Current limitation |
|---------|---------|--------------------|
| `limits.max_query_size` | `10485760` | Not used as a configurable request-size limit |
| `limits.max_result_rows` | `100000` | Not wired to the query helper; it uses its own fixed limit |
| `logging.level` | `"info"` | Not used to filter foreground log output |
| `logging.file` | `~/.sql-not-so-lite/sqnsl.log` | Does not redirect the standard logger |

Foreground logs go to stderr. Service definitions control their own output destinations. Do not rely on these stored settings for request-size protection or log management, or on `snapshot_retention` for automatic cleanup. See [query-result limits](api.md#query-result-limitations) for the actual behavior.
