// scrapers/linkedin.go
package scrapers

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/mxschmitt/playwright-go"
	"go.uber.org/zap"

	"github.com/yourname/job-scraper/models"
	"github.com/yourname/job-scraper/utils"
)

// LinkedInScraper scrapes LinkedIn's public job search. It first uses the
// guest search endpoint (plain HTTP, no browser — much faster) and falls
// back to a headless browser if that fails.
type LinkedInScraper struct {
	browserScraper
}

// NewLinkedInScraper creates a new LinkedIn scraper
func NewLinkedInScraper(logger *zap.Logger, browser *Browser, minDelay, maxDelay, maxRetries int) *LinkedInScraper {
	return &LinkedInScraper{browserScraper{logger, browser, minDelay, maxDelay, maxRetries}}
}

func (s *LinkedInScraper) Name() string { return models.SourceLinkedIn }

var linkedInSite = browserSite{
	source:   models.SourceLinkedIn,
	pageSize: 25,
	buildURL: buildLinkedInURL,
	headers:  map[string]string{"Accept-Language": "en-US,en;q=0.9"},
	// LinkedIn keeps background requests open, so "network idle" often never
	// happens; wait for the DOM and then for the job cards instead.
	waitUntil:    playwright.WaitUntilStateDomcontentloaded,
	waitSelector: ".jobs-search__results-list li, .base-card",
	extractJS: `() => {
		const clean = s => (s || '').replace(/\s+/g, ' ').trim();
		const cards = document.querySelectorAll('.jobs-search__results-list li, .base-search-card--link');
		const jobs = [];
		cards.forEach(card => {
			const titleEl   = card.querySelector('.base-search-card__title, h3.base-search-card__title');
			const companyEl = card.querySelector('.base-search-card__subtitle a, h4.base-search-card__subtitle');
			const locationEl= card.querySelector('.job-search-card__location, .base-search-card__metadata');
			const linkEl    = card.querySelector('a.base-card__full-link, a[href*="linkedin.com/jobs"]');
			const timeEl    = card.querySelector('time');
			if (titleEl && linkEl) {
				jobs.push({
					title:    clean(titleEl.textContent),
					company:  clean(companyEl  ? companyEl.textContent  : ''),
					location: clean(locationEl ? locationEl.textContent : ''),
					link:     linkEl.href || '',
					postedAt: clean(timeEl ? timeEl.getAttribute('datetime') || timeEl.textContent : ''),
				});
			}
		});
		return jobs;
	}`,
	cleanLink: cleanLinkedInLink,
}

// Scrape runs one LinkedIn search query
func (s *LinkedInScraper) Scrape(q Query) ([]*models.Job, error) {
	jobs, err := s.scrapeHTTP(q)
	if err == nil {
		return jobs, nil
	}
	s.logger.Info("LinkedIn HTTP search failed — falling back to browser",
		zap.String("keyword", q.Keyword), zap.String("location", q.Location), zap.Error(err))
	return s.scrape(linkedInSite, q)
}

// linkedInGuestURL serves the public job search as HTML fragments (10 cards
// per request), the same data the browser page loads as you scroll.
const linkedInGuestURL = "https://www.linkedin.com/jobs-guest/jobs/api/seeMoreJobPostings/search?"

// scrapeHTTP pages through the guest endpoint down to the same depth as
// browser paging (MaxPages × 25 results). An error means "use the browser".
func (s *LinkedInScraper) scrapeHTTP(q Query) ([]*models.Job, error) {
	limit := max(q.MaxPages, 1) * linkedInSite.pageSize
	var jobs []*models.Job
	known := map[string]bool{}

	for start := 0; start < limit; {
		var cards []*models.Job
		err := utils.Retry(2, func() error {
			var err error
			cards, err = fetchLinkedInGuestPage(q, start)
			return err
		})
		if err != nil {
			if start == 0 {
				return nil, err
			}
			s.logger.Warn("LinkedIn HTTP paging stopped", zap.String("keyword", q.Keyword), zap.Error(err))
			break
		}
		if len(cards) == 0 {
			break // past the last result
		}

		pageLinks := make([]string, 0, len(cards))
		pageNew := 0
		for _, j := range cards {
			pageLinks = append(pageLinks, j.Link)
			if known[j.Link] {
				continue
			}
			known[j.Link] = true
			jobs = append(jobs, j)
			pageNew++
		}
		start += len(cards)
		s.logger.Info("Scraped page (HTTP)",
			zap.String("source", models.SourceLinkedIn),
			zap.String("keyword", q.Keyword), zap.String("location", q.Location),
			zap.Int("start", start), zap.Int("new_this_page", pageNew))

		if pageNew == 0 || start >= limit {
			break
		}
		if q.mostlySeen(pageLinks) {
			s.logger.Debug("LinkedIn page already seen in an earlier run, stopping pagination",
				zap.String("keyword", q.Keyword), zap.Int("start", start))
			break
		}
		utils.RandomDelay(s.minDelay, s.maxDelay)
	}
	return jobs, nil
}

// errLinkedInMarkup means the guest endpoint answered with HTML we don't
// recognise (layout change or a block page).
var errLinkedInMarkup = errors.New("linkedin guest endpoint: unrecognised markup")

func fetchLinkedInGuestPage(q Query, start int) ([]*models.Job, error) {
	params := linkedInParams(q)
	params.Set("start", fmt.Sprintf("%d", start))

	req, err := http.NewRequest(http.MethodGet, linkedInGuestURL+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", utils.RandomUserAgent())
	req.Header.Set("Accept", "text/html,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusBadRequest:
		return nil, nil // start is past the end of the results
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == 999:
		return nil, fmt.Errorf("linkedin guest endpoint: status %d: %w", resp.StatusCode, ErrBlocked)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("linkedin guest endpoint: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	return parseLinkedInCards(string(body))
}

// parseLinkedInCards extracts jobs from guest-endpoint HTML. An empty body
// means no (more) results; markup without cards is an error.
func parseLinkedInCards(body string) ([]*models.Job, error) {
	if strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(body), "<!DOCTYPE html>")) == "" {
		return nil, nil
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	var jobs []*models.Job
	doc.Find(".base-search-card").Each(func(_ int, card *goquery.Selection) {
		link, _ := card.Find("a.base-card__full-link").Attr("href")
		if link == "" && goquery.NodeName(card) == "a" {
			link, _ = card.Attr("href")
		}
		posted, ok := card.Find("time").Attr("datetime")
		if !ok {
			posted = card.Find("time").Text()
		}
		job := &models.Job{
			Title:    utils.CleanText(card.Find(".base-search-card__title").Text()),
			Company:  utils.CleanText(card.Find(".base-search-card__subtitle").Text()),
			Location: utils.CleanText(card.Find(".job-search-card__location").Text()),
			Link:     cleanLinkedInLink(link),
			Source:   models.SourceLinkedIn,
			PostedAt: utils.CleanText(posted),
		}
		if job.Title != "" && job.Link != "" {
			jobs = append(jobs, job)
		}
	})
	if len(jobs) == 0 && doc.Find("li").Length() > 0 {
		return nil, errLinkedInMarkup
	}
	return jobs, nil
}

// buildLinkedInURL constructs a LinkedIn job search URL.
// The public job search works without login for the first few pages, and
// accepts location=Worldwide for global searches.
func buildLinkedInURL(q Query, start int) string {
	params := linkedInParams(q)
	if start > 0 {
		params.Set("start", fmt.Sprintf("%d", start))
	}
	return "https://www.linkedin.com/jobs/search/?" + params.Encode()
}

// linkedInParams holds the search parameters shared by the HTTP and
// browser paths.
func linkedInParams(q Query) url.Values {
	params := url.Values{}
	params.Set("keywords", q.Keyword)
	params.Set("location", linkedInLocation(q.Location))
	params.Set("sortBy", "DD") // date descending
	params.Set("f_TPR", fmt.Sprintf("r%d", max(q.PostedWithinHours, 1)*3600))
	return params
}

// linkedInLocationAliases qualifies city names LinkedIn resolves to the
// wrong place: a bare "Delhi" returns jobs in Delhi, Michigan.
var linkedInLocationAliases = map[string]string{
	"delhi":     "Delhi, India",
	"new delhi": "New Delhi, India",
}

func linkedInLocation(loc string) string {
	if alias, ok := linkedInLocationAliases[strings.ToLower(strings.TrimSpace(loc))]; ok {
		return alias
	}
	return loc
}

// cleanLinkedInLink normalizes a LinkedIn job URL for deduplication
func cleanLinkedInLink(rawLink string) string {
	if rawLink == "" {
		return ""
	}
	u, err := url.Parse(rawLink)
	if err != nil {
		return rawLink
	}
	// LinkedIn paths look like /jobs/view/backend-engineer-at-acme-3912345678
	// and the host varies by country (in., id., www.). Reduce to the numeric
	// job ID on www so the same job always gets the same link.
	if _, rest, ok := strings.Cut(u.Path, "/jobs/view/"); ok {
		slug := strings.Trim(rest, "/")
		if m := linkedInJobID.FindString(slug); m != "" {
			return "https://www.linkedin.com/jobs/view/" + m
		}
		return "https://www.linkedin.com/jobs/view/" + slug
	}
	return rawLink
}

// linkedInJobID matches the trailing numeric job ID of a job slug.
var linkedInJobID = regexp.MustCompile(`\d{6,}$`)
