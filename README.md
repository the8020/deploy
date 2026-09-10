# 80|20 deployment image

This Dockerfile builds a complete 80|20 image from tagged GitHub releases. It
does not use kernel or package source from your local machine.

## Build

```sh
git clone https://github.com/the8020/deploy.git
cd deploy
docker build --build-arg VERSION=0.6 --tag the8020:0.6 .
```

`VERSION` is required and must be `major.minor`. For `0.6`, the build selects
the newest `0.6.x` kernel and the newest compatible default packages from the
same major release, up to minor version `6`.

The 0.6.3 installer includes process-preserving development activation. Pull the
current deploy checkout before rebuilding so its build dependencies are present.
Use a fresh test volume when moving from 0.6.2: the new activation stage changes
a database constraint. Retain the previous volume for its data and private work.

The Dockerfile combines the complete built-executable directory, Docker runtime
assets, release metadata, and initialized instance. New executables and helper
assets do not require new Dockerfile copy rules. Image assembly and build-cache
cleanup stay in Dockerfiles.

To build a kernel release already cloned locally, use that checkout's own
`Dockerfile`: `docker build --tag the8020 .` needs no build arguments.

## Run

```sh
docker volume create the8020-data
docker run --detach --name the8020 \
  --security-opt seccomp=unconfined \
  --publish 80:80 \
  --publish 22:22 \
  --volume the8020-data:/8020 \
  the8020:0.6
```

Open <http://localhost/> and sign in with `admin` / `admin`.

To use different initial credentials, add these options when starting a fresh
data volume:

```sh
--env THE8020_USERNAME=alice \
--env THE8020_PASSWORD='choose-a-password'
```

The credential variables only create the first user. They never modify users
already stored in the volume.
