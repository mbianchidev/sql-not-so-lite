# Usage

[Documentation index](README.md) | [Configuration](configuration.md) | [API reference](api.md)

## Managed and discovered databases

Managed databases are writable files created under `server.data_dir`, one `{name}.sqlite` file per database. Discovered databases stay at their original source paths; the discovery inspector opens them read-only. Discovery does not import, move, or grant write access to those sources.

Local replicas and stored snapshots are separate files. Neither discovery nor replication replaces an independent backup.

## Web GUI

Start the daemon and open [http://localhost:9147](http://localhost:9147), or run `sqnsl gui` to open its configured URL.

1. Use **+** beside **Managed databases** to create a database, or search the sidebar and select an existing one.
2. Open **Schema**, then **New table**, to create a table and define its fields in order.
3. Open **Browse** to inspect rows or insert records with explicit values, NULLs, or column defaults.
4. Use **SQL Editor** for SQL with Monaco syntax highlighting and Ctrl+Enter/Cmd+Enter execution.
5. Sort query results or export the returned rows as CSV or JSON.

The workspace starts in **dark mode**, regardless of the operating system's theme. The header toggle saves your explicit light/dark preference in your browser and applies it to the SQL editors too. The footer shows the daemon's connection state and runtime statistics.

The sidebar shows active/idle databases and expands the selected database's tables. On smaller screens, open it with the menu button; Escape closes it. Workspace tabs support arrow-key navigation.

The schema editor can add fields and edit supported field names, types, nullability, and defaults. Editing rebuilds the table in one transaction while preserving column order, data, indexes, and triggers. Schemas with constraints the editor cannot safely reconstruct are rejected rather than silently weakened.

The **Discovery** menu supports directory scans and search by name, path, repository, status, and database metadata. Star files and use **Favorites** to narrow the catalog. Select a file to open its read-only schema/table/SELECT inspector, then use **Back to databases** to return. Replication and stored snapshot/schema-history actions remain available.

The SQL editor and its worker are bundled with the application; editor startup does not require a third-party CDN.

Use **Expose API** in a managed database header or a discovered file's inspector to create a table-scoped JSON CRUD API. APIs default to read-only; **Allow writes** explicitly enables mutation, including mutation of discovered original files. Copy the one-time token and example request, or rotate/revoke access later. The inspector itself stays read-only. See the [CRUD guide](crud.md).

Choose **Test API** to open the built-in request editor. Select a CRUD method, edit the URL, headers, and JSON body, then **Send** to see HTTP status, timing, headers, and the raw response. Scoped tokens are used by default; admin credentials require an explicit selection. **Cancel** stops waiting but cannot roll back an already-applied write. See [in-UI requests and limits](crud.md#send-requests-inside-the-ui).

## CLI reference

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

Run `sqnsl <command> --help` for flags. The GUI and APIs require the daemon. Discovery/replication CLI commands also access local files and the catalog directly; use the same home/config as the daemon.

## Database discovery

The scanner walks the full home directory by default, including hidden app-data locations such as `~/Library/Application Support`, `~/.config`, and `~/.local`. Set `scanner.scan_root` or `SQNSL_SCAN_ROOT` to narrow the scope.

Files must match a configured extension and the SQLite magic header (`SQLite format 3\000`). Encrypted database formats are not supported by this discovery path. The catalog records path, size, modification time, priority, repository association, favorite state, and availability.

The daemon scans immediately at startup and every 30 minutes by default. GUI/API scans update metadata and mark missing paths without deleting catalog history or favorites. For an explicit CLI scan:

```bash
sqnsl scan /absolute/path/to/databases
sqnsl discovered
```

### Priority tiers

| Priority | Default location patterns |
|----------|---------------------------|
| 1: Docker/OrbStack | `~/.docker/`, `~/.orbstack/`, `/var/lib/docker/volumes/` |
| 2: Workspace | `~/workspace/`, `~/projects/` |
| 3: Copilot CLI | `~/.copilot/` |
| 4: App data | `~/Library/Application Support/`, `~/.config/`, `~/.local/`, configured app dot-directories |
| 5: Other | Remaining visible files under the scan root |

These priorities do not expose files inside unmounted containers or VMs. Native discovery sees only files accessible to the current user.

### Repository associations

For each discovered file, the scanner walks up the directory tree looking for `.git/config`. It parses a GitHub remote into an `owner/repo` association stored in the catalog. This is a local filesystem heuristic, not a GitHub API lookup.

## Optional Etcqlite inspection

Configure the [disabled-by-default integration](configuration.md#optional-etcqlite-inspection), sign into the workspace, then open **Discovery**. Check **I consent to metadata-only read-only inspection** and choose **Inspect Etcqlite metadata**. The source appears in the existing discovered catalog and read-only inspector; no internal database path is exposed.

The default projection contains `projection_metadata`, `key_metadata` and `lease_metadata`: byte lengths, revisions, versions, leases and operational identity fields. It does not contain raw user keys/values, auth users/password hashes/tokens, protocol commands or peer credentials. Cluster/member IDs and revisions retain their full precision. A compact revision of `-1` means no compaction has occurred.

The inspector shows the **captured revision and fetch time**, separately from source revision at the last discovery check. Checks run every 15 seconds while the Discovery view is open after consent; they do not refresh projection rows. **Stale**, **Disconnected**, **Missing projection** and **Inspection disabled** are not live-data or consensus-health claims. Cached metadata can still be browsed while Etcqlite is stopped; missing projections are not silently recreated, and source failures do not stop the standalone SQLite daemon.

Choose **Refresh metadata** to generate a new metadata-only capture. For raw user KV records, separately check **I consent to loading sensitive raw keys and values** and choose **Load sensitive keys and values**. The optional `key_values` table represents BLOBs as lossless `hex:` strings, including binary keys and values. User values can contain Kubernetes or application secrets; their display and exports remain sensitive. Refreshing metadata clears displayed results and revokes raw access even if the refresh fails. Restarting/shutting down the daemon or removing the catalog entry also removes sensitive cached files.

**Not a backup:** these immutable projections cannot restore Etcqlite. Generic managed writes, CRUD exposure, table replication, snapshot recovery and schema history are unavailable for them. Native `node.db` stores and native recovery images are also rejected by schema-only classification, including renamed or manually cataloged files. Ordinary SQLite discovery and CRUD behavior is unchanged.

Sensitive disk-backed bbolt interchange artifacts are protected too. Their `interchange_meta` marker has `kind='bbolt-interchange'`, `format=2` and a `reference` such as `3.7.3`; `interchange_buckets` and `interchange_entries` hold full binary source records with a SHA-256 footer. These are staging artifacts, not inspector projections or generic database/recovery sources. Classification reads only schema and the safe marker, never bucket/entry rows.

Discovery uses the authenticated Etcqlite format-1 contract. Projection requests pin its exact identity ETag; an identity change is not silently followed. Remove the old catalog entry and explicitly reconnect to accept a different cluster/member. The integration never requests the full sensitive native snapshot endpoint, and never uses table polling for consensus, HA or recovery.

## Replication and recovery

Replication is **local, table-level polling**, not streaming WAL replication, remote replication, or high availability.

1. Select a discovered database in the GUI, or run `sqnsl replicate <name>`.
2. The initial sync uses `VACUUM INTO` to create a consistent baseline under `replica_dir` and a stored snapshot.
3. The running daemon checks source-file/WAL modification times every `sync_interval` (default 5 seconds).
4. When a change is detected, it compares table definitions and row sets, replaces changed tables transactionally, and removes tables deleted from the source.

Each cycle reads one consistent source transaction. Replica writes are serialized within the process. Comparing full row sets can be expensive for large databases.

Active replicas resume after a daemon restart. `sqnsl replicate stop <name>` pauses a replica; selecting replication again or running `sqnsl replicate <name>` resumes it without recreating baseline metadata when the replica still exists.

Initial and rebuilt baselines produce stored snapshots. **Ordinary differential cycles do not create snapshots**, and configured snapshot retention is not automatically enforced.

### Schema versioning

Schema SQL and hashes are recorded when a replication baseline is created, starting at version 0 (`v0`). A later baseline rebuild can record a new schema version.

The GUI, REST endpoints, `sqnsl versions <name>`, and `sqnsl transitions <name>` expose stored history. **Continuous schema-change tracking is not implemented:** scans and ordinary differential syncs do not record new schema versions or transitions, and runtime code does not currently populate transition history.

### Snapshot recovery

Recovery is limited to available snapshots, not arbitrary timestamps or each replication tick. Pause replication and prefer a new target file:

```bash
sqnsl replicate stop mydb
sqnsl restore mydb --to ./mydb-restored.sqlite
sqnsl restore mydb -v 3 --to ./mydb-v3.sqlite
```

**CLI restore overwrites the original source unless `--to` is supplied. GUI/REST restore overwrites the local replica instead.**

Stop applications using the target before any in-place restore, and retain an independent backup. Restore copies directly into the target and removes WAL/SHM sidecars; it is not an atomic rename. Resuming replication can replace a restored replica with the current source data.

Query results and GUI exports also have [representation and pagination limits](api.md#query-result-limitations); they are not full database backups.
