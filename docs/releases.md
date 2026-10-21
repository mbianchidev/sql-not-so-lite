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

## CI policy and reuse

Draft pull requests run Go tests/build, mocked release/formula tests, and frontend
installation, lint, and build.
Marking a PR ready runs the full packaging and native-platform matrix; subsequent
pushes to a ready PR also run the full suite. Returning to draft restores the
lightweight checks. Superseded PR runs are canceled, but release runs are not.
`CI result` rejects failed/canceled prerequisites and only accepts intentionally
skipped distribution checks for drafts.

The frontend is built once per workflow revision and passed as a SHA-named
artifact to snapshot packaging and release publication. Consumers download it
outside the checkout, then the shared GUI build hook stages it after GoReleaser's
clean-Git validation. This preserves release validation without rebuilding the
frontend or making the checkout dirty before validation. Local builds use the
same hook to install locked frontend dependencies and build from source.
Production binaries are rebuilt with the stable version rather than publishing
snapshot-versioned binaries.

Cross-compilation uses a separate Go setup-cache fingerprint from the native
Go job, so a native-only cache cannot prevent the cross-target cache from being
saved. Homebrew bundles are cached by runner, architecture, Ruby runtime, and
Gemfile lock hash; installation/style/tests still run on both Mac architectures.
Caches only accelerate recomputation and are not substitutes for successful tests.

Go compilation/test worker counts are bounded using the Linux runner's available
CPU and memory. Target builds run at most two at a time without multiplying
per-target package parallelism. PR matrices fail fast; release matrices collect
all platform results. Docker builds retain their no-cache policy.

## Publishing

The `Release` workflow runs on `v*` tags. Before publishing it runs the same CI
as a pull request, including cross-platform archive execution, GUI/API checks,
and Debian/RPM installation/removal on AMD64 and ARM64. It then requires the
tagged commit to be reachable from `main` and the original tag to still exist
at the expected object. A removed/replaced tag must not be revived by rerunning
an old workflow.

After changes have been reviewed and merged, use a clean checkout of current
`origin/main` and a new stable version:

```bash
./.release.sh 0.0.3  # v0.0.3 is equivalent
```

This is a real publication command, not a dry run. `./.release.sh --help` is
safe and prints usage. The executable shell entry point delegates to
`scripts/release.mjs`, using the same Node.js runtime as the other release tools.

### Prerequisites and version bump

Requires Git, Node.js 22+, and a recent authenticated GitHub CLI (`gh`). The
operator needs source-repository push access, Actions write access for
cancellation, read access to the tap, and GHCR `read:packages` access. Git
authentication and a Git committer identity must already work. The script
does not log in, request new credentials, install tools, or change Git config.
For a classic GitHub CLI token, grant package inventory access separately:

```bash
gh auth refresh --hostname github.com --scopes read:packages
```

The package inventory is required even before the first packaged release.
Listing accessible packages successfully distinguishes an absent package
from a package hidden by insufficient permissions. An authorization/network
failure is not treated as an empty registry.

The script requires a clean checkout whose `HEAD` equals the advertised
`origin/main` commit. It does not switch branches, fetch into local branches,
stash changes, or update `main`. `origin` and any explicit push URL must refer
to this repository. Active Git operations and another local release invocation
are rejected.

Product versions are canonical `vMAJOR.MINOR.PATCH` Git tags. GoReleaser strips
the `v` and injects the version into `server.Version` for the CLI, HTTP, and
gRPC. The script creates an unsigned annotated tag containing a unique
invocation ID and pushes only that tag. No version file or bump commit is
needed; `web/package.json` remains the private frontend package's `0.0.0`.

Both `X.Y.Z` and `vX.Y.Z` are accepted. Leading zeroes, prerelease/build suffixes,
whitespace, and extra arguments are rejected. Numeric components are compared
without floating-point truncation. The version must advance beyond existing
stable Git tags, GitHub releases (including drafts), GHCR tags, and the Homebrew formula. Existing
target archives/images, partial architecture images, historical workflow runs,
and local/remote tags are conflicts, never overwritten.

The script inventories publication state again immediately before creating the
tag. After pushing, it discovers the source repository's `Release` workflow by
its workflow ID, tag, commit SHA, push event, and first run attempt. It never
watches an unrelated latest run. A rerun or multiple matching runs makes
ownership uncertain and stops automatic cleanup. Successful completion also
requires the expected nonempty release assets, canonical GHCR version tag,
and updated Homebrew formula to be visible.

### Rollback and recovery

`SIGINT`, `SIGTERM`, timeouts, and failures enter the same bounded cleanup path.
Before deleting a pushed tag, the script identifies its owned run, requests
cancellation if needed, and waits for terminal state. It then verifies that
**both** publication jobs were skipped and that no target release/image/formula
has appeared. Only then does it compare-and-delete its own remote tag using
an exact-object lease and delete the local tag using Git's expected-old-object
check. These operations never rewrite a branch or delete another writer's tag.

Once GoReleaser or Homebrew publication has started, rollback is **not
automatically complete**. GitHub assets may already be downloaded; GHCR version
and architecture images or `latest` may already exist; the tap may have a new
formula commit. Blind deletion/reversion would risk consumers and concurrent
releases. The script preserves these resources and the tag, exits `2`, and
prints the exact run and recovery journal. It also exits `2` if cancellation,
ownership, API visibility, registration, or cleanup cannot be confirmed.

Recovery journals are stored in the common Git directory under
`sqnsl-releases/vVERSION-INVOCATION.json`. They record the target commit/tag
object, invocation/run identity, cleanup progress, and prior/current release,
GHCR image IDs/digests/tags (including `latest`), and tap commit/formula identity.
They contain no tokens and are not committed. A lock in `sqnsl-release.lock`
serializes local invocations across worktrees.

Do not manually retag, rerun workflows, or edit publication resources while
the script is active. After incomplete recovery, first inspect the recorded
run and confirm publication has stopped. Compare the journal with current
remote state, then choose whether to complete the release or explicitly
restore/remove only invocation-owned resources with suitable permissions.
The script deliberately does not delete GitHub releases or GHCR versions,
reset shared `latest`, or revert/force-push the tap. Those post-publication
actions require an operator's recovery policy, package/tap write access, and
checks for subsequent releases. Independent tap-repository CI is not part of
the source repository's Release run.

`SIGKILL`, a power loss, or a lost machine cannot run cleanup. Inspect the
journal, remote refs, and exact workflow before recovery. A stale lock is
never removed automatically; remove it only after confirming no owner process
is still running. Do not reuse a failed version after rollback: its historical
workflow/artifact identity is retired. Choose a newer version instead.

| Exit | Meaning |
|------|---------|
| `0` | Release workflow and publication verification succeeded |
| `1` | Preflight failed, or failure was fully rolled back before publication |
| `2` | Recovery/cleanup is incomplete or ownership is uncertain |
| `64` | Invalid version or CLI arguments |
| `129`, `130`, `143` | SIGHUP, SIGINT, or SIGTERM with no remaining owned resources |

Waits are bounded and configurable in seconds (positive decimal values, at
most one day):

| Environment variable | Default | Scope |
|----------------------|---------|-------|
| `SQNSL_RELEASE_TIMEOUT_SECONDS` | `7200` | Workflow/publication monitoring after tag push |
| `SQNSL_RELEASE_ROLLBACK_TIMEOUT_SECONDS` | `300` | Entire rollback attempt |
| `SQNSL_RELEASE_COMMAND_TIMEOUT_SECONDS` | `60` | Each Git/gh subprocess, capped by an active deadline |
| `SQNSL_RELEASE_POLL_SECONDS` | `10` | Maximum delay between polls |

Initial workflow registration is additionally bounded to three minutes.
The pre-existing `v0.0.1` and `v0.0.2` source-only releases are never modified.

### Publication credentials

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

If tap publication fails after the release has published and the script has
exited with incomplete recovery, rerun only the failed `Update Homebrew tap`
job. Formula updates are idempotent; do not republish or retag the release.
The tap's CI installs and tests the actual published formula on Intel and ARM
Macs.

## Local verification

With Go, Node.js 22+, GoReleaser 2.18.3, and a running Docker/Buildx environment:

```bash
make test-release
goreleaser check
make release-snapshot
```

Snapshot builds publish nothing. They produce all archives/packages, build
separate local container images for both Linux architectures, verify checksums,
run the native binary and container against isolated fixture directories, and
install/remove native-architecture Debian and RPM packages in disposable
containers. All runtime tests use temporary data, not the user's databases.

`make test-release` needs only Node.js, Bash, and Git. It uses disposable
checkouts/bare remotes and a strict mocked `gh` executable, never live release
resources. It covers normalization, monotonic bumps, conflicts, exact run
selection, cancellation and timeout behavior, partial publication, cleanup
failures, and concurrent tag ownership. These tests run in the lightweight
web CI job on drafts as well as the full ready/tag suite.

On a Mac without an existing Homebrew installation of sql-not-so-lite, run
`bash scripts/test-homebrew.sh dist` to install/test/remove the generated formula
against local snapshot archives in a temporary tap. CI runs this on both Mac
architectures. The check does not start a Homebrew service or change the public
tap.
