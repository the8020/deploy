# syntax=docker/dockerfile:1
# The running container needs `--security-opt seccomp=unconfined` because its
# rootless service and development sandboxes launch nested gVisor processes.

FROM debian:trixie-slim AS builder

ARG DEBIAN_FRONTEND=noninteractive
ARG VERSION
ENV CGO_ENABLED=0 \
    THE8020_NETWORK_MAIN_PORT=80 \
    THE8020_NETWORK_SSH_PORT=22 \
    THE8020_SANDBOX_RUNTIME_MODE=rootless \
    THE8020_OUTER_CONTAINER_BUILD=true \
    THE8020_SKIP_RUNTIME_HOST=true

RUN apt-get update \
    && apt-get install --yes --no-install-recommends \
        bash \
        bzip2 \
        ca-certificates \
        curl \
        git \
    && rm -rf /var/lib/apt/lists/*

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

RUN if [[ ! "$VERSION" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then \
      echo "VERSION must contain exactly major.minor without leading zeroes" >&2; \
      exit 1; \
    fi \
    && release_major=${VERSION%%.*} \
    && release_minor=${VERSION#*.} \
    && git clone --quiet --filter=blob:none --no-checkout \
      https://github.com/the8020/kernel.git /usr/local/src/the8020 \
    && kernel_tag= \
    && while IFS= read -r candidate; do \
      if [[ -z "$kernel_tag" && "$candidate" =~ ^${release_major}\.${release_minor}\.(0|[1-9][0-9]*)$ ]]; then \
        kernel_tag=$candidate; \
      fi; \
    done < <(git -C /usr/local/src/the8020 tag --list "$VERSION.*" --sort=-version:refname) \
    && if [[ -z "$kernel_tag" ]]; then \
      echo "no kernel tag matches release line $VERSION" >&2; \
      exit 1; \
    fi \
    && git -C /usr/local/src/the8020 checkout --quiet --detach "$kernel_tag" \
    && install -d -m 0755 /usr/local/share/the8020 \
    && printf 'release_line=%s\nkernel_tag=%s\nkernel_commit=%s\n' \
      "$VERSION" "$kernel_tag" \
      "$(git -C /usr/local/src/the8020 rev-parse --verify HEAD)" \
      > /usr/local/share/the8020/release

WORKDIR /usr/local/src/the8020

# The fresh installation resolves and records compatible first-party package
# tags, synchronizes their tables, and materializes both sandbox images.
RUN install -d -m 0755 /8020 \
    && cd /8020 \
    && export THE8020_RELEASE_VERSION="$VERSION" \
    && printf 'exit\n' | /usr/local/src/the8020/run.sh \
    && rm -rf \
        /8020/node/kernel/runtime/downloads \
        /8020/node/kernel/runtime/gvisor \
        /8020/node/kernel/runtime/tmp \
        /8020/node/kernel/runtime/verification-deno-cache

FROM debian:trixie-slim

ARG DEBIAN_FRONTEND=noninteractive
ARG VERSION
ENV THE8020_NETWORK_MAIN_PORT=80 \
    THE8020_NETWORK_SSH_PORT=22 \
    THE8020_SANDBOX_RUNTIME_MODE=rootless

LABEL org.opencontainers.image.source="https://github.com/the8020/deploy" \
    org.opencontainers.image.version="$VERSION"

RUN apt-get update \
    && apt-get install --yes --no-install-recommends \
        bash \
        ca-certificates \
        curl \
        git \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /usr/local/src/the8020/.development/bin/kernel /usr/local/bin/kernel
COPY --from=builder /usr/local/src/the8020/.development/bin/admin /usr/local/bin/admin
COPY --from=builder /usr/local/src/the8020/.development/bin/logd /usr/local/bin/logd
COPY --from=builder /usr/local/src/the8020/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
COPY --from=builder /usr/local/src/the8020/defaults/config/runtime/smoke-portable.sh /usr/local/lib/the8020/smoke-portable.sh
COPY --from=builder /usr/local/share/the8020/release /usr/local/share/the8020/release
COPY --from=builder /8020 /8020

WORKDIR /8020
VOLUME ["/8020"]

EXPOSE 80/tcp 22/tcp
STOPSIGNAL SIGTERM

HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
    CMD ["/usr/local/bin/admin", "--root", "/8020", "kernel.status"]

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["serve"]
