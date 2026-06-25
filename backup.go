// Package backup creates scheduled .tar.gz backups of a togo app's database
// and configured files (the togo answer to Spatie Backup).
//
// A backup bundles an optional database dump (pg_dump / mysqldump / sqlite copy)
// plus configured source directories into a single gzip-compressed tar archive
// written to a destination directory. Retention prunes old archives, and run
// records are queryable over a Go + REST API. Pair it with the scheduler plugin
// to run backups on a cadence.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/togo-framework/togo"
)

// Config controls what is backed up and where.
type Config struct {
	Sources  []string // directories/files to include
	DBDriver string   // "postgres" | "mysql" | "sqlite" (empty = no DB dump)
	DBDSN    string   // connection string, or a sqlite file path
	Dir      string   // output directory for archives (default "backups")
	Keep     int      // retention: keep the newest N archives (0 = keep all)
}

// Backup is a record of one archive.
type Backup struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	Status    string    `json:"status"` // done | failed
	WithDB    bool      `json:"with_db"`
	CreatedAt time.Time `json:"created_at"`
	Error     string    `json:"error,omitempty"`
}

// Service is the backup runtime stored on the kernel (k.Get("backup")).
type Service struct {
	k    *togo.Kernel
	mu   sync.Mutex
	cfg  Config
	recs []*Backup
	seq  int
}

func init() {
	togo.RegisterProviderFunc("backup", togo.PriorityService, func(k *togo.Kernel) error {
		s := &Service{k: k, cfg: configFromEnv()}
		k.Set("backup", s)
		if k.Router != nil {
			s.mountRoutes(k.Router)
		}
		return nil
	})
}

// FromKernel returns the backup Service.
func FromKernel(k *togo.Kernel) (*Service, bool) {
	v, ok := k.Get("backup")
	if !ok {
		return nil, false
	}
	s, ok := v.(*Service)
	return s, ok
}

// Configure replaces the backup configuration.
func (s *Service) Configure(c Config) *Service {
	if c.Dir == "" {
		c.Dir = "backups"
	}
	s.mu.Lock()
	s.cfg = c
	s.mu.Unlock()
	return s
}

func configFromEnv() Config {
	c := Config{
		DBDriver: os.Getenv("BACKUP_DB_DRIVER"),
		DBDSN:    os.Getenv("BACKUP_DB_DSN"),
		Dir:      os.Getenv("BACKUP_DIR"),
	}
	if c.Dir == "" {
		c.Dir = "backups"
	}
	if v := os.Getenv("BACKUP_SOURCES"); v != "" {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				c.Sources = append(c.Sources, p)
			}
		}
	}
	if v := os.Getenv("BACKUP_KEEP"); v != "" {
		c.Keep, _ = strconv.Atoi(v)
	}
	return c
}

// Run creates a backup archive (DB dump + configured sources) and records it.
func (s *Service) Run(ctx context.Context) (*Backup, error) {
	s.mu.Lock()
	cfg := s.cfg
	s.seq++
	id := fmt.Sprintf("bkp_%s_%d", time.Now().UTC().Format("20060102T150405"), s.seq)
	s.mu.Unlock()

	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return s.record(id, "", false, err), err
	}

	sources := append([]string{}, cfg.Sources...)
	withDB := false
	if cfg.DBDriver != "" && cfg.DBDSN != "" {
		dumpDir, err := os.MkdirTemp("", "backup-db-")
		if err == nil {
			defer os.RemoveAll(dumpDir)
			if derr := dbDump(ctx, cfg, dumpDir); derr != nil {
				return s.record(id, "", false, derr), derr
			}
			sources = append(sources, dumpDir)
			withDB = true
		}
	}

	name := id + ".tar.gz"
	out := filepath.Join(cfg.Dir, name)
	f, err := os.Create(out)
	if err != nil {
		return s.record(id, "", withDB, err), err
	}
	size, werr := writeArchive(f, sources)
	cerr := f.Close()
	if werr != nil {
		os.Remove(out)
		return s.record(id, "", withDB, werr), werr
	}
	_ = cerr

	rec := &Backup{ID: id, Name: name, Path: out, Size: size, Status: "done", WithDB: withDB, CreatedAt: time.Now()}
	s.mu.Lock()
	s.recs = append(s.recs, rec)
	s.mu.Unlock()
	s.Prune()
	return rec, nil
}

func (s *Service) record(id, path string, withDB bool, err error) *Backup {
	rec := &Backup{ID: id, Path: path, Status: "failed", WithDB: withDB, CreatedAt: time.Now()}
	if err != nil {
		rec.Error = err.Error()
	}
	s.mu.Lock()
	s.recs = append(s.recs, rec)
	s.mu.Unlock()
	return rec
}

// List returns backup records, newest first.
func (s *Service) List() []*Backup {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Backup, len(s.recs))
	copy(out, s.recs)
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// Get returns a backup record by id.
func (s *Service) Get(id string) (*Backup, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.recs {
		if r.ID == id {
			return r, true
		}
	}
	return nil, false
}

// Prune keeps only the newest cfg.Keep successful archives, deleting older files.
func (s *Service) Prune() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.Keep <= 0 {
		return 0
	}
	var done []*Backup
	for _, r := range s.recs {
		if r.Status == "done" {
			done = append(done, r)
		}
	}
	sort.Slice(done, func(i, j int) bool { return done[i].CreatedAt.After(done[j].CreatedAt) })
	pruned := 0
	for i := s.cfg.Keep; i < len(done); i++ {
		if done[i].Path != "" {
			os.Remove(done[i].Path)
		}
		done[i].Status = "pruned"
		pruned++
	}
	// drop pruned records
	kept := s.recs[:0]
	for _, r := range s.recs {
		if r.Status != "pruned" {
			kept = append(kept, r)
		}
	}
	s.recs = kept
	return pruned
}

// Restore extracts a backup archive into destDir. Restoring the database from
// the included dump is a manual step (run psql/mysql against the dump file).
func (s *Service) Restore(id, destDir string) error {
	rec, ok := s.Get(id)
	if !ok || rec.Path == "" {
		return fmt.Errorf("backup: no archive for %q", id)
	}
	f, err := os.Open(rec.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	return extractArchive(f, destDir)
}

// dbDump writes a database dump into dir using the configured driver.
func dbDump(ctx context.Context, cfg Config, dir string) error {
	switch cfg.DBDriver {
	case "postgres":
		out := filepath.Join(dir, "database.sql")
		f, err := os.Create(out)
		if err != nil {
			return err
		}
		defer f.Close()
		cmd := exec.CommandContext(ctx, "pg_dump", cfg.DBDSN)
		cmd.Stdout = f
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("backup: pg_dump failed (is it installed?): %w", err)
		}
		return nil
	case "mysql":
		out := filepath.Join(dir, "database.sql")
		f, err := os.Create(out)
		if err != nil {
			return err
		}
		defer f.Close()
		cmd := exec.CommandContext(ctx, "mysqldump", cfg.DBDSN)
		cmd.Stdout = f
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("backup: mysqldump failed (is it installed?): %w", err)
		}
		return nil
	case "sqlite", "sqlite3":
		return copyFile(cfg.DBDSN, filepath.Join(dir, filepath.Base(cfg.DBDSN)))
	default:
		return fmt.Errorf("backup: unsupported DB driver %q", cfg.DBDriver)
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// writeArchive tar.gz's the given sources into w, returning bytes written.
func writeArchive(w io.Writer, sources []string) (int64, error) {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	var total int64
	for _, src := range sources {
		base := filepath.Dir(src)
		err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			rel, rerr := filepath.Rel(base, path)
			if rerr != nil {
				rel = filepath.Base(path)
			}
			hdr, herr := tar.FileInfoHeader(info, "")
			if herr != nil {
				return herr
			}
			hdr.Name = filepath.ToSlash(rel)
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			n, err := io.Copy(tw, f)
			total += n
			return err
		})
		if err != nil {
			tw.Close()
			gz.Close()
			return total, err
		}
	}
	if err := tw.Close(); err != nil {
		return total, err
	}
	if err := gz.Close(); err != nil {
		return total, err
	}
	return total, nil
}

// extractArchive extracts a tar.gz stream into dest (path-traversal-safe).
func extractArchive(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if strings.Contains(hdr.Name, "..") {
			continue // skip traversal attempts
		}
		target := filepath.Join(dest, filepath.FromSlash(hdr.Name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode&0o777))
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return err
		}
		f.Close()
	}
	return nil
}

func (s *Service) mountRoutes(r chi.Router) {
	r.Route("/api/backup", func(r chi.Router) {
		r.Post("/run", func(w http.ResponseWriter, req *http.Request) {
			rec, err := s.Run(req.Context())
			if err != nil {
				writeJSON(w, 500, map[string]any{"error": err.Error(), "backup": rec})
				return
			}
			writeJSON(w, 200, rec)
		})
		r.Get("/backups", func(w http.ResponseWriter, req *http.Request) {
			writeJSON(w, 200, s.List())
		})
		r.Get("/backups/{id}", func(w http.ResponseWriter, req *http.Request) {
			if rec, ok := s.Get(chi.URLParam(req, "id")); ok {
				writeJSON(w, 200, rec)
				return
			}
			writeJSON(w, 404, map[string]string{"error": "not found"})
		})
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
