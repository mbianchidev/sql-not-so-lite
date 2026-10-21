#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
artifact=${SQNSL_GUI_ARTIFACT_DIR:-}
if [ -n "$artifact" ]; then
  if [ ! -f "$artifact/index.html" ] || [ ! -d "$artifact/assets" ] ||
    ! grep -q 'src="/assets/' "$artifact/index.html"; then
    echo "Incomplete compiled GUI artifact: $artifact" >&2
    exit 1
  fi
  echo "Staging the workflow's compiled GUI from $artifact"
  cp -R "$artifact/." "$root/internal/server/static/"
else
  npm --prefix "$root/web" ci
  npm --prefix "$root/web" run build
fi
