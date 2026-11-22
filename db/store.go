// db/store.go
package db

import (
	"context"
	"errors"
	"os"
	"strings"

	"go.uber.org/zap"

	"github.com/yourname/job-scraper/models"
)

// Mark is one job matched by one profile (feed).
type Mark struct {
	Profile string
	Job     *models.Job
}

// Key identifies a (profile, job link) pair.
type Key struct {
	Profile string
	Link    string
}

// Store remembers which jobs each profile has already been sent, and which
// links were scraped in earlier runs. Implementations: Postgres (production,
// GitHub Actions) and a local JSON file (no database needed).
type Store interface {
	// MarkNew records every mark and returns the pairs that were not known
	// before, i.e. the jobs that are new for that profile.
	MarkNew(ctx context.Context, marks []Mark) (map[Key]bool, error)
	// SeenLinks reports which of links were scraped in an earlier run.
	SeenLinks(ctx context.Context, links []string) (map[string]bool, error)
	// RecordScraped remembers every link scraped this run (matched or not).
	RecordScraped(ctx context.Context, links []string) error
	// Fresh is true until the first MarkNew on a store that started empty.
	Fresh() bool
	Close() error
}

// Open picks the store: Postgres when databaseURL is set, otherwise the
// JSON file at storeFile. A readOnly store (DRY_RUN) still reports which
// jobs are new but saves nothing, so a dry run never uses up jobs that a
// real run should send.
func Open(databaseURL, storeFile string, profileIDs []string, readOnly bool, logger *zap.Logger) (Store, error) {
	if databaseURL == "" {
		// A missing secret in CI must not silently become "everything is new".
		if strings.EqualFold(os.Getenv("GITHUB_ACTIONS"), "true") {
			return nil, errors.New("DATABASE_URL is required on GitHub Actions (the file store does not persist between runs)")
		}
		logger.Info("No DATABASE_URL — using local file store", zap.String("file", storeFile))
		fs, err := OpenFile(storeFile)
		if err != nil {
			return nil, err
		}
		fs.readOnly = readOnly
		return fs, nil
	}
	pg, err := New(databaseURL, logger)
	if err != nil {
		return nil, err
	}
	if err := pg.Migrate(profileIDs); err != nil {
		pg.Close()
		return nil, err
	}
	pg.readOnly = readOnly
	return pg, nil
}

// uniqueStrings returns s without empty strings and duplicates, in order.
func uniqueStrings(s []string) []string {
	seen := make(map[string]bool, len(s))
	out := make([]string, 0, len(s))
	for _, v := range s {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

var (
	_ Store = (*DB)(nil)
	_ Store = (*FileStore)(nil)
)
