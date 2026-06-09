// scrapers/indeed.go
package scrapers

import (
	"fmt"
	"net/url"
	"time"

	"github.com/playwright-community/playwright-go"
	"go.uber.org/zap"

	"github.com/yourname/job-scraper/models"
	"github.com/yourname/job-scraper/utils"
)

// IndeedScraper scrapes job postings from Indeed India using Playwright
type IndeedScraper struct {
	logger      *zap.Logger
	minDelay    int
	maxDelay    int
	maxRetries  int
	knownLinks  map[string]bool // for deduplication within a single run
}

// NewIndeedScraper creates a new Indeed scraper instance
func NewIndeedScraper(logger *zap.Logger, minDelay, maxDelay, maxRetries int) *IndeedScraper {
	return &IndeedScraper{
		logger:     logger,
		minDelay:   minDelay,
		maxDelay:   maxDelay,
		maxRetries: maxRetries,
		knownLinks: make(map[string]bool),
	}
}

func (s *IndeedScraper) Name() string {
	return models.SourceIndeed
}

// Scrape fetches Indeed jobs for a keyword and location
func (s *IndeedScraper) Scrape(keyword, location string, maxPages int) ([]*models.Job, error) {
	// Install Playwright browsers if needed
	if err := playwright.Install(&playwright.RunOptions{Browsers: []string{"chromium"}}); err != nil {
		return nil, fmt.Errorf("could not install playwright: %w", err)
	}

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
			"Accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
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

		start := page_num * 10
		searchURL := buildIndeedURL(keyword, location, start)

		s.logger.Debug("Visiting Indeed URL", zap.String("url", searchURL))

		var navigateErr error
		retryErr := utils.Retry(s.maxRetries, func() error {
			_, navigateErr = page.Goto(searchURL, playwright.PageGotoOptions{
				WaitUntil: playwright.WaitUntilStateDomcontentloaded,
				Timeout:   playwright.Float(30000),
			})
			return navigateErr
		})

		if retryErr != nil {
			s.logger.Error("Failed to navigate to Indeed page",
				zap.String("url", searchURL),
				zap.Error(retryErr),
			)
			break
		}

		// Wait for job cards or skip if not found
		_, err := page.WaitForSelector("div.job_seen_beacon, div.jobsearch-SerpJobCard, li.css-5lfssm", playwright.PageWaitForSelectorOptions{
			Timeout: playwright.Float(10000),
		})
		if err != nil {
			s.logger.Warn("No job cards found on Indeed page or timeout", zap.Error(err))
			break
		}

		// Extract job cards using JavaScript evaluation
		jobData, err := page.Evaluate(`() => {
			const clean = s => (s || '').replace(/\s+/g, ' ').trim();
			const cards = document.querySelectorAll('div.job_seen_beacon, div.jobsearch-SerpJobCard, li.css-5lfssm');
			const jobs = [];
			cards.forEach(card => {
				const titleEl   = card.querySelector('h2.jobTitle span, h2.jobTitle a span[title], h2 a span, h2.jobTitle a');
				const companyEl = card.querySelector('span.companyName, a.companyOverviewLink, [data-testid="company-name"]');
				const locationEl= card.querySelector('div.companyLocation, [data-testid="text-location"]');
				const linkEl    = card.querySelector('h2.jobTitle a, h2 a');
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
		}`)

		if err != nil {
			s.logger.Error("Failed to evaluate JavaScript on Indeed page", zap.Error(err))
			break
		}

		// Parse the returned data
		rawJobs, ok := jobData.([]interface{})
		if !ok {
			s.logger.Warn("Unexpected job data type from Indeed page")
			break
		}

		pageJobCount := 0
		for _, rawJob := range rawJobs {
			jobMap, ok := rawJob.(map[string]interface{})
			if !ok {
				continue
			}

			link := getString(jobMap, "link")
			link = cleanIndeedLink(link)

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
				Source:   models.SourceIndeed,
				PostedAt: getString(jobMap, "postedAt"),
			}

			if job.Title == "" {
				continue
			}

			jobs = append(jobs, job)
			pageJobCount++

			s.logger.Debug("Found Indeed job",
				zap.String("title", job.Title),
				zap.String("company", job.Company),
			)
		}

		s.logger.Info("Scraped Indeed page",
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

// buildIndeedURL constructs a paginated Indeed search URL
func buildIndeedURL(keyword, location string, start int) string {
	base := "https://in.indeed.com/jobs"
	params := url.Values{}
	params.Set("q", keyword)
	params.Set("l", location)
	params.Set("sort", "date") // newest first
	params.Set("fromage", "1") // last 1 day
	if start > 0 {
		params.Set("start", fmt.Sprintf("%d", start))
	}
	return base + "?" + params.Encode()
}

// cleanIndeedLink strips tracking params and normalizes the URL
func cleanIndeedLink(rawLink string) string {
	u, err := url.Parse(rawLink)
	if err != nil {
		return rawLink
	}
	// Keep only the job key (jk) parameter for deduplication
	jk := u.Query().Get("jk")
	if jk != "" {
		return fmt.Sprintf("https://in.indeed.com/viewjob?jk=%s", jk)
	}
	// Remove all query params if no jk
	u.RawQuery = ""
	return u.String()
}


