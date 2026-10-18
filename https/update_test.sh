#!/usr/bin/env bash
set -euo pipefail
readonly SOURCE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
readonly TEST=$(mktemp -d)
trap 'rm -rf -- "$TEST"' EXIT
export THE8020_INSTANCE_ROOT="$TEST/instance"
root=$THE8020_INSTANCE_ROOT
state="$root/node/docker/native-https-upgrade"
prefix="$TEST/prefix"
bundle="$TEST/bundle"
mkdir -p "$state" "$prefix/bin" "$root/node/kernel/bin" "$bundle"
cp "$SOURCE/update.sh" "$bundle/"
for name in https-migrate https-entrypoint dev-prod.sh go-LICENSE go-toml-LICENSE; do
  echo replacement > "$bundle/$name"
done
printf '%s\n' "$prefix" > "$state/prefix"
echo old-wrapper > "$prefix/bin/https-entrypoint"
echo old-caddy > "$prefix/bin/caddy"
cat > "$root/node/kernel/bin/admin" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$THE8020_INSTANCE_ROOT/calls"
if [[ "$*" == *kernel.packages.list* ]]; then echo '{"success":true,"result":{}}'; fi
if [[ "$*" == *kernel.packages.inspect* ]]; then echo '{"success":true,"result":{"package":{"state":"failed"}}}'; fi
if [[ "$*" == *kernel.packages.synchronize* ]]; then
  if [[ -f "$THE8020_INSTANCE_ROOT/reject" ]]; then
    echo '{"success":true,"result":{"packages":[{"success":false,"error":"fixture rejection"}]}}'
  else echo '{"success":true,"result":{"packages":[{"success":true}]}}'; fi
fi
EOF
chmod +x "$root/node/kernel/bin/admin"
mkdir -p "$root/node/kernel/runtime/images/rootless/rootfs/usr/bin"
ln -s "$(command -v deno)" "$root/node/kernel/runtime/images/rootless/rootfs/usr/bin/deno"

# Local source changes fail before any catalog mutation.
mkdir -p "$root/packages/the8020/db"
git -C "$root/packages/the8020/db" init --quiet
touch "$root/packages/the8020/db/unsaved"
if bash "$bundle/update.sh" online > "$TEST/output" 2>&1; then exit 1; fi
[[ ! -e "$root/calls" && -f "$prefix/bin/caddy" ]]
grep -q 'Save local changes' "$TEST/output"
rm -rf -- "$root/packages/the8020/db"

# A selected tag is not completion if its catalog activation was interrupted.
mkdir -p "$root/packages/the8020/db"
git -C "$root/packages/the8020/db" init --quiet
git -C "$root/packages/the8020/db" -c user.name=Fixture -c user.email=fixture@invalid \
  -c commit.gpgsign=false commit --quiet --allow-empty -m fixture
git -C "$root/packages/the8020/db" tag 0.9.1
mkdir -p "$root/packages/the8020/https"

# A command may succeed while its package result fails; never retire Caddy then.
touch "$root/reject"
if bash "$bundle/update.sh" online > "$TEST/output" 2>&1; then exit 1; fi
[[ -f "$prefix/bin/caddy" && ! -f "$state/prepared" ]]
grep -q 'old-wrapper' "$prefix/bin/https-entrypoint"
grep -q 'kernel.packages.set the8020 db' "$root/calls"
rm "$root/reject"

# The shared wrapper waits for the other independent system.
mkdir -p "$root-prod"
touch "$root-prod/kernel.toml"
bash "$bundle/update.sh" online > "$TEST/output"
[[ -f "$state/prepared" && -f "$prefix/bin/caddy" ]]
grep -q 'paired instance' "$TEST/output"
mkdir -p "$root-prod/node/docker/native-https-upgrade"
touch "$root-prod/node/docker/native-https-upgrade/prepared"
bash "$bundle/update.sh" online > "$TEST/output"
[[ ! -e "$prefix/bin/caddy" ]]
grep -q replacement "$prefix/bin/https-entrypoint"
grep -q old-wrapper "$state/https-entrypoint.old"
grep -q 'Restart the container once' "$TEST/output"
before=$(wc -l < "$root/calls")
bash "$bundle/update.sh" online >/dev/null
[[ $(wc -l < "$root/calls") == "$before" ]]
grep -q old-wrapper "$state/https-entrypoint.old"
echo 'Native HTTPS update checks passed'
