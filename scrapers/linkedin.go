// scrapers/linkedin.go
package scrapers

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/playwright-community/playwright-go"
	"go.uber.org/zap"

	"github.com/yourname/job-scraper/models"
	"github.com/yourname/job-scraper/utils"
)

// LinkedInScraper scrapes LinkedIn Jobs using Playwright (headless browser)
// LinkedIn heavily uses JavaScript rendering — Colly alone won't work
type LinkedInScraper struct {
	logger     *zap.Logger
	minDelay   int
	maxDelay   int
	maxRetries int
	knownLinks map[string]bool
}

// NewLinkedInScraper creates a new LinkedIn scraper
func NewLinkedInScraper(logger *zap.Logger, minDelay, maxDelay, maxRetries int) *LinkedInScraper {
	return &LinkedInScraper{
		logger:     logger,
		minDelay:   minDelay,
		maxDelay:   maxDelay,
		maxRetries: maxRetries,
		knownLinks: make(map[string]bool),
	}
}

func (s *LinkedInScraper) Name() string {
	return models.SourceLinkedIn
}

// Scrape uses a headless browser to scrape LinkedIn job listings
func (s *LinkedInScraper) Scrape(keyword, location string, maxPages int) ([]*models.Job, error) {
	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("could not start playwright: %w", err)
	}
	defer pw.Stop()

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
		Args: []string{
			"--no-sandbox",
			"--disable-setuid-sandbox",
			"--disable-dev-shm-usage",
			"--disable-blink-features=AutomationControlled",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("could not launch browser: %w", err)
	}
	defer browser.Close()

	// Create a browser context with realistic settings
	context, err := browser.NewContext(playwright.BrowserNewContextOptions{
		UserAgent: playwright.String(utils.RandomUserAgent()),
		Viewport: &playwright.Size{
			Width:  1920,
			Height: 1080,
		},
		ExtraHttpHeaders: map[string]string{
			"Accept-Language": "en-US,en;q=0.9",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("could not create browser context: %w", err)
	}
	defer context.Close()

	page, err := context.NewPage()
	if err != nil {
		return nil, fmt.Errorf("could not create page: %w", err)
	}
	defer page.Close()

	// Block images and fonts to speed up loading
	if err := page.Route("**/*.{png,jpg,jpeg,gif,webp,woff,woff2,ttf}", func(route playwright.Route) {
		route.Abort()
	}); err != nil {
		s.logger.Warn("Could not set resource blocking", zap.Error(err))
	}

	var jobs []*models.Job
	stopPaging := false

	for page_num := 0; page_num < maxPages; page_num++ {
		if stopPaging {
			break
		}

		start := page_num * 25
		searchURL := buildLinkedInURL(keyword, location, start)

		s.logger.Debug("Visiting LinkedIn URL", zap.String("url", searchURL))

		var navigateErr error
		retryErr := utils.Retry(s.maxRetries, func() error {
			_, navigateErr = page.Goto(searchURL, playwright.PageGotoOptions{
				WaitUntil: playwright.WaitUntilStateNetworkidle,
				Timeout:   playwright.Float(30000),
			})
			return navigateErr
		})

		if retryErr != nil {
			s.logger.Error("Failed to navigate to LinkedIn page",
				zap.String("url", searchURL),
				zap.Error(retryErr),
			)
			break
		}

		// Wait for job cards to render
		page.WaitForSelector(".jobs-search__results-list li, .base-card", playwright.PageWaitForSelectorOptions{
			Timeout: playwright.Float(10000),
		})

		// Extract job cards using JavaScript evaluation
		// clean() collapses newlines/tabs/multiple-spaces into a single space
		jobData, err := page.Evaluate(`() => {
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
		}`)

		if err != nil {
			s.logger.Error("Failed to evaluate JavaScript on LinkedIn page", zap.Error(err))
			break
		}

		// Parse the returned data
		rawJobs, ok := jobData.([]interface{})
		if !ok {
			s.logger.Warn("Unexpected job data type from LinkedIn page")
			break
		}

		pageJobCount := 0
		for _, rawJob := range rawJobs {
			jobMap, ok := rawJob.(map[string]interface{})
			if !ok {
				continue
			}

			link := getString(jobMap, "link")
			link = cleanLinkedInLink(link)

			if link == "" {
				continue
			}

			if s.knownLinks[link] {
				stopPaging = true
				break
			}
			s.knownLinks[link] = true

			job := &models.Job{
				Title:    getString(jobMap, "title"),
				Company:  getString(jobMap, "company"),
				Location: getString(jobMap, "location"),
				Link:     link,
				Source:   models.SourceLinkedIn,
				PostedAt: getString(jobMap, "postedAt"),
			}

			if job.Title == "" {
				continue
			}

			jobs = append(jobs, job)
			pageJobCount++

			s.logger.Debug("Found LinkedIn job",
				zap.String("title", job.Title),
				zap.String("company", job.Company),
			)
		}

		s.logger.Info("Scraped LinkedIn page",
			zap.String("keyword", keyword),
			zap.Int("page", page_num+1),
			zap.Int("jobs_this_page", pageJobCount),
			zap.Int("total_jobs", len(jobs)),
		)

		// Polite delay between pages
		utils.RandomDelay(s.minDelay, s.maxDelay)
		time.Sleep(500 * time.Millisecond) // Extra buffer
	}

	return jobs, nil
}

// buildLinkedInURL constructs a LinkedIn job search URL
// LinkedIn public job search works without login for the first few pages
func buildLinkedInURL(keyword, location string, start int) string {
	params := url.Values{}
	params.Set("keywords", keyword)
	params.Set("location", location)
	params.Set("sortBy", "DD")   // date descending
	params.Set("f_TPR", "r3600") // posted in last hour (r3600 = 3600 seconds)
	if start > 0 {
		params.Set("start", fmt.Sprintf("%d", start))
	}
	return "https://www.linkedin.com/jobs/search/?" + params.Encode()
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
	// Keep only the path (job ID is in the path), remove all query params
	u.RawQuery = ""
	u.Fragment = ""

	// LinkedIn paths look like /jobs/view/3912345678/
	// Make sure it's a proper job view URL
	if strings.Contains(u.Path, "/jobs/view/") {
		return u.Scheme + "://" + u.Host + u.Path
	}

	return rawLink
}

// getString safely extracts a string value from a map
func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
