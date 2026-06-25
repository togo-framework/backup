package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeFiles creates a temp source dir with the given files.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestArchiveRoundTrip(t *testing.T) {
	src := writeFiles(t, map[string]string{
		"a.txt":       "hello",
		"sub/b.txt":   "world",
	})
	var buf bytes.Buffer
	n, err := writeArchive(&buf, []string{src})
	if err != nil {
		t.Fatalf("writeArchive: %v", err)
	}
	if n == 0 {
		t.Fatal("archive wrote 0 bytes")
	}
	dest := t.TempDir()
	if err := extractArchive(&buf, dest); err != nil {
		t.Fatalf("extractArchive: %v", err)
	}
	// The top dir name is preserved; find the files under dest.
	base := filepath.Base(src)
	got, err := os.ReadFile(filepath.Join(dest, base, "a.txt"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("a.txt = %q, err %v", got, err)
	}
	got2, err := os.ReadFile(filepath.Join(dest, base, "sub", "b.txt"))
	if err != nil || string(got2) != "world" {
		t.Fatalf("sub/b.txt = %q, err %v", got2, err)
	}
}

func TestRunCreatesArchiveAndRestore(t *testing.T) {
	src := writeFiles(t, map[string]string{"data.txt": "payload"})
	outDir := t.TempDir()
	s := (&Service{}).Configure(Config{Sources: []string{src}, Dir: outDir})

	rec, err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Status != "done" || rec.Size == 0 {
		t.Fatalf("bad record: %+v", rec)
	}
	if _, err := os.Stat(rec.Path); err != nil {
		t.Fatalf("archive file missing: %v", err)
	}
	if got := s.List(); len(got) != 1 {
		t.Fatalf("List = %d, want 1", len(got))
	}
	// Restore extracts the archive.
	restoreDir := t.TempDir()
	if err := s.Restore(rec.ID, restoreDir); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(restoreDir, filepath.Base(src), "data.txt"))
	if err != nil || string(got) != "payload" {
		t.Fatalf("restored data.txt = %q, err %v", got, err)
	}
}

func TestRetentionPrune(t *testing.T) {
	outDir := t.TempDir()
	s := (&Service{}).Configure(Config{Dir: outDir, Keep: 2})
	// Create 4 real archive files + records (oldest → newest).
	for i := 0; i < 4; i++ {
		p := filepath.Join(outDir, "bkp"+string(rune('A'+i))+".tar.gz")
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		s.recs = append(s.recs, &Backup{
			ID: "id" + string(rune('A'+i)), Path: p, Status: "done",
			CreatedAt: time.Now().Add(time.Duration(i) * time.Minute),
		})
	}
	pruned := s.Prune()
	if pruned != 2 {
		t.Fatalf("pruned %d, want 2", pruned)
	}
	if got := len(s.List()); got != 2 {
		t.Fatalf("records after prune = %d, want 2", got)
	}
	// The two oldest files must be gone, the two newest kept.
	if _, err := os.Stat(filepath.Join(outDir, "bkpA.tar.gz")); !os.IsNotExist(err) {
		t.Error("oldest archive A should be deleted")
	}
	if _, err := os.Stat(filepath.Join(outDir, "bkpD.tar.gz")); err != nil {
		t.Error("newest archive D should be kept")
	}
}

func TestDBDumpUnsupported(t *testing.T) {
	err := dbDump(context.Background(), Config{DBDriver: "oracle", DBDSN: "x"}, t.TempDir())
	if err == nil {
		t.Fatal("expected error for unsupported driver")
	}
}
