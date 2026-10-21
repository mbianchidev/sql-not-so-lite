#!/usr/bin/env bash
set -euo pipefail

dist=$(cd "${1:-dist}" && pwd)
version=$(node -p 'JSON.parse(require("node:fs").readFileSync(process.argv[1], "utf8")).version' "$dist/metadata.json")
case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "Unsupported package-test architecture: $(uname -m)" >&2; exit 1 ;;
esac

for format in deb rpm; do
  if [ "$format" = deb ]; then
    image=debian:stable-slim
  else
    image=fedora:latest
  fi
  docker run --rm --platform "linux/$arch" \
    --mount "type=bind,source=$dist,target=/packages,readonly" \
    --env "VERSION=$version" --env "ARCH=$arch" --env "FORMAT=$format" \
    "$image" sh -eu -c '
      package="/packages/sql-not-so-lite_${VERSION}_linux_${ARCH}.${FORMAT}"
      mkdir -p /root/.sql-not-so-lite
      printf "fixture\n" > /root/.sql-not-so-lite/preserve
      if [ "$FORMAT" = deb ]; then
        dpkg --install "$package"
      else
        rpm --install "$package"
      fi
      test "$(sqnsl version)" = "sql-not-so-lite $VERSION"
      test -f /usr/lib/systemd/user/sqnsl.service
      grep -Fx "ExecStart=/usr/bin/sqnsl start" /usr/lib/systemd/user/sqnsl.service
      test ! -e /etc/systemd/system/sqnsl.service
      mkdir /tmp/expected
      tar -xzf "/packages/sql-not-so-lite_${VERSION}_linux_${ARCH}.tar.gz" -C /tmp/expected
      cmp /usr/bin/sqnsl /tmp/expected/sqnsl
      if [ "$FORMAT" = deb ]; then
        dpkg --remove sql-not-so-lite
      else
        rpm --erase sql-not-so-lite
      fi
      test ! -e /usr/bin/sqnsl
      test ! -e /usr/lib/systemd/user/sqnsl.service
      test "$(cat /root/.sql-not-so-lite/preserve)" = fixture
      echo "$FORMAT installation, binary contents, and removal verified ($ARCH)"
    '
done
