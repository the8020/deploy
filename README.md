# 80|20 deployment image

Build a release image from GitHub without using local kernel or package source:

```sh
docker build --build-arg VERSION=0.1 -t the8020:0.1 .
```

`VERSION` is a two-number release line. `0.1` selects the newest `0.1.x`
kernel. Each default package selects its newest compatible tag with the same
major and a minor no newer than the requested minor.

Run the image on HTTP port 80 and SSH port 22:

```sh
docker volume create the8020-data
docker run --name the8020 \
  --security-opt seccomp=unconfined \
  -p 80:80 \
  -p 22:22 \
  -v the8020-data:/8020 \
  the8020:0.1
```

Open <http://localhost/> and sign in with `admin` / `admin`.

For a new data volume, override the initial user with:

```sh
docker run --name the8020 \
  --security-opt seccomp=unconfined \
  -p 80:80 \
  -p 22:22 \
  -v the8020-data:/8020 \
  -e THE8020_USERNAME=alice \
  -e THE8020_PASSWORD='choose-a-password' \
  the8020:0.1
```

Existing users are never changed by these environment variables.
