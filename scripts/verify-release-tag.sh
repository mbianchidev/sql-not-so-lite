#!/usr/bin/env bash
set -euo pipefail

: "${RELEASE_REF:?Set RELEASE_REF to the release tag ref.}"
if ! git merge-base --is-ancestor HEAD origin/main; then
  echo "::error::The tagged commit is not reachable from origin/main. Refusing release checks or publication." >&2
  exit 1
fi

expected=$(git rev-parse --verify "$RELEASE_REF")
actual=$(git ls-remote --refs origin "$RELEASE_REF" | awk '{print $1}')
if [ "$actual" != "$expected" ] ||
  [ "$(git rev-parse "$RELEASE_REF^{commit}")" != "$(git rev-parse HEAD)" ]; then
  echo "::error::The release tag was removed or replaced. Refusing release checks or publication." >&2
  exit 1
fi
