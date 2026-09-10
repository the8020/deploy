Parent DOX: [8020 workspace](../AGENTS.md).

Framework source:
[agent0ai/dox/AGENTS.md](https://github.com/agent0ai/dox/blob/765ae4ac02cc884eefcd41a3d0f71941721adb89/AGENTS.md).

# DOX framework

- DOX is highly performant AGENTS.md hierarchy installed here
- Agent must follow DOX instructions across any edits

## Core Contract

- AGENTS.md files are binding work contracts for their subtrees
- Work products, source materials, instructions, records, assets, and durable
  docs must stay understandable from the nearest applicable AGENTS.md plus every
  parent AGENTS.md above it

## Read Before Editing

1. Read the root AGENTS.md
2. Identify every file or folder you expect to touch
3. Walk from the repository root to each target path
4. Read every AGENTS.md found along each route
5. If a parent AGENTS.md lists a child AGENTS.md whose scope contains the path,
   read that child and continue from there
6. Use the nearest AGENTS.md as the local contract and parent docs for repo-wide
   rules
7. If docs conflict, the closer doc controls local work details, but no child
   doc may weaken DOX

Do not rely on memory. Re-read the applicable DOX chain in the current session
before editing.

## Update After Editing

Every meaningful change requires a DOX pass before the task is done.

Update the closest owning AGENTS.md when a change affects:

- purpose, scope, ownership, or responsibilities
- durable structure, contracts, workflows, or operating rules
- required inputs, outputs, permissions, constraints, side effects, or artifacts
- user preferences about behavior, communication, process, organization, or
  quality
- AGENTS.md creation, deletion, move, rename, or index contents

Update parent docs when parent-level structure, ownership, workflow, or child
index changes. Update child docs when parent changes alter local rules. Remove
stale or contradictory text immediately. Small edits that do not change behavior
or contracts may leave docs unchanged, but the DOX pass still must happen.

## Hierarchy

- Root AGENTS.md is the DOX rail: project-wide instructions, global preferences,
  durable workflow rules, and the top-level Child DOX Index
- Child AGENTS.md files own domain-specific instructions and their own Child DOX
  Index
- Each parent explains what its direct children cover and what stays owned by
  the parent
- The closer a doc is to the work, the more specific and practical it must be

## Child Doc Shape

- Create a child AGENTS.md when a folder becomes a durable boundary with its own
  purpose, rules, responsibilities, workflow, materials, or quality standards
- Work Guidance must reflect the current standards of the project or user
  instructions; if there are no specific standards or instructions yet, leave it
  empty
- Verification must reflect an existing check; if no verification framework
  exists yet, leave it empty and update it when one exists

Default section order:

- Purpose
- Ownership
- Local Contracts
- Work Guidance
- Verification
- Child DOX Index

## Style

- Keep docs concise, current, and operational
- Document stable contracts, not diary entries
- Put broad rules in parent docs and concrete details in child docs
- Prefer direct bullets with explicit names
- Do not duplicate rules across many files unless each scope needs a local
  version
- Delete stale notes instead of explaining history
- Trim obvious statements, repeated rules, misplaced detail, and warnings for
  risks that no longer exist

## Closeout

1. Re-check changed paths against the DOX chain
2. Update nearest owning docs and any affected parents or children
3. Refresh every affected Child DOX Index
4. Remove stale or contradictory text
5. Run existing verification when relevant
6. Report any docs intentionally left unchanged and why

## User Preferences

When the user requests a durable behavior change, record it here or in the
relevant child AGENTS.md

- Keep the Dockerfile universal across release lines: copy semantic artifact
  directories. Dockerfiles own deployment assembly and disposable build-cache
  cleanup; keep those specifications out of the kernel installer. Do not
  enumerate individual binaries, helper files, or runtime modules in copies.

## Child DOX Index

This root retains repository-wide contracts and files outside the child scopes
below.

No child DOX documents. This document owns the entire local scope.

# Purpose

- Build the release-based 80|20 deployment image from tagged GitHub
  repositories.

# Ownership

- Own `Dockerfile` and the documented build/run workflow in `README.md`.
- The kernel repository owns installation, image materialization, the
  entrypoint, and runtime health behavior; packages own application sources and
  compatible release tags.

# Local Contracts

- Require `VERSION=<major.minor>` without leading zeroes and select the newest
  kernel patch in that exact release line.
- Resolve compatible first-party package tags through the selected kernel's
  ordinary installer. Never copy local kernel or package sources into the
  release build.
- The build stage supplies Python 3 for the kernel's workspace prototype build.
  Its complete executable payload includes the process-preserving development
  runtime; development activation must not fall back to the legacy build.
- Build and initialize `/8020`, retain selected release metadata, and persist
  runtime data through the `/8020` volume.
- Copy the selected kernel's complete `.development/bin/` and `docker/rootfs/`
  payloads, the release-metadata directory, and the initialized instance. The
  Dockerfile owns destination paths and build-cache cleanup. The kernel's local
  Dockerfile builds its tagged checkout without a remote version selector.
- Container execution requires the documented unconfined outer seccomp profile
  for nested rootless gVisor; publish HTTP and SSH ports as documented.
- Initial username/password environment values affect only first-user bootstrap
  on a fresh volume. Existing users are preserved.
- The runtime image includes curl. The kernel-owned entrypoint announces
  readiness only after creating or preserving the initial login user and
  receiving HTTP 200 from the public login service on the configured main port.

# Work Guidance

- Compose deployment from the kernel installer and independently released
  packages. Keep application behavior in those packages; do not patch it into
  the image build or change the kernel to compensate for a package defect.
- Keep release metadata authoritative and build artifacts derived. Verify
  deployment fixes through the existing fresh-volume smoke at the layer that
  owns the failure.

- Keep release selection and build/run documentation aligned with the Dockerfile
  and the owning kernel installation contract.

# Verification

- `README.md` documents the Docker build and fresh-volume run smoke; use an
  existing release line for `VERSION`.
- The image health check invokes `admin --root /8020 kernel.status`.
