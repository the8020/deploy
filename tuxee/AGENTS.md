Parent DOX: [deploy](../AGENTS.md).

# Purpose

- Build the single-node Docker preview with a Tuxee development sandbox and a
  browser gateway on localhost.

# Ownership

- The Docker build reuses the standard release builder and fetches the matching
  dev-core preview tag. The Tuxee image supplies the guest desktop and apps.
  Dev-core owns the Development screen, assets and admission service.

# Local Contracts

- Publish only the gateway on Docker host loopback. The main UI and each viewer
  have separate browser origins. The gateway obtains the sandbox ID from
  dev-core, retains its ordinary console, verifies the guest certificate and
  keeps the guest login on the server. A launch code is valid once for 45 seconds.
- Closing the view releases the console. Recheck admission every five seconds
  while attached and cancel active streams when access ends. Read the launch
  request before locking the view registry.
- The guest starts Tuxee on loopback with a random port. Each sandbox stores its
  own generated root password in a mode-0600 file. Rootless guests share node
  loopback, so possession of that password and access to the node network allow
  direct guest login.
- Start each new trial with a generated password for the preview account. Keep
  credentials and instance state out of build inputs and Git.

# Work Guidance

- Keep the browser integration and upstream Tuxee independent of the kernel.

# Verification

- From `gateway/`, run `go test ./...`. From this repository's root, run
  `docker buildx bake -f tuxee/docker-bake.hcl --load tuxee`.
- On a fresh container, sign into Development, open Tuxee, exercise a desktop
  terminal, GUI app and audio, and switch back to the named terminal. Verify
  that logging out closes the desktop and its open WebSockets.

# Child DOX Index

This document owns the gateway, guest helper and Docker build.
