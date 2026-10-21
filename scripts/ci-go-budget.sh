#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_ENV:?Run this script inside GitHub Actions.}"
cpus=$(nproc)
memory_kb=$(awk '/^MemAvailable:/ {print $2}' /proc/meminfo)
if ! [[ "$cpus" =~ ^[1-9][0-9]*$ && "$memory_kb" =~ ^[1-9][0-9]*$ ]]; then
  echo "Cannot determine the runner's available CPU and memory." >&2
  exit 1
fi

workers=2
if ((cpus < 2 || memory_kb < 4194304)); then
  workers=1
  echo "::warning::Runner resources require a single Go worker (${cpus} CPUs, ${memory_kb} KiB available)."
fi
threads=$((cpus / workers))
printf 'CI_WORKERS=%s\nGOMAXPROCS=%s\n' "$workers" "$threads" >> "$GITHUB_ENV"
echo "Go budget: ${workers} workers, ${threads} CPU threads each (${cpus} CPUs, ${memory_kb} KiB available)."
