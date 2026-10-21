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

`sqnsl config` prints the core server, idle, limits, and logging sections. Read the TOML file for scanner and replicator settings.

## Server and storage

| Setting | Default | Meaning |
|---------|---------|---------|
| `server.grpc_port` | `50051` | gRPC listener port |
| `server.http_port` | `9147` | GUI and REST listener port |
| `server.data_dir` | `~/.sql-not-so-lite/databases` | Managed `.sqlite` files and internal `catalog.sqlite` |
| `idle.connection_timeout` | `"5m"` | Close database connections after this idle duration |
| `idle.check_interval` | `"30s"` | Interval between idle checks |
| `limits.max_databases` | `100` | Store-manager limit used when creating managed databases; not a filesystem quota |
| `replicator.replica_dir` | `~/.sql-not-so-lite/replicas` | Current local copies of discovered databases |
| `replicator.snapshot_dir` | `~/.sql-not-so-lite/snapshots` | Stored replication baselines |

HTTP and gRPC bind on all interfaces. There is no bind-address setting, authentication, or TLS setting in this config. Restrict network access externally.

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

## Environment overrides

| Variable | Effect |
|----------|--------|
| `SQNSL_DATA_DIR` | Replace only the managed database/catalog directory. Does not relocate replicas, snapshots, config, or PID files. |
| `SQNSL_SCAN_ROOT` | Replace the recursive scan root and reset the scanner's path-priority defaults under that root. |

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
