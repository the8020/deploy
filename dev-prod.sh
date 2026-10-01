#!/usr/bin/env bash
set -euo pipefail

readonly DEV=${THE8020_INSTANCE_ROOT:-/8020}
readonly PROD="${DEV}-prod"
readonly ENTRYPOINT=docker-entrypoint.sh
readonly DENO="$DEV/node/kernel/runtime/images/rootless/rootfs/usr/bin/deno"
initial_username=${THE8020_USERNAME:-admin}
initial_password=${THE8020_PASSWORD:-admin}

if [[ ${1:-serve} == health ]]; then
  for root in "$DEV" "$PROD"; do
    admin --root "$root" kernel.status >/dev/null
  done
  for port in 80 8080; do
    curl --fail --silent --show-error --noproxy '*' --max-time 3 \
      "http://127.0.0.1:$port/health" >/dev/null
  done
  exit
fi
if (( $# > 0 )) && [[ "$*" != serve ]]; then exec "$@"; fi

pids=()
finish() {
  trap - EXIT
  for pid in "${pids[@]}"; do kill -TERM "$pid" 2>/dev/null || true; done
  for pid in "${pids[@]}"; do wait "$pid" 2>/dev/null || true; done
}
trap finish EXIT
trap 'exit 143' TERM HUP
trap 'exit 130' INT

start() {
  local root=$1 port=$2 ssh=$3 name=$4 role=$5 pid deadline response
  echo "startup: starting $name on HTTP $port and SSH $ssh" >&2
  THE8020_INSTANCE_ROOT="$root" THE8020_NETWORK_MAIN_PORT="$port" \
    THE8020_NETWORK_SSH_PORT="$ssh" "$ENTRYPOINT" serve &
  pid=$!
  pids+=("$pid")
  deadline=$((SECONDS + 360))
  until [[ -f "$root/node/docker/initial-user.done" ]] &&
      curl --fail --silent --noproxy '*' --max-time 3 \
        "http://127.0.0.1:$port/the8020/uui/login/" >/dev/null; do
    if ! kill -0 "$pid" 2>/dev/null || (( SECONDS >= deadline )); then
      echo "$name did not become ready" >&2
      return 1
    fi
    sleep 0.2
  done
  if [[ -f "$root/node/docker/dev-prod.done" ]]; then return; fi
  response=$(admin --root "$root" --json runtime eval \
    'export default async (profile) => {
      const {setSystemProfile} = await import("/p/the8020/system/profile.ts");
      return await setSystemProfile(profile);
    }' --input "{\"name\":\"$name\",\"role\":\"$role\"}" \
    --timeout-ms 60000 --read /workspace/packages --imports jsr.io,registry.npmjs.org)
  printf '%s' "$response" | "$DENO" eval --quiet --no-config '
    const response = JSON.parse(await new Response(Deno.stdin.readable).text());
    if (response.success !== true || response.result?.state !== "SUCCEEDED") {
      throw new Error("System profile setup failed: " + JSON.stringify(response));
    }
  '
}

start "$DEV" 80 22 Development development
start "$PROD" 8080 2222 Production production
if [[ ! -f "$DEV/node/docker/dev-prod.done" || ! -f "$PROD/node/docker/dev-prod.done" ]]; then
  echo 'startup: connecting development and production' >&2
  connect() {
    printf '%s\n' "$initial_password" |
      admin --root "$1" deployments.connect "$2" "$initial_username" --password-stdin >/dev/null
  }
  connect "$DEV" http://127.0.0.1
  connect "$DEV" http://127.0.0.1:8080
  connect "$PROD" http://127.0.0.1
  for root in "$DEV" "$PROD"; do
    (umask 077; touch "$root/node/docker/dev-prod.done")
  done
fi
unset initial_username initial_password
unset THE8020_USERNAME THE8020_PASSWORD THE8020_SIGNING_KEY
echo '80|20 dev/prod is ready; deployment URLs: http://127.0.0.1 and http://127.0.0.1:8080' >&2

# Any child exit stops the pair; ordinary kernel restarts retain their PID.
wait -n "${pids[@]}"
