# 80|20 deployment images

Choose a single system or a connected development/production pair:

| Image | Dockerfile | HTTP ports | SSH ports |
| --- | --- | --- | --- |
| [Single system](#single-system-image) | `Dockerfile` | 80 | 22 |
| [Dev/prod pair](#devprod-image) | `Dockerfile.dev-prod` | 80 / 8080 | 22 / 2222 |

## Get the repository

```sh
git clone https://github.com/the8020/deploy.git
cd deploy
```

Run the commands below from this directory. The release build downloads tagged
kernel and package sources from GitHub; it does not use local workspace sources.

## Single-system image

### Build

```sh
docker build --build-arg VERSION=0.7 --tag the8020:0.7 .
```

`VERSION` is required and must be `major.minor`. For `0.7`, the build selects
the newest `0.7.x` kernel and the newest compatible default packages from the
same major release, up to minor version `7`.

The 0.6.3 installer includes process-preserving development activation. Pull the
current deploy checkout before rebuilding so its build dependencies are present.
Use a fresh test volume when moving from 0.6.2: the new activation stage changes
a database constraint. Retain the previous volume for its data and private work.

Current builds use one common sandbox engine for development, services and jobs.
Its executable payload stays in the image; startup links the node's runtime path
to it and checks it again, including with an existing 0.6.3 data volume.

From kernel 0.7.3, the generic Deno runtime definitions and sandbox images also
ship outside the data volume. On startup, the entrypoint refreshes them in an
existing volume when they differ from the image, so runtime SDK changes reach
upgraded instances. Users, packages, development sandboxes and the database are
left untouched.

The Dockerfile ships the complete built-executable directory, packages, scripts,
Docker runtime assets, and release metadata. It omits the builder's database,
users, node identity, and private keys. First startup creates fresh IDs and keys
for each volume, including when several containers use the same image. New
executables and helper assets do not require individual Dockerfile copy rules.
Image assembly and build-cache cleanup stay in Dockerfiles.

To build a kernel release already cloned locally, use that checkout's own
`Dockerfile`: `docker build --tag the8020 .` needs no build arguments.

### Run

```sh
docker volume create the8020-data
docker run --detach --name the8020 \
  --security-opt seccomp=unconfined \
  --publish 80:80 \
  --publish 22:22 \
  --volume the8020-data:/8020 \
  the8020:0.7
```

Open <http://localhost/> and sign in with `admin` / `admin`.

## Initial credentials for either image

To use different initial credentials, add these options when starting a fresh
data volume:

```sh
--env THE8020_USERNAME=alice \
--env THE8020_PASSWORD='choose-a-password'
```

The credential variables only create the first user. They never modify users
already stored in the volume.

The newly created initial user receives role `**` with `"*" = "*"` through
ordinary auth commands. Interrupted first-user grants resume on restart.
Existing completed volumes retain their role assignments. Releases containing
this integration must also publish a compatible `the8020/auth` package tag.

## Dev/prod image

`Dockerfile.dev-prod` adds a second independent system to the ordinary image.
Dev uses `/8020` and ports **80/22**; prod uses `/8020-prod` and ports
**8080/2222**. Both start through the same first-user bootstrap, with `admin` /
`admin` by default and the same `THE8020_USERNAME` and `THE8020_PASSWORD`
overrides. Their databases, package checkouts, users, and system identities are
separate. Startup sets their names and environment roles to
Development/development and Production/production.

### Build

Use release line `0.7`, with kernel 0.7.4 or newer and the matching updated
packages. These releases include the multi-instance entrypoint, bundled runtime
state, HTTP Git, `deployments.connect`, system-scoped authentication cookies,
and encrypted secret storage. Earlier kernel patches predate the complete pair
implementation.

Build the ordinary base image first, then extend it with the second system:

```sh
docker build --build-arg VERSION=0.7 --tag the8020:dev-prod-base .
docker build --file Dockerfile.dev-prod \
  --build-arg BASE_IMAGE=the8020:dev-prod-base --tag the8020:dev-prod .
```

If you already have a compatible base image, skip the first command and use its
tag for `BASE_IMAGE`. The pair reuses that image's selected packages and runtime
assets.

To build the base from a sibling kernel checkout instead, use the command below
in place of the first build. Its HEAD must have a release tag. It builds local
kernel sources but still downloads tagged packages, so compatible package tags
must already be published:

```sh
docker build --tag the8020:dev-prod-base ../kernel
```

### Run

Use fresh volumes for the first deployment:

```sh
docker run --detach --name the8020-dev-prod \
  --security-opt seccomp=unconfined \
  --cpus=2 --memory=4g \
  --publish 80:80 --publish 22:22 \
  --publish 8080:8080 --publish 2222:2222 \
  --volume the8020-dev:/8020 --volume the8020-prod:/8020-prod \
  the8020:dev-prod
```

Open dev at <http://localhost/> and prod at <http://localhost:8080/>. Each
system uses a cookie named `the8020_auth_sys-<10-character ID>`, so both logins
coexist on the same hostname. SSH uses `ssh -p 22 admin@localhost` for dev and
`ssh -p 2222 admin@localhost` for prod.

### Automatic connections and persistence

Connection initialization runs on the first container startup, after both
systems become ready. It does not run during `docker build`.

Startup connects dev to prod at `http://127.0.0.1:8080` and prod to dev at
`http://127.0.0.1`, using the initial username/password. Dev also records itself
as its development Git source. The ordinary `deployments.connect` command
verifies the remote identity and receives its password through secure stdin.
Passwords are encrypted by the secrets package before database storage.
Interrupted setup retries on startup. Completed volumes retain profiles,
accounts, and connections. Changing bootstrap credentials later does not update
them.

Each system generates and persists its own master key. Cross-system deployment
uses HTTP Basic account credentials and does not require matching master keys.
An explicitly supplied `THE8020_SIGNING_KEY` provisions both systems with that
value through the ordinary kernel contract.

These URLs are internal to the container. Published host ports can be remapped
without changing them. Transport security remains the system owner's choice.
Keep both volumes and their private master-key files when restarting the pair.
Use fresh volumes for this storage contract: legacy UUID profiles and plaintext
secret rows are not migrated. Replacing a master key makes existing encrypted
secrets unreadable; retain the original key to retain access.

The container health check checks both kernels and both HTTP endpoints. Stopping
the container stops both kernels; an unexpected child exit also stops the pair.
Allow normal kernel cleanup with `docker stop --time 60 the8020-dev-prod`.
