<div align="center">
  <img src=".github/assets/togo-mark.svg" alt="togo" height="64" />
  <h1>togo-framework/backup</h1>
  <p>
    <a href="https://to-go.dev/marketplace"><img src="https://img.shields.io/badge/marketplace-to--go.dev-1FC7DC" alt="marketplace" /></a>
    <a href="https://pkg.go.dev/github.com/togo-framework/backup"><img src="https://pkg.go.dev/badge/github.com/togo-framework/backup.svg" alt="pkg.go.dev" /></a>
    <img src="https://img.shields.io/badge/license-MIT-blue" alt="MIT" />
  </p>
  <p><strong>Scheduled DB + files backups for <a href="https://to-go.dev">togo</a> — archive, retain, restore.</strong></p>
</div>

## Install

```bash
togo install togo-framework/backup
```

The togo answer to **Spatie Backup**. Bundle a database dump + configured files into a single `.tar.gz`, keep the newest N, and restore on demand. Pair with [`scheduler`](https://to-go.dev/plugins/scheduler) to back up on a cadence.

## Configuration

| Env | Description |
|---|---|
| `BACKUP_SOURCES` | comma-separated directories/files to include |
| `BACKUP_DB_DRIVER` | `postgres` \| `mysql` \| `sqlite` (omit to skip the DB) |
| `BACKUP_DB_DSN` | DB connection string, or the sqlite file path |
| `BACKUP_DIR` | output directory for archives (default `backups`) |
| `BACKUP_KEEP` | retention — keep the newest N archives (0 = keep all) |

Or configure in code:

```go
b, _ := backup.FromKernel(k)
b.Configure(backup.Config{
    Sources:  []string{"storage/app", "uploads"},
    DBDriver: "postgres", DBDSN: os.Getenv("DATABASE_URL"),
    Dir:      "backups", Keep: 7,
})
```

## Usage

```go
rec, err := b.Run(ctx)        // create a backup now → *Backup{ID, Path, Size, ...}
list := b.List()              // newest first
b.Restore(rec.ID, "/restore") // extract the archive (DB restore is a manual psql/mysql step)
b.Prune()                     // enforce retention
```

### Schedule it

```go
scheduler.Register("backup", func(ctx context.Context) error {
    b, _ := backup.FromKernel(k); _, err := b.Run(ctx); return err
})
sched.Schedule("backup", scheduler.DailyAt(3, 0)) // 03:00 nightly
```

## REST API

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/backup/run` | create a backup now |
| `GET` | `/api/backup/backups` | list backups (newest first) |
| `GET` | `/api/backup/backups/{id}` | one backup record |

> `pg_dump`/`mysqldump` must be on `PATH` for the respective drivers; sqlite is copied directly. Archives extract path-traversal-safe.

---

<div align="center">
  <h3>Premium sponsors</h3>
  <p>
    <a href="https://id8media.com"><strong>ID8 Media</strong></a> &nbsp;·&nbsp;
    <a href="https://one-studio.co"><strong>One Studio</strong></a>
  </p>
  <p><sub>Support togo — <a href="https://github.com/sponsors/fadymondy">become a sponsor</a>.</sub></p>
</div>
