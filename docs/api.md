# API reference

[Documentation index](README.md) | [Usage](usage.md) | [Protobuf contract](../api/proto/sqnsl.proto)

The daemon must be running. The default endpoints are gRPC at `localhost:50051` and HTTP at `http://localhost:9147`, bound to loopback. External binds require an admin bearer token. See [authentication and TLS requirements](configuration.md#authentication-and-network-access).

When admin authentication is enabled, add `Authorization: Bearer TOKEN` to REST requests and gRPC metadata. For example, use `curl -H "Authorization: Bearer $SQNSL_AUTH_TOKEN" ...` or `grpcurl -H "Authorization: Bearer $SQNSL_AUTH_TOKEN" ...`. Scoped CRUD tokens do not authorize management, raw SQL, or gRPC.

## gRPC

The service is `sqnsl.v1.SqlNotSoLite`. Reflection is enabled, so `grpcurl` does not need a local proto file for these examples.

| RPC | Purpose |
|-----|---------|
| `CreateDatabase(name)` | Create a managed `.sqlite` database |
| `ListDatabases()` | List managed databases and metadata |
| `DropDatabase(name)` | Delete a managed database and its file |
| `GetDatabaseInfo(name)` | Inspect a managed database |
| `Execute(database, sql, params)` | Run statements such as INSERT, UPDATE, DELETE, or DDL |
| `Query(database, sql, params, limit, offset)` | Query managed data with result pagination |
| `GetSchema(database)` | Inspect tables, columns, and indexes |
| `Ping()` | Read version, uptime, active database count, and memory statistics |

These are summaries, not literal client method signatures. Consult the [message definitions](../api/proto/sqnsl.proto) for request/response fields and generated-client types.

```bash
grpcurl -plaintext localhost:50051 sqnsl.v1.SqlNotSoLite/ListDatabases

grpcurl -plaintext -d '{"name":"myapp"}' \
  localhost:50051 sqnsl.v1.SqlNotSoLite/CreateDatabase

grpcurl -plaintext \
  -d '{"database":"myapp","sql":"CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)"}' \
  localhost:50051 sqnsl.v1.SqlNotSoLite/Execute

grpcurl -plaintext \
  -d '{"database":"myapp","sql":"INSERT INTO items (name) VALUES (?)","params":["demo"]}' \
  localhost:50051 sqnsl.v1.SqlNotSoLite/Execute

grpcurl -plaintext \
  -d '{"database":"myapp","sql":"SELECT * FROM items","limit":10}' \
  localhost:50051 sqnsl.v1.SqlNotSoLite/Query
```

Discovery, replication, and recovery are exposed by the CLI/REST interface, not these gRPC methods.

## REST first use

Create a managed database, then run SQL:

```bash
curl -fsS http://localhost:9147/api/databases \
  -H 'Content-Type: application/json' -d '{"name":"demo"}'

curl -fsS http://localhost:9147/api/databases/demo/query \
  -H 'Content-Type: application/json' -d '{"sql":"SELECT 1 AS ready"}'
```

The query response contains `"Rows":[["1"]]`. Select `demo` in the GUI for further work. Repeating creation with the same name returns an error rather than replacing the file.

Managed query requests accept `sql`, optional string `params`, and optional `limit`/`offset`. Statements beginning with SELECT, PRAGMA, EXPLAIN, or WITH use the query path; other statements use the execution path.

## REST endpoints

### Schema-driven CRUD

See the [CRUD guide](crud.md) for registration, scoped tokens, typed JSON, filters, and write safety.

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/data/:alias` | Read allowed table schemas and keys |
| GET | `/api/data/:alias/:table` | Filter, sort, project, and paginate JSON rows |
| POST | `/api/data/:alias/:table` | Create one row |
| PATCH | `/api/data/:alias/:table?FIELD=eq.VALUE` | Update exactly one matching row |
| DELETE | `/api/data/:alias/:table?FIELD=eq.VALUE` | Delete exactly one matching row |
| GET, POST | `/api/exposures` | List/create scoped database APIs |
| GET, PUT, DELETE | `/api/exposures/:alias` | Inspect/update/revoke an API |
| POST | `/api/exposures/:alias/token` | Rotate its token |

The existing query endpoints retain their original response format. CRUD endpoints use native JSON objects instead of the string-based query format below.

### Managed databases

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/databases` | List managed databases |
| POST | `/api/databases` | Create a database with `{"name":"..."}` |
| GET | `/api/databases/:name` | Get database information |
| DELETE | `/api/databases/:name` | Delete a database and its file |
| GET | `/api/databases/:name/schema` | Read the schema |
| GET | `/api/databases/:name/tables` | List tables |
| POST | `/api/databases/:name/tables` | Create a table with its initial fields |
| GET | `/api/databases/:name/tables/:table?limit=N&offset=N` | Read table rows |
| POST | `/api/databases/:name/tables/:table/columns` | Add a field |
| PUT | `/api/databases/:name/tables/:table/columns` | Edit a supported field definition |
| POST | `/api/databases/:name/tables/:table/rows` | Insert a row using values, NULLs, or defaults |
| POST | `/api/databases/:name/query` | Execute SQL with `{"sql":"..."}` |

### Discovery, replication, and snapshots

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/scan` | Get current scan status |
| POST | `/api/scan` | Scan the default root or `{"paths":["/absolute/path"]}` |
| GET | `/api/discovered` | List discovered databases |
| GET | `/api/discovered/:id` | Read discovered database details |
| PATCH | `/api/discovered/:id` | Set favorite state with `{"favorite":true}` |
| GET | `/api/discovered/:id/schema` | Inspect source schema read-only |
| GET | `/api/discovered/:id/table?name=TABLE&limit=N&offset=N` | Preview source rows read-only |
| POST | `/api/discovered/:id/query` | Run one read-only SELECT with `{"sql":"SELECT ..."}` |
| POST | `/api/discovered/:id/replicate` | Start or resume local replication |
| DELETE | `/api/discovered/:id/replicate` | Pause replication |
| POST | `/api/discovered/:id/restore` | Restore a snapshot to the local replica; optionally `{"version":N}` |
| GET | `/api/discovered/:id/snapshots` | List stored snapshots |
| GET | `/api/discovered/:id/versions` | List stored schema versions |
| GET | `/api/discovered/:id/transitions` | List stored schema transitions |

REST restore targets the replica, unlike the CLI's default source restore. Read [recovery precautions](usage.md#snapshot-recovery) before using either path.

### Optional Etcqlite projections

These routes require workspace admin authentication, including on loopback, and the [explicitly enabled integration](configuration.md#optional-etcqlite-inspection). Scoped CRUD tokens cannot authorize them.

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/etcqlite` | Read configuration/consent state; after consent, check authenticated discovery and report cached projection freshness |
| POST | `/api/etcqlite/projections` | Fetch a new projection with explicit inspection consent; returns the existing discovered-database response |
| DELETE | `/api/discovered/:id` | Remove the catalog entry, its private projection, and sensitive-data permission; never changes Etcqlite |

Initial metadata-only inspection:

```bash
curl -fsS http://localhost:9147/api/etcqlite/projections \
  -H "Authorization: Bearer $SQNSL_AUTH_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"ack_read_only_inspection":true}'
```

Sensitive user KV inspection requires **both** flags:

```bash
curl -fsS http://localhost:9147/api/etcqlite/projections \
  -H "Authorization: Bearer $SQNSL_AUTH_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"ack_read_only_inspection":true,"include_data":true,"ack_sensitive_data":true}'
```

Either sensitive flag alone is rejected, not treated as permission. Repeating a metadata-only request revokes sensitive access before attempting the upstream fetch. Creation returns `201`; replacement preserves the catalog ID/favorites and returns `200`.

Inspect the returned `id` through the existing `/api/discovered/:id/schema`, `/table` and `/query` routes. Projection responses include `kind:"etcqlite"` and an `inspection` object with decimal-string `cluster_id`, `member_id`, `revision`, `compact_revision` and `source_revision`, schema/engine/API/build metadata, timestamps, `includes_data` and `state`. `source_path` is the configured endpoint, not a private artifact or internal store path. Query cells remain strings; projection BLOBs use `hex:` encoding. Projections are limited to 64 MiB, query cell payload to 1 MiB, and SELECT previews to 1,000 rows with a 10-second query timeout.

`GET /api/etcqlite` does not contact the endpoint before consent. After consent, source failures return status `200` with truthful `state:"disconnected"` and an error, while cached metadata remains separately inspectable. It does not report the standalone daemon as failed. Projection fetches return `400` for invalid/partial consent, `401` for missing workspace authentication, `404` when disabled, `412` for a changed identity, `502` for an unsafe/incompatible artifact, or `503` for an unavailable endpoint, credentials or cache.

Upstream discovery is authenticated `GET /etcqlite/v1/discovery`; projection creation is `POST /etcqlite/v1/projections` with its exact ETag in `If-Match`. Upstream errors are not retried or displayed as response payloads. Native snapshots are never requested. Projections and native Etcqlite schemas cannot be exposed through CRUD, opened writable, replicated, or restored as generic SQLite; protected discovery operations return `403`.

### Health and statistics

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/health` | Version and health status |
| GET | `/api/stats` | Version, uptime, memory, active connections, and goroutine count |
| GET | `/api/auth` | Public boolean indicating whether admin authentication is required |

## Query result limitations

- Query cells are strings. SQL `NULL` is represented by the string `"NULL"`, not a typed null value.
- The query helper defaults to 1,000 rows and caps results at 100,000 independently of `limits.max_result_rows`.
- gRPC `total_count` and REST `TotalCount` count rows returned in the current page, not all matching rows. Use explicit SQL for a complete count.
- GUI exports contain the returned result set, not a full database backup.

Do not rely on the stored `limits.max_query_size` setting as a request-size control; see [inactive configuration settings](configuration.md#settings-not-enforced-at-runtime).
