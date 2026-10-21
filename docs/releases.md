# Installation and releases

## Release contents

Each `vMAJOR.MINOR.PATCH` tag publishes these files to GitHub Releases:

| Platform | Architectures | Artifacts |
|----------|---------------|-----------|
| macOS | AMD64, ARM64 | `sql-not-so-lite_VERSION_darwin_ARCH.tar.gz` |
| Linux | AMD64, ARM64 | `sql-not-so-lite_VERSION_linux_ARCH.tar.gz`, `.deb`, `.rpm` |

Every binary contains the built web GUI and a release version shared by
`sqnsl version`, the HTTP health endpoint, and gRPC `Ping`. The archives also
contain the README and MIT license. `checksums.txt` covers all eight artifacts.

The Homebrew formula is published to
[mbianchidev/homebrew-tap](https://github.com/mbianchidev/homebrew-tap) after a
stable release. It selects a verified archive for the current OS and architecture.
The tap is bootstrapped separately; its formula becomes available with the first
packaged stable release. Prereleases publish versioned artifacts and container
images, but do not change the formula or the container's `latest` tag.

## Portable archives

Download the archive for your OS and architecture and `checksums.txt` from the
same release. For example:

```bash
VERSION=0.0.3
ARCHIVE="sql-not-so-lite_${VERSION}_darwin_arm64.tar.gz"
gh release download "v$VERSION" --repo mbianchidev/sql-not-so-lite \
  --pattern "$ARCHIVE" --pattern checksums.txt
grep "  $ARCHIVE$" checksums.txt | shasum -a 256 -c -
tar -xzf "$ARCHIVE"
mkdir -p "$HOME/.local/bin"
install -m 755 sqnsl "$HOME/.local/bin/sqnsl"
sqnsl version
```

Add `$HOME/.local/bin` to `PATH`. On Linux,
`sha256sum -c` can be used instead of `shasum -a 256 -c`.

## Services and upgrades

Homebrew users should manage the daemon with `brew services start`, `stop`, or
`restart mbianchidev/tap/sql-not-so-lite`. Run this as your normal user, not with
`sudo`. Do not also run `sqnsl install`, which would create a second service.

Debian and RPM packages install `/usr/bin/sqnsl` and the user unit
`/usr/lib/systemd/user/sqnsl.service`. They do not enable a system-wide service
or start the daemon during installation. Enable it as your normal user:

```bash
systemctl --user daemon-reload
systemctl --user enable --now sqnsl
```

After upgrading a package, restart the user service. Package removal removes
the executable and packaged unit, but does not remove `~/.sql-not-so-lite` or
any databases. Stop/disable the service before removing the package:

```bash
systemctl --user disable --now sqnsl
```

For archive/source installations, `sqnsl install` uses the installed binary's
actual path. The normal per-user configuration and data paths remain unchanged.

## Containers

The GHCR image supports `linux/amd64` and `linux/arm64`. Stable tags include
the version without `v` (for example, `:0.0.3`) and `:latest`.

```bash
docker run --rm \
  -p 127.0.0.1:9147:9147 -p 127.0.0.1:50051:50051 \
  -v sqnsl-data:/data \
  -v "$HOME/example-databases:/scan:ro" \
  -e SQNSL_SCAN_ROOT=/scan \
  ghcr.io/mbianchidev/sql-not-so-lite:0.0.3
```

The native binary is preferable for full desktop discovery. Containers only
see mounted paths. Read-only source mounts support discovery, inspection, and
replication, but in-place restore requires a writable mount.

`HOME=/data` keeps configuration, PID files, logs, replicas, and snapshots in
the persistent volume. Managed databases and the catalog also use `/data`.
The default scan root is `/data`; override `SQNSL_SCAN_ROOT` for source mounts.
An image upgrade must reuse the same data volume.

The release Dockerfile copies the already verified GoReleaser binaries. Its
only build-time command runs on the builder's native architecture, so creating
the multiarch image does not require installing QEMU on the host.

## Publishing

The `Release` workflow runs on `v*` tags. Before publishing it runs the same CI
as a pull request, including cross-platform archive execution, GUI/API checks,
and Debian/RPM installation/removal on AMD64 and ARM64. It then requires the
tagged commit to be reachable from `main`.

After the packaging PR has been reviewed and merged, publish a new tag:

```bash
git fetch origin main --tags
git tag --no-sign -a v0.0.3 origin/main -m "Release v0.0.3"
git push origin v0.0.3
```

Choose a new version rather than moving an existing tag. The pre-existing
`v0.0.1` and `v0.0.2` releases are source-only and are not modified.

The repository's `GITHUB_TOKEN` publishes release assets and GHCR images with
`contents: write` and `packages: write`. `HOMEBREW_TAP_SSH_KEY` is a separate
write-enabled SSH deploy key restricted to `mbianchidev/homebrew-tap`; it is
already configured as a source-repository Actions secret. It cannot write to
other repositories. Stable releases check this secret before publishing.

GitHub initially makes a newly published container package private, even for a
public source repository. After the first successful release, change the
`sql-not-so-lite` container package's visibility to **Public** in GitHub package
settings to allow anonymous pulls. This is a one-time GitHub setting, not a
container build option.

Formula generation consumes the published release's checksums, not placeholder
hashes. GoReleaser's deprecated `brews` integration is deliberately not used:
the tap uses a formula so the daemon works with `brew services`. Formula updates
commit directly to the tap's `main` using its scoped deploy key.

If tap publication fails after the release has published, rerun only the failed
`Update Homebrew tap` job. Formula updates are idempotent; do not republish or
retag the release. The tap's CI installs and tests the actual published formula
on Intel and ARM Macs.

## Local verification

With Go, Node.js 22+, GoReleaser 2.18.3, and a running Docker/Buildx environment:

```bash
node --test scripts/homebrew.test.mjs
goreleaser check
make release-snapshot
```

Snapshot builds publish nothing. They produce all archives/packages, build
separate local container images for both Linux architectures, verify checksums,
run the native binary and container against isolated fixture directories, and
install/remove native-architecture Debian and RPM packages in disposable
containers. All runtime tests use temporary data, not the user's databases.

On a Mac without an existing Homebrew installation of sql-not-so-lite, run
`bash scripts/test-homebrew.sh dist` to install/test/remove the generated formula
against local snapshot archives in a temporary tap. CI runs this on both Mac
architectures. The check does not start a Homebrew service or change the public
tap.
