# backup — usage

## Configure
Via env (`BACKUP_SOURCES`, `BACKUP_DB_DRIVER`, `BACKUP_DB_DSN`, `BACKUP_DIR`, `BACKUP_KEEP`) or:
```go
b, _ := backup.FromKernel(k)
b.Configure(backup.Config{Sources: []string{"uploads"}, DBDriver:"postgres", DBDSN:dsn, Dir:"backups", Keep:7})
```

## Run / list / restore / prune
```go
rec, _ := b.Run(ctx)            // bundles DB dump + sources into Dir/<id>.tar.gz
b.List()                        // []*Backup, newest first
b.Restore(rec.ID, "/restore")   // extract archive (DB restore = run psql/mysql on database.sql)
b.Prune()                       // keep newest Keep, delete older
```

## Schedule with the scheduler plugin
```go
scheduler.Register("backup", func(ctx context.Context) error { _, e := b.Run(ctx); return e })
sched.Schedule("backup", scheduler.DailyAt(3, 0))
```

## REST
| Method | Path |
|---|---|
| POST | `/api/backup/run` |
| GET | `/api/backup/backups` |
| GET | `/api/backup/backups/{id}` |

## Notes
- `pg_dump`/`mysqldump` must be installed for those drivers; sqlite is file-copied.
- Archives are gzip tar; extraction skips `..` path-traversal entries.
- The DB dump lands as `database.sql` inside the archive — restore it manually.
