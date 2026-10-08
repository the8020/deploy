#!/usr/bin/env bash
set -euo pipefail

readonly SOURCE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
readonly VERSION=2.11.6
if [[ ${1:-install} == build ]]; then
  kernel_development=/usr/local/src/the8020/.development
  go_command=$kernel_development/toolchains/go/bin/go
  if [[ ! -x "$go_command" ]]; then go_command=$(command -v go); fi
  export GOCACHE=$kernel_development/cache/go-build
  export GOMODCACHE=$kernel_development/cache/go-mod
  export CGO_ENABLED=0 GOWORK=off
  cd "$SOURCE"
  artifact=/usr/local/share/the8020/https
  install -d "$artifact"
  "$go_command" build -mod=readonly -trimpath -ldflags='-s -w' -o "$artifact/https-entrypoint" .
  module_directory=$("$go_command" list -m -f '{{.Dir}}' github.com/pelletier/go-toml/v2)
  go_root=$("$go_command" env GOROOT)
  install -m 0644 "$module_directory/LICENSE" "$artifact/go-toml-LICENSE"
  install -m 0644 "$go_root/LICENSE" "$artifact/go-LICENSE"
  install -m 0644 "$SOURCE/https.toml" "$SOURCE/bootstrap.sh" "$artifact/"
  exit 0
fi
if [[ ${1:-install} != install ]]; then echo 'Expected build or install' >&2; exit 1; fi
case $(dpkg --print-architecture) in
  amd64) architecture=amd64 ;;
  arm64) architecture=arm64 ;;
  *) echo 'HTTPS bootstrap supports amd64 and arm64' >&2; exit 1 ;;
esac

temporary=$(mktemp -d)
trap 'rm -rf -- "$temporary"' EXIT
archive="caddy_${VERSION}_linux_${architecture}.tar.gz"
base="https://github.com/caddyserver/caddy/releases/download/v$VERSION"
curl --fail --silent --show-error --location --retry 3 --max-time 120 \
  --output "$temporary/$archive" "$base/$archive"
curl --fail --silent --show-error --location --retry 3 --max-time 30 \
  --output "$temporary/checksums" "$base/caddy_${VERSION}_checksums.txt"
(cd "$temporary"; awk -v name="$archive" '$2 == name {print; found=1} END {if (!found) exit 1}' checksums | sha512sum --check --status)
tar -xzf "$temporary/$archive" -C "$temporary" caddy LICENSE
install -m 0755 "$temporary/caddy" /usr/local/bin/caddy
install -d /usr/local/share/doc/caddy /usr/local/share/doc/the8020-https /etc/the8020
install -m 0644 "$temporary/LICENSE" /usr/local/share/doc/caddy/LICENSE
install -m 0644 "$SOURCE/go-toml-LICENSE" "$SOURCE/go-LICENSE" /usr/local/share/doc/the8020-https/
install -m 0644 "$SOURCE/https.toml" /etc/the8020/https.toml
ln -sf "$SOURCE/https-entrypoint" /usr/local/bin/https-entrypoint
/usr/local/bin/caddy version
