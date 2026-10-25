# Installation

[Documentation index](README.md)

## Requirements

Native builds and release targets support **macOS and Linux on AMD64 or ARM64**. Service integration uses launchd on macOS and per-user systemd on Linux. Windows is not a supported build/release target.

Source builds require Git, Make, Go **1.27.3+**, and Node.js with npm. Use **Node.js 26.12.1**, pinned in [`.node-version`](../.node-version), to match CI and the Docker GUI builder. The minimum requirement remains **22.13+ within 22.x, or 24+** to satisfy both frontend build and lint requirements. SQLite uses a pure-Go driver: no CGO or separate database-server installation is required.

Optional tools are documented with their workflows: Docker/Compose for containers, `grpcurl` for [gRPC examples](api.md), and protobuf tooling for [development](development.md).

## Install a release

[Packaged releases](https://github.com/mbianchidev/sql-not-so-lite/releases) start at `v0.0.3`, with macOS/Linux archives and Linux `.deb`/`.rpm` packages. Release binaries include the GUI and need neither Go nor Node.js at runtime.

With [Homebrew](https://brew.sh/) on macOS or Linux:

```bash
brew install mbianchidev/tap/sql-not-so-lite
mkdir -p "$HOME/sqnsl-scan"
SQNSL_SCAN_ROOT="$HOME/sqnsl-scan" sqnsl start
```

Open [http://localhost:9147](http://localhost:9147); `/api/health` returns status `"ok"` and the installed release version. Stop the foreground process with **Ctrl+C**.

Without Homebrew, follow the [portable archive and checksum instructions](releases.md#portable-archives), or download the package for your Linux distribution and architecture. For example, after downloading the matching assets:

```bash
VERSION=0.0.3
sudo apt install "./sql-not-so-lite_${VERSION}_linux_amd64.deb"  # Debian/Ubuntu
sudo dnf install "./sql-not-so-lite_${VERSION}_linux_amd64.rpm"  # Fedora/RHEL
```

Use the command for your distribution and replace `amd64` with `arm64` on ARM machines. Run the daemon as your normal user, not with `sudo`.

## Build from source

To build the current default branch rather than install a release:

```bash
git clone https://github.com/mbianchidev/sql-not-so-lite.git
cd sql-not-so-lite
make all

mkdir -p "$HOME/sqnsl-scan"
SQNSL_SCAN_ROOT="$HOME/sqnsl-scan" ./sqnsl start
```

`make all` runs the locked frontend installation and production build before compiling the Go binary with the GUI embedded. `make build` or `go build` alone on a clean checkout embeds the placeholder page instead.

The daemon stays in the foreground. Open [http://localhost:9147](http://localhost:9147), or check it from another terminal:

```bash
curl -fsS http://localhost:9147/api/health
# {"status":"ok","version":"dev"}
```

Stop it with **Ctrl+C**.

**Run as a normal user on a trusted network.** HTTP and gRPC listen on all interfaces without authentication or TLS. Restrict access with a firewall or an authenticated proxy. Without `SQNSL_SCAN_ROOT`, startup scans the full home directory and repeats every 30 minutes. This environment override applies to that process only; set `scanner.scan_root` in the [config file](configuration.md) for a persistent setting.

## Install the built binary

From the repository root after building:

```bash
mkdir -p "$HOME/.local/bin"
install -m 755 sqnsl "$HOME/.local/bin/sqnsl"
export PATH="$HOME/.local/bin:$PATH"
sqnsl version
```

Add `$HOME/.local/bin` to your shell's `PATH` permanently if needed. Keep the binary in a stable location before installing a service. `make install` is an alternative that rebuilds and copies it to `/usr/local/bin`; that directory must be writable by the installing user.

## Optional user service

Create and edit the config before enabling automatic discovery:

```bash
sqnsl config init   # Once, if the config file does not already exist
# Edit ~/.sql-not-so-lite/config.toml and set an absolute scanner.scan_root.
sqnsl install
```

On macOS, installation loads a launchd agent that starts on login. On Linux, it installs and enables a per-user systemd unit; start it explicitly:

```bash
systemctl --user start sqnsl
```

Do not run a foreground daemon and a service on the same ports. `sqnsl uninstall` removes this service integration, not your database files. Replacing the installed binary requires restarting the service.

Foreground logs go to stderr. The macOS launchd definition redirects output to `~/.sql-not-so-lite/sqnsl.log`; the Linux user unit uses the journal. Changing `logging.file` in TOML does not redirect foreground logging.

## Release services and upgrades

For a Homebrew-managed user service, use this instead of starting the foreground daemon:

```bash
brew services start mbianchidev/tap/sql-not-so-lite
```

Linux packages install `/usr/bin/sqnsl` and a user service without starting a system-wide daemon. Enable the packaged unit as your normal user:

```bash
systemctl --user daemon-reload
systemctl --user enable --now sqnsl
```

Use only one service manager: Homebrew services, the packaged Linux unit, or `sqnsl install`. See [release installation and upgrades](releases.md) for archive checksums, package behavior, and removal.

## Docker from source

```bash
make docker-build  # No-cache build; default image tag is dev
docker run --rm -p 127.0.0.1:9147:9147 -p 127.0.0.1:50051:50051 \
  -v sqnsl-data:/data sql-not-so-lite:dev
```

Alternatively, `make docker-up` performs a no-cache Compose build and starts it in the background; `make docker-down` stops it. **The checked-in Compose file publishes both ports on all host interfaces**, unlike the localhost-only example above.

Containers see only mounted paths. Use the native binary for desktop-wide discovery, or mount a source directory and set `SQNSL_SCAN_ROOT` to its path inside the container. Read-only mounts support discovery and inspection, but not in-place restoration of source files.

Configuration, managed databases, catalog, replicas, and snapshots live under `/data`. Reuse the named volume across upgrades. See [container storage and published-image configuration](releases.md#containers); use GHCR tags only after the corresponding image is published.

## Troubleshooting

| Symptom | Check |
|---------|-------|
| The GUI says it has not been built | Run `make all`, then restart the rebuilt binary. A plain Go build does not build React assets. |
| A server cannot listen on its port | Check for another daemon or service; HTTP defaults to 9147 and gRPC to 50051. Choose other ports in the config if necessary. |
| Discovery is slow or finds unexpected files | Set an explicit absolute scan root instead of scanning all of `$HOME`. Check filesystem permissions; do not work around them by running the daemon as root. |
| A config path creates an unexpected directory | TOML strings do not expand `~` or `$HOME`. Use the absolute paths generated by `sqnsl config init`. |
| A package or image is unavailable | Check the release's actual assets. Source-only releases do not contain binaries, packages, a formula, or a published image. |

For source/archive installations, remove the user service with `sqnsl uninstall` before removing the installed binary. Package removal and service removal do not delete `~/.sql-not-so-lite`, discovered sources, or Docker volumes. Back up data before choosing any separate cleanup.
