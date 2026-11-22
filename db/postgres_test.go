package db

import (
	"context"
	"os"
	"testing"

	"go.uber.org/zap"

	"github.com/yourname/job-scraper/models"
)

// Runs only with TEST_DATABASE_URL set (a disposable database: it is wiped).
func TestPostgresStore(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	d, err := New(dsn, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.conn.Exec(`DROP TABLE IF EXISTS profile_jobs, scraped_links, jobs`); err != nil {
		t.Fatal(err)
	}
	if err := d.Migrate([]string{"p1", "p2"}); err != nil {
		t.Fatal(err)
	}
	if !d.Fresh() {
		t.Error("empty database should be fresh")
	}

	a := &models.Job{Title: "A", Company: "X", Link: "https://a", Source: "linkedin"}
	b := &models.Job{Title: "B", Company: "Y", Link: "https://b", Source: "careers"}
	isNew, err := d.MarkNew(ctx, []Mark{{"p1", a}, {"p2", a}, {"p1", b}, {"p1", a}})
	if err != nil {
		t.Fatal(err)
	}
	if len(isNew) != 3 || d.Fresh() {
		t.Fatalf("first mark: %v (want 3 new), fresh=%v", isNew, d.Fresh())
	}
	isNew, err = d.MarkNew(ctx, []Mark{{"p1", a}, {"p2", b}})
	if err != nil {
		t.Fatal(err)
	}
	if len(isNew) != 1 || !isNew[Key{"p2", "https://b"}] {
		t.Errorf("second mark = %v, want only p2/b", isNew)
	}

	if err := d.RecordScraped(ctx, []string{"https://a", "https://a", "https://c", ""}); err != nil {
		t.Fatal(err)
	}
	seen, err := d.SeenLinks(ctx, []string{"https://a", "https://b", "https://c"})
	if err != nil {
		t.Fatal(err)
	}
	if !seen["https://a"] || seen["https://b"] || !seen["https://c"] {
		t.Errorf("seen = %v, want a and c", seen)
	}

	// Read-only (dry run): reports new jobs but leaves no trace.
	d.readOnly = true
	c := &models.Job{Title: "C", Company: "Z", Link: "https://c-job", Source: "hn"}
	isNew, err = d.MarkNew(ctx, []Mark{{"p1", c}})
	if err != nil {
		t.Fatal(err)
	}
	if !isNew[Key{"p1", "https://c-job"}] {
		t.Errorf("read-only mark = %v, want c new", isNew)
	}
	var n int
	_ = d.conn.QueryRow(`SELECT COUNT(*) FROM jobs WHERE link = 'https://c-job'`).Scan(&n)
	if n != 0 {
		t.Error("read-only MarkNew left a row in jobs")
	}
	d.readOnly = false

	// Re-running the migration on an existing database must be harmless.
	if err := d.Migrate([]string{"p1", "p2"}); err != nil {
		t.Fatal(err)
	}
	if d.Fresh() {
		t.Error("database with jobs should not be fresh")
	}
}
