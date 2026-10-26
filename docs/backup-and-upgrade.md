# Backup and upgrade

**English** | [Español](../docs-es/backup-and-upgrade.md)

Everything tkt keeps lives in its data directory. Back it up before every upgrade, and follow
this guide to restore it or move to a new version. The directory is described in
[Deployment](deployment.md#the-data-volume).

## What to back up

Copy the whole data directory, not a selection of files.

| File | Why it matters |
| --- | --- |
| `tkt.db` | The SQLite database: tickets, comments, people, the catalog, workflows, settings, and attachments. Attachments live inside the database, not as separate files. |
| `smtp.key` | The key that seals the stored SMTP password and the decision-model API key. It sits next to the database and is created on the first secret save. |
| `tkt.db-wal`, `tkt.db-shm` | SQLite's write-ahead log and shared-memory files. They may exist while the server runs and belong to the database's state. |

`smtp.key` is a lock, not a second copy of the secrets. A restored database without it still
holds every ticket and setting, but the sealed SMTP password and decision-model API key cannot
be opened: root enters them again in **Settings → Email notifications** and **Settings → Tickets
& SLA**. Anyone holding both the database and the key can read those secrets, so keep backups
private.

> [!WARNING]
> A backup without `smtp.key` is incomplete. If the instance ever saved an SMTP password or a
> decision-model API key and no copy of the key exists, those secrets are lost for good.

## Back up a Compose instance

Stop the service so nothing writes to the database, copy the directory through the container,
and start it again:

```console
docker compose stop app
container=$(docker compose ps -aq app)
mkdir -p backup
docker cp "$container:/data" backup/
docker compose start app
```

Copying through the container avoids guessing the volume name Compose generated, such as
`tkt_tkt-data`. Use a new directory per backup so a later run cannot overwrite the previous one.

Check that `tkt.db` is in the copy:

```console
ls -l backup/data
```

## Back up a `docker run` instance

A standalone container keeps the volume name you gave it. For a container named `tkt`:

Stop it, copy the data directory, and run the same check as above:

```console
docker stop tkt
mkdir -p backup
docker cp tkt:/data backup/
docker start tkt
```

If the container is already gone, mount the same volume into a temporary container and copy
from it. Use the image the instance runs: `tkt:local` in the standalone example, or the
   published tag.

```console
docker create --name tkt-backup -v tkt-data:/data tkt:local
docker cp tkt-backup:/data backup/
docker rm tkt-backup
```

## Restore

> [!WARNING]
> A restore deletes the current data. Both procedures below replace the data volume, so anything
> missing from the backup is gone, and a restore from an incomplete backup cannot be undone.
> Confirm that the backup contains `tkt.db` and, when it should exist, `smtp.key`, and keep the
> current instance stopped until the restored one is verified.

The restore commands use GNU tar, the default `tar` on a Linux host.

### Compose

Let Compose recreate the volume it declares instead of naming it.

```console
docker compose down -v
docker compose create app
container=$(docker compose ps -aq app)
tar --owner=65532 --group=65532 -C backup/data -cf - . | docker cp - "$container:/data"
docker compose start app
```

`docker compose down -v` deletes the current volume, which is why the backup comes first.
`docker compose create app` creates it fresh with the `/data` ownership prepared by the image.

The tar stream sets the owner to UID and GID `65532` explicitly, because files copied out of the
container belong to the account that runs Docker; `docker cp -a` alone would carry that local
ownership back. Writing the owner into the stream makes the restore deterministic.

### `docker run`

Recreate the volume with the name the instance uses, fill it, then start the container against
it. Use the same image as the backup helper.

```console
docker stop tkt
docker rm tkt
docker volume rm tkt-data
docker volume create tkt-data
docker create --name tkt-restore -v tkt-data:/data tkt:local
tar --owner=65532 --group=65532 -C backup/data -cf - . | docker cp - tkt-restore:/data
docker rm tkt-restore
```

Start it again with the same options as before; the volume name, port, and flags come from
[Deployment](deployment.md#run-it). Only the contents changed.

### Check the restore

```console
docker compose ps
curl -fsS http://127.0.0.1:8080/healthz
```

The people and tickets from the backup are there when you sign in. What the health output means
is in [Deployment](deployment.md#check-that-it-is-running).

### Permissions

A bind mount needs a host directory readable and writable by UID `65532`;
`chown -R 65532:65532` sets it. Running the server as another user is not supported.

## Upgrade

Images come from the release tags described in
[Deployment](deployment.md#the-container-image); this guide assumes no version number.

1. **Back up first.** Run the backup procedure for your installation. An upgrade can apply
   database migrations, and the backup is the way back.
2. **Get the new image.** For a published release, set the tag and recreate the container:

   ```console
   TKT_TAG=<release-tag> docker compose pull
   TKT_TAG=<release-tag> docker compose up -d
   ```

   Replace `<release-tag>` with the tag of the release you are installing.

   For a checkout, fetch the new source and rebuild:

   ```console
   git fetch
   git checkout <tag-or-commit>
   docker compose up -d --build
   ```

3. **Watch it start.** The server applies the migrations it has not seen, each in its own
   transaction. A failing migration stops startup instead of serving a half-migrated database;
   check `docker compose logs app` and restore the backup if it fails.
4. **Verify.** A ticket and the settings pages render after signing in; for the health output,
   see [Deployment](deployment.md#check-that-it-is-running).

### Rollback

There are no down migrations, and an older image cannot run against a database that a newer
version already migrated.

To go back, keep the tag you were running, restore the backup taken before the upgrade, and run
the previous image against the restored data.
