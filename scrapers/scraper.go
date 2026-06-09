// scrapers/scraper.go
package scrapers

import (
	"github.com/yourname/job-scraper/models"
)

// Scraper defines the interface every job source must implement
type Scraper interface {
	// Name returns the scraper's source identifier (e.g. "linkedin")
	Name() string

	// Scrape fetches jobs for the given keyword and location
	// Returns a list of jobs and a stop flag — stop=true means a duplicate
	// was found and incremental scraping should halt
	Scrape(keyword, location string, maxPages int) ([]*models.Job, error)
}

// ScrapeResult bundles results from one scraper run
type ScrapeResult struct {
	Source string
	Jobs   []*models.Job
	Err    error
}
