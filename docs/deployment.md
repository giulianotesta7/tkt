# Deployment

**English** | [Español](../docs-es/deployment.md)

Running tkt as a container: the image, the data volume, configuration, the public base URL,
and a reverse proxy with HTTPS. A first install is [Getting started](getting-started.md).

## The container image

The `Dockerfile` compiles the server as a static binary and copies it into a distroless base.
The image owns the runtime contract, so every way of running it gets the same defaults:

| Property | Value |
| --- | --- |
| Listens on | `:8080` (`TKT_LISTEN`), port `8080` exposed |
| Database | `/data/tkt.db` (`TKT_DB_PATH`) |
| Runs as | UID `65532`, an unprivileged account |
| Data directory | `/data`, created and owned by `65532` in the image |
| Stop signal | `SIGTERM`, handled as a graceful shutdown |
| Healthcheck | `/server -healthcheck` every 10 seconds, 3-second timeout, 3 retries, 5-second start period |

There is no shell or package manager inside: inspect the container instead of running commands
in it.

Tags: a `vX.Y.Z` tag publishes `ghcr.io/giulianotesta7/tkt` with the full version, the minor
version and `latest`. Nothing rolls from the main branch, so `latest` moves only on a release,
and until a tag exists you build the image from a checkout.

## Run it

### With Compose

```console
docker compose up -d --build
```

It publishes `8080` on the host, mounts the Compose volume `tkt-data` at `/data`, and sets the
restart policy to `unless-stopped`. The image reference is
`ghcr.io/giulianotesta7/tkt:${TKT_TAG:-latest}`: set `TKT_TAG` to pin a release. With the default
project name `tkt` Docker names the volume
`tkt_tkt-data`; the standalone example below creates a different volume named `tkt-data`.

The mapping `8080:8080` publishes on every interface, which suits a local trial. Behind a
reverse proxy, change it to `127.0.0.1:8080:8080` so the plain-HTTP port is reachable only from
the proxy.

### With `docker run`

```console
docker volume create tkt-data
docker build -t tkt:local .
docker run -d \
  --name tkt \
  --restart unless-stopped \
  -p 127.0.0.1:8080:8080 \
  -v tkt-data:/data \
  tkt:local
```

## Check that it is running

```console
docker ps
curl -fsS http://127.0.0.1:8080/healthz
```

`/healthz` answers `ok` without authentication. The container healthcheck proves the database
is reachable: it runs the server binary with `-healthcheck`, which opens the database and runs a
query, and `docker ps` shows `healthy` when it passes.

## The data volume

Everything tkt must keep lives in `/data`:

| File | Holds | Back up |
| --- | --- | --- |
| `tkt.db` | Tickets, people, the catalog and settings | Yes, together with `smtp.key` |
| `smtp.key` | The key that seals stored secrets, written on the first save of an SMTP password or a decision-model key | Yes, together with `tkt.db` |

> [!WARNING]
> Restoring `tkt.db` without `smtp.key` leaves stored secrets unreadable.
> [Backup and upgrade](backup-and-upgrade.md#what-to-back-up) has the procedure.

The database uses write-ahead logging, so `tkt.db-wal` and `tkt.db-shm` may appear next to it
while the server runs. A named volume inherits the prepared ownership of `/data`, so the
unprivileged UID can create the database on first start. A bind mount does not: the host
directory must be writable by UID `65532`.

## Configuration

tkt reads two environment variables. The image sets both, so setting either only overrides the
default:

| Variable | Image default | Meaning |
| --- | --- | --- |
| `TKT_DB_PATH` | `/data/tkt.db` | Path of the SQLite database file |
| `TKT_LISTEN` | `:8080` | Address the HTTP server listens on |

Everything else is configured in the interface and stored in the database:

| Screen | Configures |
| --- | --- |
| Settings → Organization | Instance name, help desk name, support address, logo |
| Settings → Sign-in page | The presentation of the sign-in page |
| Settings → Language | The instance-wide interface language |
| Settings → Tickets & SLA | Appearance, SLA on or off, hold auto-close |
| Settings → Email notifications (root only) | SMTP server, encryption, sender, TKT address |
| Settings → Working calendar | Timezone, working days and hours the SLA clock runs on |
| Settings → Security | New-password minimum, self-service password change and recovery |

[Administration](administration.md) explains what each setting does.

### CLI flags

The server binary takes two one-shot flags:

- `-healthcheck` opens the database, runs a query, and exits `0` or `1`. The image's
  `HEALTHCHECK` uses it.
- `-recover-root <id>` activates the account with that id and promotes it to root for a
  database whose root cannot be proven, audits the recovery, and exits. It fails closed when a
  root already exists or the id is unknown, and it is not a general password reset.

Run a one-shot command with the server stopped:

```console
docker stop tkt
docker run --rm -v tkt-data:/data --entrypoint /server tkt:local -recover-root 1
docker start tkt
```

Replace `1` with the intended account ID, or use the Compose service's volume and image.
Investigate the error before starting the server again.

## The public base URL

Email links have to be absolute, so tkt needs the address people use to reach it: the
**TKT address** field in Settings → Email notifications. It is required once email notifications
are on, and the saved value is validated as an absolute `http` or `https` URL and stored without a
trailing slash.

That address is used for ticket links in notification emails and for the single-use link in a
password recovery message. Set it to the public address, for example
`https://tkt.example.com`, never to `localhost`.

## Reverse proxy with HTTPS

tkt speaks plain HTTP and never terminates TLS, so the proxy in front holds the certificate.

1. Preserve the original `Host` header, port included: tkt compares it with the browser's
   `Origin` header on state-changing requests and refuses a mismatch with `403`. nginx's
   `$http_host` keeps the port, while `$host` drops it.
2. Restrict the container port to the proxy's interface, `127.0.0.1` or a private network.
3. Set the TKT address to the public HTTPS address, so emailed links never point at the proxy's
   backend.

An nginx server block:

```nginx
server {
    listen 443 ssl;
    server_name tkt.example.com;

    ssl_certificate     /etc/ssl/certs/tkt.example.com.pem;
    ssl_certificate_key /etc/ssl/private/tkt.example.com.key;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $http_host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 60s;
    }
}

server {
    listen 80;
    server_name tkt.example.com;
    return 301 https://$host$request_uri;
}
```

> [!WARNING]
> The server sees plain HTTP and cannot mark the sign-in cookie `Secure`; the cookie is
> `HttpOnly` and `SameSite=Strict`. Enable HSTS at the proxy and never expose the container
> directly to the internet.
