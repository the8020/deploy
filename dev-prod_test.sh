#!/usr/bin/env bash
set -euo pipefail
trap 'echo "Dev/prod startup check failed at line $LINENO" >&2' ERR

readonly SOURCE_ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
readonly TEST_ROOT=$(mktemp -d)
pair_pid=""
cleanup() {
  if [[ -n "$pair_pid" ]]; then
    kill -TERM "$pair_pid" 2>/dev/null || true
    wait "$pair_pid" 2>/dev/null || true
  fi
  rm -rf -- "$TEST_ROOT"
}
trap cleanup EXIT
export TEST_ROOT THE8020_INSTANCE_ROOT="$TEST_ROOT/dev"
export THE8020_USERNAME=deployer THE8020_PASSWORD='test password'
mkdir -p "$TEST_ROOT/bin" "$THE8020_INSTANCE_ROOT/node/kernel/runtime/images/rootless/rootfs/usr/bin"
ln -s "$(command -v deno)" "$THE8020_INSTANCE_ROOT/node/kernel/runtime/images/rootless/rootfs/usr/bin/deno"
cat > "$TEST_ROOT/bin/docker-entrypoint.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
mkdir -p "$THE8020_INSTANCE_ROOT/node/docker"
printf '%s %s\n' "$THE8020_NETWORK_MAIN_PORT" "$THE8020_NETWORK_SSH_PORT" >> "$TEST_ROOT/ports"
# The common entrypoint owns initial users and each independent key.
touch "$THE8020_INSTANCE_ROOT/node/docker/initial-user.done"
sleep 60 &
child=$!
trap 'kill "$child" 2>/dev/null || true; wait "$child" 2>/dev/null || true' EXIT
trap 'exit 143' TERM
wait "$child"
EOF
cat > "$TEST_ROOT/bin/curl" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
cat > "$TEST_ROOT/bin/admin" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == *deployments.connect* ]]; then
  [[ "$*" == *' deployer --password-stdin' ]]
  IFS= read -r password
  [[ "$password" == 'test password' ]]
  # Check stdin without ever recording it.
  printf '%s\n' "$*" >> "$TEST_ROOT/connections"
  if [[ -f "$TEST_ROOT/fail-connect" ]]; then exit 1; fi
elif [[ "$*" == *'runtime eval'* ]]; then
  while (( $# > 0 )); do
    if [[ "$1" == --input ]]; then printf '%s\n' "$2" >> "$TEST_ROOT/profiles"; break; fi
    shift
  done
  printf '%s\n' '{"success":true,"result":{"state":"SUCCEEDED"}}'
fi
EOF
chmod +x "$TEST_ROOT/bin/"*
export PATH="$TEST_ROOT/bin:$PATH"

start_pair() {
  bash "$SOURCE_ROOT/dev-prod.sh" > "$TEST_ROOT/output" 2>&1 &
  pair_pid=$!
}
wait_ready() {
  for _ in {1..200}; do
    if grep -Fq 'dev/prod is ready' "$TEST_ROOT/output"; then return; fi
    kill -0 "$pair_pid" 2>/dev/null || break
    sleep 0.05
  done
  cat "$TEST_ROOT/output" >&2
  return 1
}
stop_pair() {
  kill -TERM "$pair_pid"
  wait "$pair_pid" 2>/dev/null || true
  pair_pid=""
}

# A failed connection must leave initialization pending and stop both children.
touch "$TEST_ROOT/fail-connect"
start_pair
if wait "$pair_pid"; then exit 1; fi
pair_pid=""
[[ ! -f "$THE8020_INSTANCE_ROOT/node/docker/dev-prod.done" ]]
[[ ! -f "$THE8020_INSTANCE_ROOT-prod/node/docker/dev-prod.done" ]]
rm "$TEST_ROOT/fail-connect" "$TEST_ROOT/connections" "$TEST_ROOT/profiles" "$TEST_ROOT/ports"

start_pair
wait_ready
[[ $(wc -l < "$TEST_ROOT/connections") == 3 ]]
[[ $(wc -l < "$TEST_ROOT/profiles") == 2 ]]
grep -Fq '"name":"Development","role":"development"' "$TEST_ROOT/profiles"
grep -Fq '"name":"Production","role":"production"' "$TEST_ROOT/profiles"
grep -Fxq '80 22' "$TEST_ROOT/ports"
grep -Fxq '8080 2222' "$TEST_ROOT/ports"
grep -Fq 'deployments.connect http://127.0.0.1 deployer --password-stdin' "$TEST_ROOT/connections"
grep -Fq 'deployments.connect http://127.0.0.1:8080 deployer --password-stdin' "$TEST_ROOT/connections"
stop_pair

# Completed volumes retain their profiles and connections despite env changes.
export THE8020_USERNAME=changed THE8020_PASSWORD=changed
start_pair
wait_ready
[[ $(wc -l < "$TEST_ROOT/connections") == 3 ]]
[[ $(wc -l < "$TEST_ROOT/profiles") == 2 ]]
bash "$SOURCE_ROOT/dev-prod.sh" health
stop_pair
echo 'Dev/prod startup checks passed'
