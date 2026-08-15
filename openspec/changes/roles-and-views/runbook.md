# Runbook: SQLite Backup Before Deploy (Roles and Views)

**Applies to:** every deploy that ships migration `0003_roles_and_views.sql`
(and the binary that runs it). The migration mutates existing rows: legacy
users get a role and legacy tickets may get a requester. A backup is the
only guaranteed rollback for a bad migration run.

## 1. Back up before deploy (mandatory)

The database uses WAL journaling, so the live data lives in `app.db` plus
`app.db-wal` (and `app.db-shm`). Copying only `app.db` can miss the latest
committed pages.

**Stopped server (recommended, only guaranteed consistent copy):** stop the
old server first, then copy all three files:

```bash
cp -p data/tkt.db          data/backups/tkt.db.$(date -u +%Y%m%dT%H%M%SZ)
cp -p data/tkt.db-wal      data/backups/tkt.db-wal.$(date -u +%Y%m%dT%H%M%SZ) 2>/dev/null || true
cp -p data/tkt.db-shm      data/backups/tkt.db-shm.$(date -u +%Y%m%dT%H%M%SZ) 2>/dev/null || true
```

**Live server (only with SQLite's online backup):** a `wal_checkpoint(FULL)`
does NOT prevent new writes, so copying `db`, `wal`, and `shm` separately is
NOT a consistent snapshot and must not be used as a rollback backup. Use the
online backup API instead, which produces a single consistent snapshot file:

```bash
sqlite3 data/tkt.db ".backup data/backups/tkt.db.$(date -u +%Y%m%dT%H%M%SZ)"
```

Adjust the paths to the actual database file (see `data/` in this repo).

Verify the backup is readable and contains the expected schema version:

```bash
sqlite3 data/backups/tkt.db.<stamp> "SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 3;"
```

## 2. Deploy migration + binary together

Ship `0003` (embedded in the new binary) and the binary as one unit. The
migration runner records each applied version in `schema_migrations`
inside the migration's own transaction; the legacy backfill (root
promotion from reliable id=1, requester backfill) runs immediately after
the migration and is idempotent.

## 3. Fail-closed recovery path

When a legacy database cannot prove the original setup user (users exist
but id=1 is gone), startup fails closed with a `-recover-root` error —
the operator must explicitly select the root identity. Do not work around
the failure; restore the backup and run the recovery flow (`-recover-root`
lands in the next slice) before serving ambiguous databases.

## 4. Rollback

Restore the backup files and the previous binary:

```bash
cp -p data/backups/tkt.db.<stamp> data/tkt.db
rm -f data/tkt.db-wal data/tkt.db-shm   # stale WAL must not replay onto the restored db
```

Start the previous binary. Confirm the app boots and the ticket list
matches the pre-deploy state.

## 5. Post-deploy smoke check

- One user has `role='root'` (the reliable legacy setup user) and every
  other legacy user has `role='agent'`.
- Tickets whose creator is provable from a single creation event have a
  `requester_user_id`; the rest are NULL (agent+-only).
- `schema_migrations` lists versions 1, 2, 3.
