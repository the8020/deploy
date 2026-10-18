Parent DOX: [deploy](../AGENTS.md).

# Purpose

- Prepare native HTTPS for both deployment images and import supported state
  from their former bundled Caddy deployment.

# Ownership

- Own `bootstrap.sh`, `entrypoint.sh`, the one-time `https-migrate` Go reader,
  its TOML dependency, and deployment lifecycle checks.
- Kernel owns HTTP/TLS transport. `the8020/https` owns shared policy, issuance,
  renewal, certificate records, and administration. Secrets owns private keys.
- The existing kernel entrypoint and `dev-prod.sh` own normal process startup,
  initial users, connections, and shutdown. No proxy or certificate daemon runs.

# Local Contracts

- Bootstrap builds the CGO-free reader with the existing Go toolchain and ships
  Go/TOML license notices. Installation links the reader and shell entrypoint;
  it downloads no Caddy and installs no runtime interpreter or TLS TOML.
- Require native HTTPS from kernel 0.9 or newer and the HTTPS package. Retained
  volumes keep their installed releases; reject older retained kernels and
  missing packages before migration. Custom container commands still exec
  directly with their original exit status.
- Single/dev uses native HTTP 80 and 9080, HTTPS 443, and SSH 22. Production uses
  HTTP 8080 and 9081, HTTPS 8443, and SSH 2222. Single-system main-port overrides
  continue to work. The node-local HTTPS/additional-HTTP environment settings
  select single/dev ports; zero HTTPS keeps TLS closed.
- First startup prepares ordinary instances with TLS port zero, waits for the
  HTTPS package, imports legacy state if present, and forces local publication.
  Pair setup assigns development's challenge fallback to `http://127.0.0.1:8080`.
  Only after both systems accept preparation does it record private
  `node/docker/native-https.done` markers, stop preparation, and exec normal
  startup. Preparation is bounded to 900 seconds with a 60-second forced-stop
  deadline. Failures never enable TLS or remove original certificate files.
- Markers cover image preparation only. Configuration and imported identity are
  authoritative in the database; the package's import digest makes interrupted
  retries idempotent. Completed image restarts do not reread old files, import
  keys, or replace administrator settings.
- Legacy inputs use `THE8020_HTTPS_CONFIG` (default `/etc/the8020/https.toml`)
  and `THE8020_HTTPS_DIR` (default `/var/lib/the8020/https`). They are migration
  inputs only. A fresh image has neither and uses database defaults.
- Reader output is deterministic, limited to 256 KiB, and written exclusively to
  a new mode-0600 file. The wrapper keeps it in a private temporary directory,
  passes only its pathname to `https.import --state-file`, and removes it on
  success or failure. Private bytes never enter stdout, argv, or job results.
- Preserve original limits, configured IP/ACME directory, all admission attempts
  (seconds converted to milliseconds), admitted names, supplied PEM pairs,
  cached certificate/key pairs, local root CA, and supported ACME accounts.
  Missing admission history for cached ACME certificates fails explicitly.
- Support the bundled Caddy/CertMagic file layout, HTTPS ACME directories,
  one account per directory, the configured directory plus Let's Encrypt
  production/staging, and at most 100 imported certificates. Reject incomplete
  pairs, conflicting certificates, unknown issuer directories, unsupported CA
  layouts, multiple accounts for a directory, symlinks, invalid configuration,
  and oversized state. The HTTPS package validates keys, chains, identities,
  and transactionally imports all authoritative records before TLS starts.
- Each independent dev/prod database receives the original shared legacy state
  once, encrypted under its own master key. Subsequent issuance and renewal are
  system-owned; only public challenge responses cross the pair's HTTP boundary.

# Work Guidance

- Migration is an offline read of the old proxy state. Stop Caddy before reading
  it and preserve its original files and deployment image until import succeeds.
- Before switching an existing installation, update its retained kernel and
  compatible packages through their ordinary owners, keeping native TLS disabled
  with `THE8020_NETWORK_HTTPS_PORT=0` during preparation. Preserve each database
  and its original master key, plus the old HTTPS directory and TOML.
- Mount/provide those legacy inputs to the new image for first startup. Remove
  the temporary TLS-port-zero override when enabling native serving. Unsupported
  inputs require explicit operator migration; never guess a CA, reset budgets,
  replace keys, or silently fall back to new issuance.
- No legacy files are deleted automatically. After successful cutover, the UUI
  HTTPS program and ordinary HTTPS commands own configuration and replacement.
- Enrollment limits control certificate enrollment, not general HTTP traffic.

# Verification

- Run `bash -n https/bootstrap.sh https/entrypoint.sh https/entrypoint_test.sh`.
- With Go 1.21 or newer, run `(cd https && CGO_ENABLED=0 GOMAXPROCS=2
  go test -p 1 -timeout=60s -v .)`. Tests cover deterministic read-only conversion,
  private output, key/account/budget preservation, and rejection of unsupported
  or incomplete inputs. With Caddy on PATH, a fixture verifies its real local
  CA and certificate layout; Caddy is a test dependency only.
- With Deno on PATH, run `bash https/entrypoint_test.sh` and
  `bash dev-prod_test.sh`. They check preparation failures, independent pair
  imports, publication gating, restart preservation, process cleanup, old-kernel
  rejection, custom commands, and native port mappings through process doubles.
- Run the parent build-script checks. Actual image startup/shutdown and the
  native multi-node/ACME acceptance tests remain separate required checks;
  report unavailable checks rather than treating doubles as end-to-end proof.

# Child DOX Index

No child DOX documents. This document owns the entire local scope.
