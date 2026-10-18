#!/usr/bin/env bash
set -euo pipefail
trap 'echo "HTTPS startup check failed at line $LINENO" >&2' ERR
readonly SOURCE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
readonly TEST_ROOT=$(mktemp -d)
pid=""
cleanup() {
  if [[ -n "$pid" ]]; then
    kill -TERM "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
  rm -rf -- "$TEST_ROOT"
}
trap cleanup EXIT
export TEST_ROOT THE8020_INSTANCE_ROOT="$TEST_ROOT/dev"
mkdir -p "$TEST_ROOT/bin"
for root in "$THE8020_INSTANCE_ROOT" "$THE8020_INSTANCE_ROOT-prod"; do
  mkdir -p "$root/packages/the8020/https/cbus/commands"
  touch "$root/packages/the8020/https/cbus/commands/import.toml"
  mkdir -p "$root/node/kernel/runtime/images/rootless/rootfs/usr/bin"
  ln -s "$(command -v deno)" "$root/node/kernel/runtime/images/rootless/rootfs/usr/bin/deno"
done
cat > "$TEST_ROOT/bin/https-migrate" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
echo 'read migration' >> "$TEST_ROOT/events"
if [[ -f "$TEST_ROOT/reject-source" ]]; then exit 7; fi
if [[ -f "$TEST_ROOT/legacy" ]]; then
  (umask 077; printf '%s' 'private migration fixture' > "$3")
  printf '%s' "$3" > "$TEST_ROOT/private-path"
fi
EOF
cat > "$TEST_ROOT/bin/admin" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == --root ]]
root=$2
shift 2
if [[ ${1:-} == --json ]]; then shift; fi
case "$1" in
  kernel.status)
    if [[ -f "$TEST_ROOT/runtime-pending" ]]; then
      echo 'runtime pending' >> "$TEST_ROOT/events"
      printf '%s\n' '{"success":true,"result":{"runtime_ready":false}}'
      exit
    fi
    printf '%s\n' '{"success":true,"result":{"runtime_ready":true}}'
    ;;
  https.import)
    [[ "$2" == --state-file && -f "$3" && $(stat -c %a "$3") == 600 ]]
    echo "import $root" >> "$TEST_ROOT/events"
    if [[ -f "$TEST_ROOT/reject-import" ]]; then exit 8; fi
    ;;
  https.configuration)
    if (( $# > 1 )); then
      [[ "$2" == '{"challengeFallback":"http://127.0.0.1:8080"}' ]]
      echo "fallback $root" >> "$TEST_ROOT/events"
    fi
    ;;
  kernel.reindex)
    echo "publish $root" >> "$TEST_ROOT/events"
    if [[ -f "$TEST_ROOT/reject-publication" ]]; then exit 9; fi
    ;;
  kernel.config.unset)
    [[ "$2" == network.https_port ]]
    echo "enable ports $root" >> "$TEST_ROOT/events"
    ;;
  *) exit 10 ;;
esac
EOF
cat > "$TEST_ROOT/bin/docker-entrypoint.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
mode=${0##*/}
roots=("$THE8020_INSTANCE_ROOT")
if [[ "$mode" == dev-prod.sh ]]; then roots+=("$THE8020_INSTANCE_ROOT-prod"); fi
for root in "${roots[@]}"; do
  mkdir -p "$root/node/docker"
  touch "$root/node/docker/initial-user.done"
done
if [[ ${THE8020_HTTPS_MIGRATING:-} == 1 ]]; then
  [[ "$THE8020_NETWORK_HTTPS_PORT" == 0 ]]
  echo "prepare $mode" >> "$TEST_ROOT/events"
else
  echo "serve $mode $THE8020_NETWORK_HTTPS_PORT $THE8020_NETWORK_ADDITIONAL_HTTP_PORT" >> "$TEST_ROOT/events"
fi
sleep 60 &
child=$!
trap 'kill "$child" 2>/dev/null || true; wait "$child" 2>/dev/null || true; echo stopped >> "$TEST_ROOT/events"' EXIT
trap 'exit 143' TERM INT
wait "$child"
EOF
cp "$TEST_ROOT/bin/docker-entrypoint.sh" "$TEST_ROOT/bin/dev-prod.sh"
chmod +x "$TEST_ROOT/bin/"*
export PATH="$TEST_ROOT/bin:$PATH"

start() {
  bash "$SOURCE/entrypoint.sh" "$1" serve > "$TEST_ROOT/output" 2>&1 &
  pid=$!
}
ready() {
  for _ in {1..200}; do
    if grep -q '^serve ' "$TEST_ROOT/events"; then return; fi
    if ! kill -0 "$pid" 2>/dev/null; then break; fi
    sleep 0.05
  done
  cat "$TEST_ROOT/output" >&2
  return 1
}
stop() {
  kill -TERM "$pid"
  wait "$pid" 2>/dev/null || true
  pid=""
}
reset_markers() {
  rm -f "$THE8020_INSTANCE_ROOT/node/docker/native-https.done" \
    "$THE8020_INSTANCE_ROOT-prod/node/docker/native-https.done"
  : > "$TEST_ROOT/events"
}

# Custom commands retain their exit status and do not initialize anything.
if bash "$SOURCE/entrypoint.sh" single bash -c 'exit 23'; then exit 1; else [[ $? == 23 ]]; fi
[[ ! -f "$TEST_ROOT/events" ]]

# Unsupported source fails before either instance starts.
touch "$TEST_ROOT/reject-source"
start single
if wait "$pid"; then exit 1; fi
pid=""
! grep -q '^prepare\|^serve' "$TEST_ROOT/events"
rm "$TEST_ROOT/reject-source"

# Failed import/publication never enable TLS or mark initialization complete.
touch "$TEST_ROOT/legacy"
for failure in reject-import reject-publication; do
  reset_markers
  touch "$TEST_ROOT/$failure"
  start single
  if wait "$pid"; then exit 1; fi
  pid=""
  ! grep -q '^serve ' "$TEST_ROOT/events"
  [[ ! -f "$THE8020_INSTANCE_ROOT/node/docker/native-https.done" ]]
  [[ ! -f $(cat "$TEST_ROOT/private-path") ]]
  ! grep -q 'private migration fixture' "$TEST_ROOT/output"
  rm "$TEST_ROOT/$failure"
done

# Both databases receive the same original material before enabling native TLS.
reset_markers
mkdir -p "$THE8020_INSTANCE_ROOT/node/docker/native-https-upgrade"
touch "$THE8020_INSTANCE_ROOT/node/docker/native-https-upgrade/prepared"
touch "$TEST_ROOT/runtime-pending"
start dev-prod
for _ in {1..200}; do
  if grep -q '^runtime pending' "$TEST_ROOT/events"; then break; fi
  sleep 0.01
done
grep -q '^runtime pending' "$TEST_ROOT/events"
! grep -q '^import\|^publish\|^serve' "$TEST_ROOT/events"
rm "$TEST_ROOT/runtime-pending"
ready
[[ $(grep -c '^import ' "$TEST_ROOT/events") == 2 ]]
[[ $(grep -c '^fallback ' "$TEST_ROOT/events") == 1 ]]
[[ $(grep -c '^enable ports ' "$TEST_ROOT/events") == 1 ]]
grep -Fxq "fallback $THE8020_INSTANCE_ROOT" "$TEST_ROOT/events"
grep -Fxq 'serve dev-prod.sh 443 9080' "$TEST_ROOT/events"
[[ -f "$THE8020_INSTANCE_ROOT/node/docker/native-https.done" ]]
[[ -f "$THE8020_INSTANCE_ROOT-prod/node/docker/native-https.done" ]]
[[ ! -f $(cat "$TEST_ROOT/private-path") ]]
stop

# Restarts preserve administrator configuration and skip old file parsing.
: > "$TEST_ROOT/events"
touch "$TEST_ROOT/reject-source"
start dev-prod
ready
! grep -q '^read\|^import\|^fallback\|^prepare' "$TEST_ROOT/events"
stop
rm "$TEST_ROOT/reject-source"

# A fresh system has no import, and the existing port override remains explicit.
reset_markers
rm "$TEST_ROOT/legacy"
export THE8020_NETWORK_HTTPS_PORT=9443 THE8020_NETWORK_ADDITIONAL_HTTP_PORT=9180
start single
ready
! grep -q '^import ' "$TEST_ROOT/events"
grep -Fxq 'serve docker-entrypoint.sh 9443 9180' "$TEST_ROOT/events"
stop

# Retained older kernels are rejected before any migration or process startup.
reset_markers
root=$THE8020_INSTANCE_ROOT
mkdir -p "$root/node/kernel/releases/0.8.3" "$root/node/kernel/runtime/images/rootless/rootfs/usr/bin"
printf '%s' '{"version":"0.8.3"}' > "$root/node/kernel/releases/0.8.3/release.json"
ln -s releases/0.8.3 "$root/node/kernel/current"
start single
if wait "$pid"; then exit 1; fi
pid=""
[[ ! -s "$TEST_ROOT/events" ]]
if ! grep -q 'Update the retained kernel' "$TEST_ROOT/output"; then
  cat "$TEST_ROOT/output" >&2
  exit 1
fi

echo 'Native HTTPS startup checks passed'
