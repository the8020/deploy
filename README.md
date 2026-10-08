# Run 80|20 with Docker

Choose one system or a connected development/production pair.

```sh
git clone https://github.com/the8020/deploy.git
cd deploy
```

## Single system

```sh
docker build --build-arg VERSION=0.7 -t the8020 .
docker run -p 8888:9080 -p 8443:443 -p 2222:22 the8020
```

Open <http://localhost:8888/> and sign in with **admin / admin**.
HTTPS is at <https://localhost:8443/>; SSH uses port 2222.

## Development + production

Two independent systems, connected automatically.

```sh
docker build -f Dockerfile.dev-prod --build-arg VERSION=0.7 -t the8020:dev-prod .
docker run -p 8888:9080 -p 8443:443 -p 2222:22 \
  -p 8889:9081 -p 8444:8443 -p 2223:2222 the8020:dev-prod
```

| System | HTTP | HTTPS | SSH port |
| --- | --- | --- | --- |
| Development | <http://localhost:8888/> | <https://localhost:8443/> | 2222 |
| Production | <http://localhost:8889/> | <https://localhost:8444/> | 2223 |

Sign in with **admin / admin** on either system.

The commands run in the foreground; press Ctrl+C to stop. Each new container
starts fresh. In `-p 8888:9080`, `8888` is your computer's port.

## HTTPS

On a VPS, use `-p 80:9080 -p 443:443` for the main HTTP/HTTPS bindings and point
your domain at it. Certificates are issued and renewed automatically.

Local HTTPS uses a local CA, so your browser will warn until you trust it.
For an IP address, set `tls.ip_address` in [https/https.toml](https/https.toml).

Edit that TOML before building to change defaults. In a running container it is
at `/etc/the8020/https.toml`; enrollment limits update without a restart.
`THE8020_HTTPS_DIR` selects certificate storage, default `/var/lib/the8020/https`.

## Other options

- Change the initial login by adding `-e THE8020_USERNAME=alice -e THE8020_PASSWORD='choose-a-password'`.
- Sandbox startup under Docker's default seccomp policy requires
  `--security-opt seccomp=unconfined`.

More configuration and checks: [deployment](AGENTS.md), [HTTPS](https/AGENTS.md).
