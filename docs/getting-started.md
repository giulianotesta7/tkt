# Getting started

**English** | [Español](../docs-es/getting-started.md)

From an empty host to a first ticket: install tkt, create the root account, and apply the
minimum configuration. The container contract and the supported settings are in
[Deployment](deployment.md); day-to-day administration is in [Administration](administration.md).

## Before you start

- Docker and Docker Compose.
- A port reachable from a browser. The examples use `8080`, the port the container listens on.

Running from source is possible; see [CONTRIBUTING](../CONTRIBUTING.md).

## 1. Start tkt

```console
docker compose up -d --build
```

Compose builds the image, publishes `8080` and keeps the data in the `tkt-data` volume;
[Deployment](deployment.md#the-data-volume) covers the volume and its ownership.

```console
docker compose ps
curl -fsS http://localhost:8080/healthz
```

`/healthz` answers `ok`; [Deployment](deployment.md#check-that-it-is-running) covers the
container healthcheck.

## 2. Create the root account

Open `http://localhost:8080/setup`. The form exists only until the first account is created;
later visits redirect to the sign-in page.

- **Name**: how the account appears on tickets and in the audit trail.
- **Password**: 12 characters minimum by default, 72 bytes maximum. The minimum is configurable
  later, between 8 and 64 characters.

Sign in with those credentials.

## 3. Minimum configuration

Sign in as root.

1. **Settings → Organization**: the name in the interface header, the help desk name and the
   support address.
2. **Settings → Language**: one interface language for the whole instance, applied immediately.
3. **Catalog**: a department groups desks, a desk groups categories and their agents, and a
   category groups requests that follow one workflow. Create a department, a desk and a category,
   open the category's workflow, add a step and publish it.
4. **Users**: create the accounts that work on tickets, give them the **Agent** role, and add them
   to the desk. People who only report problems need the **User** role and no desk.
5. **Settings → Security**: the minimum password length and the two self-service switches (change
   from Preferences, recovery on the sign-in page); recovery needs email notifications.
   [Administration](administration.md#security) covers the rules.
6. **Settings → Email notifications** (root only, optional): an SMTP server, a sender address and
   the **TKT address**; [Deployment](deployment.md#the-public-base-url) explains the address.

Verify by opening **New ticket**: the published category is in the picker.

## 4. Create a first ticket

Sign out, sign in as someone with the **User** role, open **New ticket**, pick the published
category, write a title and a description, and submit.

Verify that the ticket appears in that person's list and that the desk members see it in their
queue as the workflow assigns it.

## Next steps

- [Deployment](deployment.md): the image, the data volume, the public base URL, and HTTPS
  behind a reverse proxy.
- [Administration](administration.md): users and roles, desks, categories, workflows, SLA,
  email and the Security settings.
- [User guide](user-guide.md): the requester and agent journeys.
- [Backup and upgrade](backup-and-upgrade.md): what to back up, how to restore, and how to
  move to a new version.
