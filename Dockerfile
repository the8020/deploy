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
        python3 \
    && rm -rf /var/lib/apt/lists/*

COPY build-image.sh /usr/local/bin/build-image.sh
RUN chmod 0755 /usr/local/bin/build-image.sh \
    && /usr/local/bin/build-image.sh "$VERSION"

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

COPY --from=builder /usr/local/src/the8020/.development/bin/ /usr/local/bin/
COPY --from=builder /usr/local/src/the8020/docker/rootfs/ /
COPY --from=builder /usr/local/share/the8020/ /usr/local/share/the8020/
COPY --from=builder /8020/packages/ /8020/packages/
COPY --from=builder /8020/scripts/ /8020/scripts/
COPY --from=builder /8020/node/kernel/runtime/ /8020/node/kernel/runtime/

# Ship code and runtime assets, with fresh database, identities and keys per volume.
RUN install -d -m 0700 /8020/node/kernel /8020/database \
    && install -d -m 0755 /8020/users \
    && install -m 0600 /dev/null /8020/kernel.toml

WORKDIR /8020
VOLUME ["/8020"]

EXPOSE 80/tcp 22/tcp
STOPSIGNAL SIGTERM

HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
    CMD ["/usr/local/bin/admin", "--root", "/8020", "kernel.status"]

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["serve"]
