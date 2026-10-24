# Run 80|20 with Docker

Choose one system or a connected development/production pair.
Builds use `--no-cache` to resolve the latest tagged kernel in the selected line.

```sh
git clone https://github.com/the8020/deploy.git
cd deploy
```

## Single system

```sh
docker build --no-cache --build-arg VERSION=0.9 -t the8020 .
docker run -p 8888:9080 -p 8443:443 -p 2222:22 the8020
```

Open <http://localhost:8888/> and sign in with **admin / admin**.
HTTPS is at <https://localhost:8443/>; SSH uses port 2222.

## Development + production

Two independent systems, connected automatically.

```sh
docker build --no-cache -f Dockerfile.dev-prod --build-arg VERSION=0.9 -t the8020:dev-prod .
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
Open **HTTPS** in the system to configure allowed domains, Let's Encrypt,
enrollment limits, uploaded certificates, and an IP address. Its advanced view
provides the public local CA certificate. Settings and certificates are stored
with the system database; private keys use the system's encrypted secret storage.

## Existing installations

For bundled Caddy installations running kernel 0.7.4 or newer, follow the
[0.9.1 update instructions](https://github.com/the8020/kernel/blob/0.9.1/README.md#development-updates).
Update both instances of a dev/prod pair before restarting the container once.
The restart imports existing certificates and settings and transfers Caddy's
ports to the kernel.

## Other options

- Change the initial login by adding `-e THE8020_USERNAME=alice -e THE8020_PASSWORD='choose-a-password'`.
- Sandbox startup under Docker's default seccomp policy requires
  `--security-opt seccomp=unconfined`.

More configuration and checks: [deployment](AGENTS.md), [HTTPS](https/AGENTS.md).
