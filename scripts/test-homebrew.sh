#!/usr/bin/env bash
set -euo pipefail

dist=$(cd "${1:-dist}" && pwd)
tap=sqnsl-testing/release
formula="$tap/sql-not-so-lite"
if brew list --formula --versions sql-not-so-lite >/dev/null 2>&1; then
  echo "Cannot run isolated Homebrew installation check: sql-not-so-lite is already installed." >&2
  exit 1
fi

temp=$(mktemp -d "${TMPDIR:-/tmp}/sqnsl-brew.XXXXXX")
tapped=false
installed=false
cleanup() {
  status=$?
  trap - EXIT
  if $installed; then brew uninstall --formula "$formula" || status=1; fi
  if $tapped; then brew untap "$tap" || status=1; fi
  node -e 'require("node:fs").rmSync(process.argv[1], {recursive: true, force: true})' "$temp"
  exit "$status"
}
trap cleanup EXIT
mkdir -p "$temp/tap/Formula"
cp "$dist/sql-not-so-lite.rb" "$temp/tap/Formula/"
git init -q -b main "$temp/tap"
git -C "$temp/tap" add Formula/sql-not-so-lite.rb
git -C "$temp/tap" -c user.name=Fixture -c user.email=fixture@example.invalid \
  commit --no-gpg-sign -qm "Add formula fixture"
HOMEBREW_NO_AUTO_UPDATE=1 brew tap --custom-remote "$tap" "$temp/tap"
tapped=true
HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_DEVELOPER=1 brew style --formula "$formula"
tap_path=$(brew --repository "$tap")
node --input-type=module - "$tap_path/Formula/sql-not-so-lite.rb" "$dist" <<'NODE'
import { readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
const [path, dist] = process.argv.slice(2)
const formula = readFileSync(path, 'utf8').replace(
  /https:\/\/github\.com\/mbianchidev\/sql-not-so-lite\/releases\/download\/[^/]+\/([^"]+)/g,
  (_, filename) => pathToFileURL(join(dist, filename)).href,
)
writeFileSync(path, formula)
NODE
HOMEBREW_NO_AUTO_UPDATE=1 brew install --formula "$formula"
installed=true
HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_DEVELOPER=1 brew test "$formula"
