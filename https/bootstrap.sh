#!/usr/bin/env bash
set -euo pipefail

readonly SOURCE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
if [[ ${1:-install} == build ]]; then
  kernel_source=/usr/local/src/the8020
  if [[ ! -f "$kernel_source/kernel/settings/definitions/network/https_port.toml" ||
        ! -f /8020/packages/the8020/https/cbus/commands/import.toml ]]; then
    echo 'This deployment requires native HTTPS (kernel 0.9 or newer and compatible packages).' >&2
    exit 1
  fi
  kernel_development=$kernel_source/.development
  go_command=$kernel_development/toolchains/go/bin/go
  if [[ ! -x "$go_command" ]]; then go_command=$(command -v go); fi
  export GOCACHE=$kernel_development/cache/go-build
  export GOMODCACHE=$kernel_development/cache/go-mod
  export CGO_ENABLED=0 GOWORK=off
  cd "$SOURCE"
  artifact=/usr/local/share/the8020/https
  install -d "$artifact"
  "$go_command" build -mod=readonly -trimpath -ldflags='-s -w' -o "$artifact/https-migrate" .
  module_directory=$("$go_command" list -m -f '{{.Dir}}' github.com/pelletier/go-toml/v2)
  go_root=$("$go_command" env GOROOT)
  install -m 0644 "$module_directory/LICENSE" "$artifact/go-toml-LICENSE"
  install -m 0644 "$go_root/LICENSE" "$artifact/go-LICENSE"
  install -m 0755 "$SOURCE/entrypoint.sh" "$artifact/https-entrypoint"
  install -m 0644 "$SOURCE/bootstrap.sh" "$artifact/"
  exit 0
fi
if [[ ${1:-install} != install ]]; then echo 'Expected build or install' >&2; exit 1; fi
install -d /usr/local/share/doc/the8020-https
install -m 0644 "$SOURCE/go-toml-LICENSE" "$SOURCE/go-LICENSE" /usr/local/share/doc/the8020-https/
ln -sf "$SOURCE/https-entrypoint" /usr/local/bin/https-entrypoint
ln -sf "$SOURCE/https-migrate" /usr/local/bin/https-migrate
