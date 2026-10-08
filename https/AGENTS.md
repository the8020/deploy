Parent DOX: [deploy](../AGENTS.md).

# Purpose

- Bootstrap and run automatic HTTPS for both deployment images.

# Ownership

- Own `bootstrap.sh`, `proxy.go`, shipped `https.toml`, the Go module, and
  `proxy_test.go`.
- Caddy owns TLS, ACME, certificate selection/storage/renewal, HTTP proxying,
  and WebSocket transport. This scope owns admission policy and image startup.
- Kernels and application packages retain their HTTP and authentication contracts.

# Local Contracts

- Bootstrap's `build` phase uses the kernel builder's Go toolchain to compile a
  standalone, CGO-free entrypoint. The `install` phase installs checksum-verified
  Caddy, links that binary, preserves Caddy/Go/TOML license notices, and installs
  `/etc/the8020/https.toml`. Final images contain no Go helper sources or
  toolchain and gain no installed Python/interpreter runtime.
- One proxy serves each container: HTTP 9080/HTTPS 443 to kernel port 80;
  the pair additionally serves HTTP 9081/HTTPS 8443 to kernel port 8080.
  Publish host HTTP 80/8080 to 9080/9081. Internal HTTP 80/8080 stays unchanged.
  Single-system upstreams honor `THE8020_NETWORK_MAIN_PORT`; reject a collision
  with proxy ports 443/9080.
- `THE8020_HTTPS_DIR` selects private certificate/admission storage, default
  `/var/lib/the8020/https`. A volume is optional. `provided/` accepts PEM
  certificate-and-key bundles, selected by their SANs and renewed externally.
- `THE8020_HTTPS_CONFIG` selects TOML, default `/etc/the8020/https.toml`.
  Limits reload for every unfamiliar-name admission. Invalid limits deny new
  admission; existing admitted names remain eligible for renewal. TLS settings
  take effect at startup. Zero enrollment/name limits pause new enrollment.
- Admission normalizes names, limits attempts before DNS/network work, and
  persists attempts and admitted names in private `admissions.json`, bounded to
  1 MiB and saved through atomic replacement. Invalid state fails startup rather
  than resetting budgets. Failed checks consume the hourly allowance; admission
  reserves a name before ACME, including failed
  issuance. Names remain recorded, so lowering limits does not revoke them.
- A timed HTTP challenge checks reachability through the requested name before
  ACME. One Go context bounds DNS and HTTP together; connections pin resolved
  public addresses, reject mixed public/private answers, and follow no redirects.
  The loopback helper has at most 32 open connections and no public ask route.
- Local certificates cover localhost and loopback. Optional `tls.ip_address`
  selects the no-SNI certificate behind Docker NAT: configuring a public IP uses
  Let's Encrypt's short-lived profile for all public certificates; private IPs
  use the local CA. Unknown external IPs cannot be inferred from a first HTTPS
  handshake. Clients must trust the local CA for local certificates. No CA keys
  or certificates enter image layers.
- Proxy and instance startup share shutdown: any child exit stops the container;
  TERM/INT forwards graceful shutdown with a 60-second deadline. Bootstrap
  credentials reach only the instance entrypoint, never Caddy. DNS/HTTP admission
  checks run inside the helper without additional processes.
- Preserve original Host, forwarded HTTPS scheme, streaming, and upgrades.
  The existing kernel reports the proxy socket's client IP, not forwarded IPs;
  local token authority still requires the native authentication marker.

# Work Guidance

- Keep this independent of application startup and databases. Use stock Caddy
  and Go standard-library facilities plus the existing Go TOML parser; do not
  implement TLS or ACME here. Reuse the image's Go builder, not a new runtime.
- Enrollment budgets bound certificate abuse, not traffic floods or enrollment
  availability during an attack. Domain reachability is not owner approval.

# Verification

- Run `bash -n https/bootstrap.sh` and
  `(cd https && CGO_ENABLED=0 GOMAXPROCS=2 go test -p 1 -timeout=90s -v .)` with
  Go 1.21 or newer. Tests check the actual helper's compiled lifecycle and image
  wiring, including the absence of an HTTPS runtime interpreter install.
- With checksum-verified Caddy on PATH, the same test exercises real local TLS,
  certificate reuse, both proxy destinations, headers/cookies, WebSocket
  transport, supplied certificates, and child shutdown. All checks use isolated
  temporary state and loopback listeners; no production ACME issuance.
- With Pebble also on PATH, the same check exercises local ACME domain and
  no-SNI IP issuance, the short-lived profile, real HTTP admission proof, and
  paused enrollment. Pebble bypasses public DNS validation; DNS guards are tested
  independently. No public CA or host trust-store changes are used.
- Socket-based checks report a skip when the environment forbids loopback
  listeners; report those skips rather than claiming end-to-end verification.
- Run the parent repository's existing build/startup checks. Docker image smoke
  remains a separate check and must be reported when unavailable.

# Child DOX Index

No child DOX documents. This document owns the entire local scope.
