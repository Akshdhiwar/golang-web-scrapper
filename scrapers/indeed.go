// scrapers/indeed.go
package scrapers

import (
	"fmt"
	"net/url"

	"github.com/mxschmitt/playwright-go"
	"go.uber.org/zap"

	"github.com/yourname/job-scraper/models"
)

// IndeedScraper scrapes Indeed India with a headless browser
type IndeedScraper struct {
	browserScraper
}

// NewIndeedScraper creates a new Indeed scraper instance
func NewIndeedScraper(logger *zap.Logger, browser *Browser, minDelay, maxDelay, maxRetries int) *IndeedScraper {
	return &IndeedScraper{browserScraper{logger, browser, minDelay, maxDelay, maxRetries}}
}

func (s *IndeedScraper) Name() string { return models.SourceIndeed }

var indeedSite = browserSite{
	source:   models.SourceIndeed,
	pageSize: 10,
	buildURL: buildIndeedURL,
	headers: map[string]string{
		"Accept-Language": "en-US,en;q=0.9",
		"Accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	},
	waitUntil:    playwright.WaitUntilStateDomcontentloaded,
	waitSelector: "div.job_seen_beacon, div.jobsearch-SerpJobCard, li.css-5lfssm",
	requireCards: true,
	extractJS: `() => {
		const clean = s => (s || '').replace(/\s+/g, ' ').trim();
		const cards = document.querySelectorAll('div.job_seen_beacon, div.jobsearch-SerpJobCard, li.css-5lfssm');
		const jobs = [];
		cards.forEach(card => {
			// Indeed has used both h2 and h3 for .jobTitle
			const titleEl   = card.querySelector('.jobTitle a span[title], .jobTitle span[title], .jobTitle a span, .jobTitle a');
			const companyEl = card.querySelector('[data-testid="company-name"], span.companyName, a.companyOverviewLink');
			const locationEl= card.querySelector('[data-testid="text-location"], div.companyLocation');
			const linkEl    = card.querySelector('.jobTitle a, h2 a, h3 a');
			const timeEl    = card.querySelector('span.date, [data-testid="myJobsStateDate"]');
			if (titleEl && linkEl) {
				jobs.push({
					title:    clean(titleEl.textContent),
					company:  clean(companyEl  ? companyEl.textContent  : ''),
					location: clean(locationEl ? locationEl.textContent : ''),
					link:     linkEl.href || '',
					postedAt: clean(timeEl ? timeEl.textContent : ''),
				});
			}
		});
		return jobs;
	}`,
	cleanLink: cleanIndeedLink,
}

// Scrape runs one Indeed search query
func (s *IndeedScraper) Scrape(q Query) ([]*models.Job, error) {
	return s.scrape(indeedSite, q)
}

// buildIndeedURL constructs a paginated Indeed search URL
func buildIndeedURL(q Query, start int) string {
	params := url.Values{}
	params.Set("q", q.Keyword)
	params.Set("l", q.Location)
	params.Set("sort", "date") // newest first
	params.Set("fromage", fmt.Sprintf("%d", ceilDays(q.PostedWithinHours)))
	if start > 0 {
		params.Set("start", fmt.Sprintf("%d", start))
	}
	return "https://in.indeed.com/jobs?" + params.Encode()
}

// ceilDays converts an hour window to whole days (minimum 1).
func ceilDays(hours int) int {
	return max((hours+23)/24, 1)
}

// cleanIndeedLink strips tracking params and normalizes the URL
func cleanIndeedLink(rawLink string) string {
	u, err := url.Parse(rawLink)
	if err != nil {
		return rawLink
	}
	// Keep only the job key (jk) parameter for deduplication
	if jk := u.Query().Get("jk"); jk != "" {
		return "https://in.indeed.com/viewjob?jk=" + jk
	}
	u.RawQuery = ""
	return u.String()
}
