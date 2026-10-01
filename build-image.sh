#!/usr/bin/env bash
set -euo pipefail

readonly VERSION=${1:-}
if [[ ! "$VERSION" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo 'VERSION must contain exactly major.minor without leading zeroes' >&2
  exit 1
fi
release_major=${VERSION%%.*}
release_minor=${VERSION#*.}
git clone --quiet --filter=blob:none --no-checkout \
  https://github.com/the8020/kernel.git /usr/local/src/the8020
kernel_tag=
kernel_tags=$(git -C /usr/local/src/the8020 tag --list "$VERSION.*" --sort=-version:refname)
while IFS= read -r candidate; do
  if [[ -z "$kernel_tag" && "$candidate" =~ ^${release_major}\.${release_minor}\.(0|[1-9][0-9]*)$ ]]; then
    kernel_tag=$candidate
  fi
done <<< "$kernel_tags"
if [[ -z "$kernel_tag" ]]; then
  echo "no kernel tag matches release line $VERSION" >&2
  exit 1
fi
git -C /usr/local/src/the8020 checkout --quiet --detach "$kernel_tag"
install -d -m 0755 /usr/local/share/the8020
printf 'release_line=%s\nkernel_tag=%s\nkernel_commit=%s\n' \
  "$VERSION" "$kernel_tag" \
  "$(git -C /usr/local/src/the8020 rev-parse --verify HEAD)" \
  > /usr/local/share/the8020/release

# Use the selected kernel's installer for packages, tables and sandbox images.
install -d -m 0755 /8020
cd /8020
export THE8020_RELEASE_VERSION="$VERSION"
printf 'exit\n' | /usr/local/src/the8020/run.sh
rm -rf \
  /8020/node/kernel/runtime/downloads \
  /8020/node/kernel/runtime/gvisor \
  /8020/node/kernel/runtime/tmp \
  /8020/node/kernel/runtime/verification-deno-cache
mv /8020/node/kernel/bin /usr/local/share/the8020/runtime-bin
ln -s /usr/local/share/the8020/runtime-bin /8020/node/kernel/bin
if grep -q '^readonly RUNTIME_STATE=' \
    /usr/local/src/the8020/docker/rootfs/usr/local/bin/docker-entrypoint.sh; then
  install -d -m 0755 /usr/local/share/the8020/runtime-state
  mv /8020/node/kernel/runtime/definitions /8020/node/kernel/runtime/images \
    /usr/local/share/the8020/runtime-state/
  cd /usr/local/share/the8020/runtime-state
  { cat ../release; cat images/*/image.json;
    tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -cf - definitions; } \
    | sha256sum | cut -d' ' -f1 > id
fi

# Keep only platform assets from the build instance's observed runtime state.
find /8020/node/kernel/runtime -mindepth 1 -maxdepth 1 \
  ! -name definitions ! -name images -exec rm -rf -- {} +
