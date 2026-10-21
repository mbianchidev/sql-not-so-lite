# Documentation

[Project overview](../README.md)

## Using sql-not-so-lite

| Guide | Start here for |
|-------|----------------|
| [Installation](installation.md) | Supported platforms, source builds, user services, Docker, and troubleshooting |
| [Usage](usage.md) | GUI workflows, CLI commands, discovery, local replication, and safe snapshot recovery |
| [Configuration](configuration.md) | TOML settings, defaults, storage paths, environment overrides, and inactive settings |
| [APIs](api.md) | gRPC and REST examples, endpoint reference, and query-result limitations |

The daemon distinguishes writable **managed databases** from read-only inspection of **discovered source files**. Start with a small scan root and use it only on a trusted network; the servers have no built-in authentication or TLS.

## Developing and releasing

- [Developer guide](development.md): setup, hot reload, architecture, checks, and contributing.
- [Installation and release operations](releases.md): configured distribution artifacts, upgrades, CI policy, publication, credentials, retries, and recovery.
- [Protobuf contract](../api/proto/sqnsl.proto): service definitions and message types.
- [Build targets](../Makefile): the commands used by local development and CI.

## Current limits

Local replication is table-level polling, not streaming or remote replication. Snapshots are stored baselines, not a continuous point-in-time recovery log. [Recovery](usage.md#snapshot-recovery), [schema tracking](usage.md#schema-versioning), and [query-result limits](api.md#query-result-limitations) describe the behavior to account for before using real data.
