// scrapers/scraper.go
package scrapers

import (
	"context"
	"time"

	"github.com/yourname/job-scraper/models"
)

// Query is one search to run against a search source.
type Query struct {
	Keyword           string
	Location          string
	MaxPages          int
	PostedWithinHours int

	// Seen reports which links were already scraped in an earlier run. When
	// set, paging stops once a page is mostly old results (sites return
	// newest first, so later pages would be older still).
	Seen func(links []string) map[string]bool
}

// seenThreshold is the share of already-seen links (in fifths) at which a
// page counts as "old". Not 100%: promoted posts can be new on any page.
const seenThreshold = 4 // 4/5 = 80%

// mostlySeen reports whether this page's links were mostly scraped before.
func (q Query) mostlySeen(links []string) bool {
	if q.Seen == nil || len(links) == 0 {
		return false
	}
	return len(q.Seen(links))*5 >= len(links)*seenThreshold
}

// Scraper is a query-based source (LinkedIn, Indeed, Naukri). Scrape must be
// safe to call from several goroutines at once.
type Scraper interface {
	// Name returns the source identifier (e.g. "linkedin")
	Name() string

	// Scrape fetches jobs for one query. Paging stops early when a page
	// repeats links already seen during this call.
	Scrape(q Query) ([]*models.Job, error)
}

// Feed is a source fetched once per run (public job APIs, career pages);
// profiles filter its results afterwards.
type Feed interface {
	Name() string

	// Fetch returns jobs posted after since (when the source exposes dates).
	Fetch(ctx context.Context, since time.Time) ([]*models.Job, error)
}

// ScrapeResult bundles results from one scraper run
type ScrapeResult struct {
	Source string
	Jobs   []*models.Job
	Err    error
}
