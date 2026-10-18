#!/usr/bin/env bash
set -euo pipefail
umask 077

readonly SOURCE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
readonly PHASE=${1:?expected offline or online}
readonly ROOT=${THE8020_INSTANCE_ROOT:?missing instance root}
readonly STATE="$ROOT/node/docker/native-https-upgrade"
readonly ADMIN="$ROOT/node/kernel/bin/admin"

case "$PHASE" in
  offline)
    entrypoint=$(command -v https-entrypoint)
    prefix=$(cd -- "$(dirname -- "$entrypoint")/.." && pwd -P)
    [[ -x "$prefix/bin/caddy" ]] || { echo 'Bundled Caddy is missing; refusing an ambiguous conversion.' >&2; exit 1; }
    temporary=$(mktemp -d)
    trap 'rm -rf -- "$temporary"' EXIT
    # Validate supported input before changing the working deployment.
    "$SOURCE/https-migrate" "${THE8020_HTTPS_CONFIG:-/etc/the8020/https.toml}" \
      "${THE8020_HTTPS_DIR:-/var/lib/the8020/https}" "$temporary/state.json"
    "$SOURCE/https-migrate" --prepare-update "$ROOT"
    printf '%s\n' "$prefix" > "$STATE/prefix"
    ;;
  online)
    [[ -f "$STATE/prefix" ]] || exit 0
    prefix=$(cat "$STATE/prefix")
    if [[ ! -f "$STATE/prepared" ]]; then
      # These are the minimum package versions used by native HTTPS and its UI.
      packages=(db:0.9.1 system:0.7.2 services:0.7.3 auth:0.7.2 packages:0.9.0 secrets:0.9.0 jobs:0.9.0 users:0.9.0 uui:0.9.0 admin-core:0.8.2 https:0.9.0)
      updates=()
      for item in "${packages[@]}"; do
        name=${item%:*}; version=${item#*:}; path="$ROOT/packages/the8020/$name"
        if [[ -d "$path" && -n $(find "$path" -mindepth 1 -maxdepth 1 -print -quit) ]]; then
          if [[ -n $(git -C "$path" status --porcelain) ]]; then
            echo "Save local changes in the8020/$name before retrying the update." >&2
            exit 1
          fi
          current=$(git -C "$path" describe --tags --exact-match 2>/dev/null || true)
          [[ "$current" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "the8020/$name has an untagged version; select a supported release before retrying." >&2; exit 1; }
          if [[ $(printf '%s\n' "$current" "$version" | sort -V | tail -1) == "$current" ]]; then
            if "$ADMIN" --root "$ROOT" --json kernel.packages.inspect "the8020/$name" |
                "$ROOT/node/kernel/runtime/images/rootless/rootfs/usr/bin/deno" eval --quiet --no-config '
                  const response = JSON.parse(await new Response(Deno.stdin.readable).text());
                  const entry = response.result?.package;
                  Deno.exit(response.success && entry?.state === "ready" && entry.active_commit === Deno.args[0] ? 0 : 1);
                ' "$(git -C "$path" rev-parse HEAD)"; then continue; fi
            [[ "$current" == "$version" ]] || { echo "Finish activating the newer the8020/$name release before retrying." >&2; exit 1; }
          fi
        fi
        updates+=("$item")
      done
      if [[ ! -f "$STATE/packages.before.json" ]]; then
        "$ADMIN" --root "$ROOT" --json kernel.packages.list > "$STATE/packages.before.json.tmp"
        mv -- "$STATE/packages.before.json.tmp" "$STATE/packages.before.json"
      fi
      for item in "${updates[@]}"; do
        name=${item%:*}; version=${item#*:}
        echo "HTTPS update: installing the8020/$name $version"
        if ! result=$("$ADMIN" --root "$ROOT" kernel.packages.set the8020 "$name" \
            --source "https://github.com/the8020/$name.git" --tag "$version"); then
          printf '%s\n' "$result" >&2
          exit 1
        fi
        "$ADMIN" --root "$ROOT" --json kernel.packages.synchronize --packages "the8020/$name" |
          "$ROOT/node/kernel/runtime/images/rootless/rootfs/usr/bin/deno" eval --quiet --no-config '
            const response = JSON.parse(await new Response(Deno.stdin.readable).text());
            if (!response.success || response.result?.packages?.length !== 1 || !response.result.packages[0].success) {
              throw new Error("Package update failed: " + JSON.stringify(response));
            }
          '
      done
      if ! result=$("$ADMIN" --root "$ROOT" https.configuration); then
        printf '%s\n' "$result" >&2
        exit 1
      fi
      touch "$STATE/prepared"
    fi
    roots=("$ROOT")
    if [[ -f "$ROOT-prod/kernel.toml" ]]; then roots+=("$ROOT-prod");
    elif [[ "$ROOT" == *-prod && -f "${ROOT%-prod}/kernel.toml" ]]; then roots+=("${ROOT%-prod}"); fi
    for root in "${roots[@]}"; do
      if [[ ! -f "$root/node/docker/native-https-upgrade/prepared" ]]; then
        echo "HTTPS update: update the paired instance $root to 0.9.1 before restarting the container."
        exit 0
      fi
    done
    artifact="$prefix/share/the8020/https"
    mkdir -p "$artifact"
    if [[ ! -f "$STATE/https-entrypoint.old" ]]; then cp -pL "$prefix/bin/https-entrypoint" "$STATE/https-entrypoint.old"; fi
    for name in https-migrate https-entrypoint; do
      install -m 0755 "$SOURCE/$name" "$artifact/$name.new"
      mv -f -- "$artifact/$name.new" "$artifact/$name"
      ln -sfn "$artifact/$name" "$prefix/bin/$name"
    done
    install -m 0755 "$SOURCE/dev-prod.sh" "$prefix/bin/dev-prod.sh.new"
    mv -f -- "$prefix/bin/dev-prod.sh.new" "$prefix/bin/dev-prod.sh"
    for name in go-LICENSE go-toml-LICENSE; do install -m 0644 "$SOURCE/$name" "$artifact/$name"; done
    rm -f -- "$prefix/bin/caddy"
    echo 'HTTPS update: prepared. Restart the container once to stop Caddy, import its certificates, and transfer its ports to the kernel.'
    ;;
  *) echo 'Expected offline or online' >&2; exit 2 ;;
esac
