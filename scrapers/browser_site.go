// scrapers/browser_site.go
package scrapers

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mxschmitt/playwright-go"
	"go.uber.org/zap"

	"github.com/yourname/job-scraper/models"
	"github.com/yourname/job-scraper/utils"
)

// ErrBlocked means the site served a login wall, captcha or access-denied
// page instead of results. The scheduler stops sending that source more
// tasks for the rest of the run when it sees this repeatedly.
var ErrBlocked = errors.New("blocked by site (login wall / captcha / access denied)")

// blockedMarkers appear in the URL or title of block pages.
var blockedMarkers = []string{"authwall", "/login", "checkpoint", "captcha",
	"security verification", "just a moment", "access denied", "sign in", "sign up"}

// browserSite describes how to page through one site's search results with
// Playwright. LinkedIn and Indeed differ only in these settings.
type browserSite struct {
	source       string
	pageSize     int
	buildURL     func(q Query, start int) string
	headers      map[string]string
	waitUntil    *playwright.WaitUntilState
	waitSelector string
	// requireCards stops paging when waitSelector never appears
	// (otherwise extraction is attempted anyway).
	requireCards bool
	// extractJS returns [{title, company, location, link, postedAt}]
	extractJS string
	cleanLink func(string) string
}

// browserScraper holds what every Playwright-based scraper needs.
type browserScraper struct {
	logger     *zap.Logger
	browser    *Browser
	minDelay   int
	maxDelay   int
	maxRetries int
}

func (s *browserScraper) scrape(site browserSite, q Query) ([]*models.Job, error) {
	page, closePage, err := s.browser.NewPage(site.headers)
	if err != nil {
		return nil, err
	}
	defer closePage()

	var jobs []*models.Job
	known := make(map[string]bool) // per call: safe under concurrency

	for pageNum := range q.MaxPages {
		searchURL := site.buildURL(q, pageNum*site.pageSize)
		s.logger.Debug("Visiting URL", zap.String("source", site.source), zap.String("url", searchURL))

		// Two quick attempts: a slow page is usually a throttled one, and
		// retrying it harder only makes blocking more likely.
		err := utils.Retry(min(s.maxRetries, 2), func() error {
			_, err := page.Goto(searchURL, playwright.PageGotoOptions{
				WaitUntil: site.waitUntil,
				Timeout:   playwright.Float(20000),
			})
			return err
		})
		if err != nil {
			// First page failing means the whole task failed; later pages
			// just end paging with what we have.
			if pageNum == 0 {
				return nil, fmt.Errorf("navigate %s: %w", searchURL, err)
			}
			s.logger.Warn("Navigation failed, stopping pagination",
				zap.String("source", site.source), zap.String("url", searchURL), zap.Error(err))
			break
		}

		if isBlocked(page) {
			if pageNum == 0 {
				return nil, fmt.Errorf("%s: %w", site.source, ErrBlocked)
			}
			s.logger.Warn("Blocked mid-pagination, stopping", zap.String("source", site.source))
			break
		}

		_, err = page.WaitForSelector(site.waitSelector, playwright.PageWaitForSelectorOptions{
			Timeout: playwright.Float(10000),
		})
		if err != nil && site.requireCards {
			// No cards on the first page of a broad, newest-first search is
			// how Indeed's bot wall looks (it serves a page, just not results).
			// Report it as a block so the scheduler stops this source after a
			// few in a row instead of waiting out every remaining query.
			if pageNum == 0 {
				return nil, fmt.Errorf("%s: no job cards on first page: %w", site.source, ErrBlocked)
			}
			s.logger.Warn("No job cards found or timeout",
				zap.String("source", site.source), zap.String("url", searchURL), zap.Error(err))
			break
		}

		data, err := page.Evaluate(site.extractJS)
		if err != nil {
			return jobs, fmt.Errorf("evaluate %s: %w", site.source, err)
		}
		rawJobs, _ := data.([]any)

		pageNew, repeated := 0, false
		var pageLinks []string
		for _, raw := range rawJobs {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			link := site.cleanLink(getString(m, "link"))
			if link == "" {
				continue
			}
			pageLinks = append(pageLinks, link)
			if known[link] {
				repeated = true
				continue
			}
			known[link] = true

			job := &models.Job{
				Title:    getString(m, "title"),
				Company:  getString(m, "company"),
				Location: getString(m, "location"),
				Link:     link,
				Source:   site.source,
				PostedAt: getString(m, "postedAt"),
			}
			if job.Title == "" {
				continue
			}
			jobs = append(jobs, job)
			pageNew++
		}

		s.logger.Info("Scraped page",
			zap.String("source", site.source),
			zap.String("keyword", q.Keyword),
			zap.String("location", q.Location),
			zap.Int("page", pageNum+1),
			zap.Int("jobs_this_page", pageNew),
			zap.Int("total_jobs", len(jobs)),
		)

		if pageNum < q.MaxPages-1 && q.mostlySeen(pageLinks) {
			s.logger.Debug("Page already seen in an earlier run, stopping pagination",
				zap.String("source", site.source), zap.Int("page", pageNum+1))
			break
		}
		// A page with nothing new (or repeating earlier results) means we've
		// run past the end of the result list.
		if pageNew == 0 || (repeated && pageNew < site.pageSize/2) {
			break
		}
		if pageNum < q.MaxPages-1 {
			utils.RandomDelay(s.minDelay, s.maxDelay)
		}
	}
	return jobs, nil
}

// isBlocked reports whether the current page looks like a block page.
func isBlocked(page playwright.Page) bool {
	title, _ := page.Title()
	probe := strings.ToLower(page.URL() + " " + title)
	for _, m := range blockedMarkers {
		if strings.Contains(probe, m) {
			return true
		}
	}
	return false
}

// getString safely extracts a trimmed string value from a map
func getString(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}
