#!/usr/bin/env bash
set -euo pipefail

mode=${1:-}
shift || true
case "$mode" in
  single) entrypoint=docker-entrypoint.sh ;;
  dev-prod) entrypoint=dev-prod.sh ;;
  *) echo 'Expected single or dev-prod' >&2; exit 2 ;;
esac
if (( $# > 0 )) && [[ "$*" != serve ]]; then exec "$@"; fi

export THE8020_NETWORK_HTTPS_PORT=${THE8020_NETWORK_HTTPS_PORT:-443}
export THE8020_NETWORK_ADDITIONAL_HTTP_PORT=${THE8020_NETWORK_ADDITIONAL_HTTP_PORT:-9080}
roots=("${THE8020_INSTANCE_ROOT:-/8020}")
if [[ "$mode" == dev-prod ]]; then roots+=("${roots[0]}-prod"); fi
pending=false
for root in "${roots[@]}"; do
  if [[ ! -f "$root/packages/the8020/https/cbus/commands/import.toml" ]]; then
    echo "Native HTTPS packages are missing in $root. Update the retained instance's packages before changing deployment images." >&2
    exit 1
  fi
  if [[ -L "$root/node/kernel/current" ]]; then
    "$root/node/kernel/runtime/images/rootless/rootfs/usr/bin/deno" eval --quiet --no-config '
      const release = JSON.parse(await Deno.readTextFile(Deno.args[0]));
      const version = /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/.exec(release.version);
      if (!version || Number(version[1]) === 0 && Number(version[2]) < 9) {
        throw new Error("Update the retained kernel to 0.9 or newer before changing deployment images.");
      }
    ' "$root/node/kernel/current/release.json"
  fi
  if [[ ! -f "$root/node/docker/native-https.done" ]]; then pending=true; fi
done

if [[ "$pending" == true ]]; then
  temporary=$(mktemp -d)
  child=""
  stop_preparation() {
    if [[ -n "$child" ]]; then
      kill -TERM "$child" 2>/dev/null || true
      wait "$child" 2>/dev/null || true
      child=""
    fi
  }
  finish() {
    stop_preparation
    rm -rf -- "$temporary"
  }
  trap finish EXIT
  trap 'exit 143' TERM HUP
  trap 'exit 130' INT
  https-migrate "${THE8020_HTTPS_CONFIG:-/etc/the8020/https.toml}" \
    "${THE8020_HTTPS_DIR:-/var/lib/the8020/https}" "$temporary/state.json"

  echo 'startup: preparing native HTTPS with TLS disabled' >&2
  # timeout forwards shutdown to the complete preparation process group.
  timeout --kill-after=60s 900s env THE8020_HTTPS_MIGRATING=1 \
    THE8020_NETWORK_HTTPS_PORT=0 "$entrypoint" serve &
  child=$!
  for root in "${roots[@]}"; do
    deadline=$((SECONDS + 360))
    until [[ -f "$root/node/docker/initial-user.done" ]] &&
        admin --root "$root" --json kernel.status 2>/dev/null |
          "$root/node/kernel/runtime/images/rootless/rootfs/usr/bin/deno" eval --quiet --no-config '
            const response = JSON.parse(await new Response(Deno.stdin.readable).text());
            Deno.exit(response.success && response.result?.runtime_ready &&
                !response.result?.runtime_application_failure ? 0 : 1);
          ' 2>/dev/null &&
        admin --root "$root" https.configuration >/dev/null 2>&1; do
      if ! kill -0 "$child" 2>/dev/null || (( SECONDS >= deadline )); then
        echo "HTTPS package did not become ready in $root; TLS remains disabled." >&2
        exit 1
      fi
      sleep 0.2
    done
    if [[ ! -f "$root/node/docker/native-https.done" ]]; then
      if [[ -f "$temporary/state.json" ]]; then
        admin --root "$root" https.import --state-file "$temporary/state.json"
      fi
      if [[ "$mode" == dev-prod && "$root" == "${roots[0]}" ]]; then
        admin --root "$root" https.configuration '{"challengeFallback":"http://127.0.0.1:8080"}' >/dev/null
      fi
      admin --root "$root" kernel.reindex --packages the8020/https
    fi
  done
  # Mark only after both independent systems have accepted their configuration.
  for root in "${roots[@]}"; do
    (umask 077; touch "$root/node/docker/native-https.done")
  done
  stop_preparation
  rm -rf -- "$temporary"
  trap - EXIT TERM INT HUP
fi

exec "$entrypoint" serve
