// scrapers/naukri.go
package scrapers

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/gocolly/colly/v2"
	"go.uber.org/zap"

	"github.com/yourname/job-scraper/models"
	"github.com/yourname/job-scraper/utils"
)

// NaukriScraper scrapes job postings from Naukri.com
type NaukriScraper struct {
	logger     *zap.Logger
	minDelay   int
	maxDelay   int
	maxRetries int
	knownLinks map[string]bool
}

// NewNaukriScraper creates a new Naukri scraper
func NewNaukriScraper(logger *zap.Logger, minDelay, maxDelay, maxRetries int) *NaukriScraper {
	return &NaukriScraper{
		logger:     logger,
		minDelay:   minDelay,
		maxDelay:   maxDelay,
		maxRetries: maxRetries,
		knownLinks: make(map[string]bool),
	}
}

func (s *NaukriScraper) Name() string {
	return models.SourceNaukri
}

// naukriAPIResponse represents the JSON response from Naukri's internal API
type naukriAPIResponse struct {
	JobDetails []struct {
		Title       string `json:"title"`
		CompanyName string `json:"companyName"`
		Location    []struct {
			Label string `json:"label"`
		} `json:"location"`
		PostedDate string `json:"footerPlaceholderLabel"`
		JdURL      string `json:"jdURL"`
	} `json:"jobDetails"`
}

// Scrape fetches Naukri jobs using their public-facing job listing pages
func (s *NaukriScraper) Scrape(keyword, location string, maxPages int) ([]*models.Job, error) {
	var jobs []*models.Job
	stopPaging := false

	c := colly.NewCollector(
		colly.AllowedDomains("www.naukri.com"),
		colly.MaxDepth(1),
	)

	c.Limit(&colly.LimitRule{
		DomainGlob:  "*naukri.com*",
		Delay:       time.Duration(s.minDelay) * time.Second,
		RandomDelay: time.Duration(s.maxDelay-s.minDelay) * time.Second,
	})

	// Naukri uses a mix of SSR and JSON data embedded in script tags
	c.OnRequest(func(r *colly.Request) {
		r.Headers.Set("User-Agent", utils.RandomUserAgent())
		r.Headers.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
		r.Headers.Set("Accept-Language", "en-US,en;q=0.5")
		r.Headers.Set("Referer", "https://www.naukri.com/")
		s.logger.Debug("Visiting Naukri URL", zap.String("url", r.URL.String()))
	})

	// Try to extract jobs from Naukri's JSON-LD or embedded data
	c.OnHTML("script#__NEXT_DATA__", func(e *colly.HTMLElement) {
		if stopPaging {
			return
		}

		// Naukri embeds job data in __NEXT_DATA__ as JSON
		var nextData struct {
			Props struct {
				PageProps struct {
					JobData struct {
						Jobs []struct {
							Title      string   `json:"title"`
							Company    string   `json:"companyName"`
							Locations  []string `json:"placeholders"`
							PostedDate string   `json:"footerPlaceholderLabel"`
							JDURL      string   `json:"jdURL"`
						} `json:"jobDetails"`
					} `json:"jobData"`
				} `json:"pageProps"`
			} `json:"props"`
		}

		if err := json.Unmarshal([]byte(e.Text), &nextData); err != nil {
			s.logger.Debug("Could not parse __NEXT_DATA__", zap.Error(err))
			return
		}

		for _, jd := range nextData.Props.PageProps.JobData.Jobs {
			if stopPaging {
				break
			}

			link := jd.JDURL
			if !strings.HasPrefix(link, "http") {
				link = "https://www.naukri.com" + link
			}

			// Clean the link for deduplication
			link = cleanNaukriLink(link)

			if s.knownLinks[link] {
				stopPaging = true
				break
			}
			s.knownLinks[link] = true

			loc := ""
			if len(jd.Locations) > 0 {
				loc = jd.Locations[0]
			}

			job := &models.Job{
				Title:    jd.Title,
				Company:  jd.Company,
				Location: loc,
				Link:     link,
				Source:   models.SourceNaukri,
				PostedAt: jd.PostedDate,
			}

			if job.Title == "" || job.Link == "" {
				continue
			}

			jobs = append(jobs, job)
			s.logger.Debug("Found job (JSON)",
				zap.String("title", job.Title),
				zap.String("company", job.Company),
			)
		}
	})

	// Fallback: parse HTML job cards if JSON data isn't available
	c.OnHTML("article.jobTuple, div.cust-job-tuple, div[class*='srp-jobtuple']", func(e *colly.HTMLElement) {
		if stopPaging {
			return
		}

		job := &models.Job{
			Source: models.SourceNaukri,
		}

		job.Title = strings.TrimSpace(e.ChildText("a.title, .title"))
		job.Company = strings.TrimSpace(e.ChildText(".comp-name, .companyInfo a"))
		job.Location = strings.TrimSpace(e.ChildText(".loc-wrap .locWdth, .location"))
		job.PostedAt = strings.TrimSpace(e.ChildText(".job-post-day"))

		link := e.ChildAttr("a.title, a[href*='naukri.com']", "href")
		if link == "" {
			return
		}
		if !strings.HasPrefix(link, "http") {
			link = "https://www.naukri.com" + link
		}
		job.Link = cleanNaukriLink(link)

		if job.Title == "" || job.Link == "" {
			return
		}

		if s.knownLinks[job.Link] {
			stopPaging = true
			return
		}
		s.knownLinks[job.Link] = true

		jobs = append(jobs, job)
		s.logger.Debug("Found job (HTML)",
			zap.String("title", job.Title),
			zap.String("company", job.Company),
		)
	})

	c.OnError(func(r *colly.Response, err error) {
		s.logger.Error("Naukri request failed",
			zap.String("url", r.Request.URL.String()),
			zap.Int("status", r.StatusCode),
			zap.Error(err),
		)
	})

	// Paginate
	for page := 1; page <= maxPages; page++ {
		if stopPaging {
			break
		}

		searchURL := buildNaukriURL(keyword, location, page)

		retryErr := utils.Retry(s.maxRetries, func() error {
			return c.Visit(searchURL)
		})

		if retryErr != nil {
			s.logger.Error("Failed to scrape Naukri page",
				zap.String("keyword", keyword),
				zap.Int("page", page),
				zap.Error(retryErr),
			)
			break
		}

		s.logger.Info("Scraped Naukri page",
			zap.String("keyword", keyword),
			zap.Int("page", page),
			zap.Int("jobs_so_far", len(jobs)),
		)
	}

	return jobs, nil
}

// buildNaukriURL constructs a Naukri search URL
// Naukri uses path segments: /keyword-jobs-in-location-pageN
func buildNaukriURL(keyword, location string, page int) string {
	kwSlug := strings.ReplaceAll(strings.ToLower(keyword), " ", "-")
	locSlug := strings.ReplaceAll(strings.ToLower(location), " ", "-")

	base := fmt.Sprintf("https://www.naukri.com/%s-jobs-in-%s", kwSlug, locSlug)
	if page > 1 {
		base = fmt.Sprintf("%s-%d", base, page)
	}

	params := url.Values{}
	params.Set("experience", "0")
	return base + "?" + params.Encode()
}

// cleanNaukriLink strips source tracking parameters
func cleanNaukriLink(rawLink string) string {
	u, err := url.Parse(rawLink)
	if err != nil {
		return rawLink
	}
	// Remove common Naukri tracking params
	q := u.Query()
	for _, param := range []string{"src", "sid", "xp", "apType"} {
		q.Del(param)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
