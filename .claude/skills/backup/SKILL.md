---
name: backup
description: Set up scheduled database + files backups for a togo app with the backup plugin — configure sources/DB, run/list/restore archives, retention, and schedule nightly backups.
---

# togo backup

Use this skill to add backups to a togo app.

## Configure
Env: `BACKUP_SOURCES` (comma dirs), `BACKUP_DB_DRIVER` (postgres|mysql|sqlite), `BACKUP_DB_DSN`, `BACKUP_DIR`, `BACKUP_KEEP`. Or `backup.FromKernel(k).Configure(backup.Config{...})`.

## Run / restore
```go
b, _ := backup.FromKernel(k)
rec, _ := b.Run(ctx)              // create now
b.List()                          // history (newest first)
b.Restore(rec.ID, "/restore")     // extract; DB restore = run psql/mysql on database.sql
b.Prune()                         // enforce retention
```

## Schedule nightly (with the scheduler plugin)
```go
scheduler.Register("backup", func(ctx context.Context) error { _, e := b.Run(ctx); return e })
sched.Schedule("backup", scheduler.DailyAt(3, 0))
```

## REST
`POST /api/backup/run`, `GET /api/backup/backups`, `GET /api/backup/backups/{id}`.

## Notes
- `pg_dump`/`mysqldump` must be on PATH; sqlite is copied. Store archives off-box (a storage provider / object storage) for real DR.
