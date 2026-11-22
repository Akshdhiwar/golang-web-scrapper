package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/yourname/job-scraper/models"
)

func TestFileStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "nested", "seen.json")

	s, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Fresh() {
		t.Fatal("new store should be fresh")
	}
	a := &models.Job{Link: "https://a"}
	b := &models.Job{Link: "https://b"}
	isNew, err := s.MarkNew(ctx, []Mark{{"p1", a}, {"p2", a}, {"p1", b}})
	if err != nil {
		t.Fatal(err)
	}
	if len(isNew) != 3 || s.Fresh() {
		t.Fatalf("first mark: %d new (want 3), fresh=%v", len(isNew), s.Fresh())
	}
	if err := s.RecordScraped(ctx, []string{"https://a", "https://c"}); err != nil {
		t.Fatal(err)
	}

	// Reopen from disk: state persists, store is no longer fresh.
	s2, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Fresh() {
		t.Error("reopened store should not be fresh")
	}
	isNew, _ = s2.MarkNew(ctx, []Mark{{"p1", a}, {"p2", b}})
	if isNew[Key{"p1", "https://a"}] || !isNew[Key{"p2", "https://b"}] || len(isNew) != 1 {
		t.Errorf("second mark = %v, want only p2/b new", isNew)
	}
	seen, _ := s2.SeenLinks(ctx, []string{"https://a", "https://b", "https://c"})
	if !seen["https://a"] || seen["https://b"] || !seen["https://c"] {
		t.Errorf("seen = %v, want a and c", seen)
	}

	// No temp files left behind by the atomic save.
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("store dir has %d entries, want just seen.json", len(entries))
	}
}

func TestFileStorePrunes(t *testing.T) {
	s := &FileStore{data: fileData{
		Profiles: map[string]map[string]int64{"p": {
			"old": time.Now().Add(-40 * 24 * time.Hour).Unix(),
			"new": time.Now().Unix(),
		}},
		Scraped: map[string]int64{
			"old": time.Now().Add(-20 * 24 * time.Hour).Unix(),
			"new": time.Now().Unix(),
		},
	}}
	s.prune(time.Now())
	if _, ok := s.data.Profiles["p"]["old"]; ok {
		t.Error("old sent entry not pruned")
	}
	if _, ok := s.data.Scraped["old"]; ok {
		t.Error("old scraped entry not pruned")
	}
	if len(s.data.Profiles["p"]) != 1 || len(s.data.Scraped) != 1 {
		t.Error("recent entries were pruned")
	}
}

func TestOpenRequiresDatabaseOnGitHubActions(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	if _, err := Open("", filepath.Join(t.TempDir(), "x.json"), nil, false, zap.NewNop()); err == nil {
		t.Error("expected an error without DATABASE_URL on GitHub Actions")
	}
}

func TestFileStoreReadOnlyNeverWrites(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "seen.json")

	// Seed one sent job with a normal store.
	s, _ := OpenFile(path)
	a := &models.Job{Link: "https://a"}
	if _, err := s.MarkNew(ctx, []Mark{{"p1", a}}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)

	ro, err := Open("", path, nil, true, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	b := &models.Job{Link: "https://b"}
	isNew, err := ro.MarkNew(ctx, []Mark{{"p1", a}, {"p1", b}})
	if err != nil {
		t.Fatal(err)
	}
	if isNew[Key{"p1", "https://a"}] || !isNew[Key{"p1", "https://b"}] {
		t.Errorf("read-only MarkNew = %v, want only b new", isNew)
	}
	if err := ro.RecordScraped(ctx, []string{"https://b"}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("read-only store modified the file")
	}

	// A real run afterwards still sees b as new.
	s2, _ := OpenFile(path)
	if isNew, _ := s2.MarkNew(ctx, []Mark{{"p1", b}}); !isNew[Key{"p1", "https://b"}] {
		t.Error("job shown in dry run was used up")
	}
}

func TestFileStoreReadOnlyFreshCreatesNoFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seen.json")
	ro, _ := Open("", path, nil, true, zap.NewNop())
	_, _ = ro.MarkNew(context.Background(), []Mark{{"p1", &models.Job{Link: "https://a"}}})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("dry run on a fresh store created the file")
	}
}
