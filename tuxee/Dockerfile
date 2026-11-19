# syntax=docker/dockerfile:1
FROM agent0ai/tuxee:latest AS desktop
USER root
COPY --from=platform /usr/local/share/the8020/runtime-state/definitions/development/build.sh /tmp/dev-build.sh
RUN bash /tmp/dev-build.sh && rm /tmp/dev-build.sh
COPY --from=platform /usr/local/share/the8020/runtime-state/images/development/rootfs/opt/development/ /opt/development/
COPY --from=platform /usr/local/share/the8020/runtime-state/images/development/rootfs/usr/bin/deno /usr/bin/deno
COPY --chmod=0755 desktop-prepare /opt/development/desktop-prepare
RUN install -m 0444 /opt/development/profile /etc/profile

FROM platform AS build
COPY gateway/ /tmp/gateway/
WORKDIR /tmp/gateway
ENV GOMAXPROCS=2 CGO_ENABLED=0 GOWORK=off
RUN set -eu; tools=/usr/local/src/the8020/.development; PATH="$tools/toolchains/go/bin:$PATH"; \
    export GOMODCACHE="$tools/cache/go-mod" GOCACHE="$tools/cache/go-build"; \
    go test -mod=readonly ./... && go build -trimpath -mod=readonly -o /tmp/preview-gateway .; \
    mkdir /tmp/gateway-licenses; cp "$(go env GOROOT)/LICENSE" /tmp/gateway-licenses/Go; \
    cp "$(go list -m -f '{{.Dir}}' golang.org/x/net)/LICENSE" /tmp/gateway-licenses/x-net
WORKDIR /8020/packages/the8020/dev-core
RUN tag=0.10.7-tuxee.1 && git fetch origin tag "$tag" \
    && git reset --hard "$tag^{commit}" && git config --local the8020.requestedTag "$tag"
WORKDIR /usr/local/share/the8020/runtime-state/images/development
RUN rm -rf rootfs
COPY --from=desktop / rootfs/
RUN set -eu; tar --sort=name --mtime=@0 --numeric-owner -cf /tmp/desktop.tar rootfs; \
    digest=$(sha256sum /tmp/desktop.tar | cut -d' ' -f1); rm /tmp/desktop.tar; \
    version=$(rootfs/usr/bin/deno --version | awk 'NR == 1 {print $2}'); \
    printf '{"schema":1,"image_digest":"sha256:%s","deno_version":"%s","built_at":"%s","build_status":"ready"}\n' \
      "$digest" "$version" "$(date -u +%FT%TZ)" > image.json

FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates curl procps && rm -rf /var/lib/apt/lists/*
WORKDIR /8020
ENV THE8020_NETWORK_MAIN_PORT=18080 THE8020_NETWORK_HTTPS_PORT=0 THE8020_SANDBOX_RUNTIME_MODE=rootless GOMAXPROCS=2
COPY --from=build /8020/packages/ ./packages/
COPY --from=build /usr/local/share/the8020/runtime-state/development-scripts/ ./scripts/
COPY --from=build /usr/local/share/the8020/runtime-state/kernel-scripts/ ./node/kernel/scripts/
COPY --from=build /usr/local/share/the8020/runtime-bin/ ./node/kernel/bin/
COPY --from=build /usr/local/src/the8020/.development/bin/ ./node/kernel/bin/
COPY --from=build /usr/local/share/the8020/runtime-state/definitions/ ./node/kernel/runtime/definitions/
COPY --from=build /usr/local/share/the8020/runtime-state/images/ ./node/kernel/runtime/images/
COPY --from=build /tmp/preview-gateway /usr/local/bin/
COPY --from=build /tmp/gateway-licenses/ /usr/local/share/doc/preview-gateway/
COPY --chmod=0755 start /usr/local/bin/start-trial
ENTRYPOINT ["/usr/local/bin/start-trial"]
