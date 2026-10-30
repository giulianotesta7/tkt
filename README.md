<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/images/banner-dark.svg">
  <img src="docs/images/banner-light.svg" alt="tkt: your platform. Your rules. Your infrastructure. A self-hosted help desk management platform." width="1280">
</picture>

# tkt

A self-hosted help desk management platform.

**English** | [Español](README.es.md)

[![Go quality](https://github.com/giulianotesta7/tkt/actions/workflows/quality.yml/badge.svg)](https://github.com/giulianotesta7/tkt/actions/workflows/quality.yml)
[![E2E](https://github.com/giulianotesta7/tkt/actions/workflows/e2e.yml/badge.svg)](https://github.com/giulianotesta7/tkt/actions/workflows/e2e.yml)
[![Container](https://github.com/giulianotesta7/tkt/actions/workflows/container.yml/badge.svg)](https://github.com/giulianotesta7/tkt/actions/workflows/container.yml)
[![Go security](https://github.com/giulianotesta7/tkt/actions/workflows/security.yml/badge.svg)](https://github.com/giulianotesta7/tkt/actions/workflows/security.yml)

</div>

A shared ticket queue in one Go binary, with embedded SQLite and server-rendered HTML
with HTMX. No external database, cache, message broker or JavaScript framework is required.
One data volume holds everything, and upgrading replaces the image.

<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/images/my-work-dark.webp">
  <img src="docs/images/my-work-light.webp" alt="tkt My work: tickets assigned to me and tickets available to claim" width="1440">
</picture>

</div>

## Performance snapshot

| Measurement | Result |
| --- | --- |
| Uncompressed image | 31.4 MB |
| Median startup, fresh / existing database | 0.54 / 0.41 s |
| Container memory, idle after reads / sampled load maximum | 172.4 / 180.3 MiB |
| Median agent queue HTTP, concurrency 1 / 5 | 6.1 / 21.3 ms |
| Median browser visible-queue marker, cold / warm cache | 120 / 124 ms |

Local measurements, not performance guarantees.

## Capabilities

- **Per-category workflows.** Each category runs the steps you design: requester and agent
  forms, manual tasks, assignment to a desk, resolution and closure, with an audit trail.
- **Roles and desks.** Four roles (user, agent, admin, root); desk membership decides who
  works each queue.
- **SLA on a working calendar.** Per-category targets in working hours and days; hold, pause
  and resume shift the commitment.
- **Notifications.** An in-app bell for everyone, plus optional email through your own SMTP
  server.
- **Search, filters, CSV export and attachments** to work a queue and keep the evidence on
  the ticket.
- **Self-service passwords.** People change their own password and, when email is configured,
  recover it from the sign-in page.
- **Category suggestions.** A local keyword engine proposes categories from the description,
  or an optional decision-model endpoint when one is configured.
- **English and Spanish interface**, your organization's name and logo, and one interface
  language for the whole instance.

## Quick start

### Docker Compose (recommended)

```console
git clone https://github.com/giulianotesta7/tkt.git
cd tkt
docker compose up -d --build
```

Compose builds the image from this checkout.

### Build from source

```console
git clone https://github.com/giulianotesta7/tkt.git
cd tkt
mkdir -p data bin
CGO_ENABLED=0 go build -o bin/tkt ./cmd/server
TKT_LISTEN=127.0.0.1:8080 TKT_DB_PATH=data/tkt.db ./bin/tkt
```

The server runs in the foreground in that shell.

### First run

Create the first account at `http://localhost:8080/setup`, which becomes root, then publish a
category workflow so the queue takes its first ticket. [Getting started](docs/getting-started.md)
has the details.

## Documentation

| Guide | What it covers |
| --- | --- |
| [Install and set up tkt](docs/getting-started.md) | Root account, minimum configuration |
| [Deploy tkt](docs/deployment.md) | Image, data volume, public base URL, HTTPS proxy, configuration |
| [Administer tkt](docs/administration.md) | Users and roles, desks, catalog, workflows, SLA, email, Security |
| [API v1 reference](docs/api.md) | Bearer authentication, scopes, JSON endpoints, errors, curl examples |
| [Work a ticket](docs/user-guide.md) | Requester and agent journeys: create, reply, hold, close |
| [Back up and upgrade](docs/backup-and-upgrade.md) | What to back up, restore, upgrade |
| [Contribute](CONTRIBUTING.md) | Local development, test layers, how to contribute |

## Contributing

Issues and pull requests are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

tkt is licensed under the Apache License 2.0. See [LICENSE](LICENSE).

The bundled Inter font is third-party software under the SIL Open Font License 1.1. See
[its license](web/templates/static/InterVariable-LICENSE.txt).
