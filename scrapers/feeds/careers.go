package feeds

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/yourname/job-scraper/config"
	"github.com/yourname/job-scraper/models"
	"github.com/yourname/job-scraper/utils"
)

// Careers fetches every company in companies.yaml from its public ATS board
// (Greenhouse, Lever or Ashby).
type Careers struct {
	Companies []config.Company
	Logger    *zap.Logger
}

func (Careers) Name() string { return models.SourceCareers }

const careersConcurrency = 8

func (c Careers) Fetch(ctx context.Context, since time.Time) ([]*models.Job, error) {
	if len(c.Companies) == 0 {
		return nil, nil
	}
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		jobs   []*models.Job
		failed int
		sem    = make(chan struct{}, careersConcurrency)
	)
	for _, co := range c.Companies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			got, err := fetchCompany(ctx, co, since)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed++
				c.Logger.Warn("Career page fetch failed",
					zap.String("company", co.Name), zap.String("ats", co.ATS), zap.Error(err))
				return
			}
			jobs = append(jobs, got...)
		}()
	}
	wg.Wait()

	// One broken board shouldn't fail the whole source (and trigger an alert).
	if failed == len(c.Companies) {
		return nil, fmt.Errorf("all %d career pages failed", failed)
	}
	return jobs, nil
}

func fetchCompany(ctx context.Context, co config.Company, since time.Time) ([]*models.Job, error) {
	switch co.ATS {
	case "greenhouse":
		var resp greenhouseResponse
		if err := getJSON(ctx, "https://boards-api.greenhouse.io/v1/boards/"+co.Slug+"/jobs", &resp); err != nil {
			return nil, err
		}
		return parseGreenhouse(resp, co, since), nil
	case "lever":
		var resp []leverPosting
		if err := getJSON(ctx, "https://api.lever.co/v0/postings/"+co.Slug+"?mode=json", &resp); err != nil {
			return nil, err
		}
		return parseLever(resp, co, since), nil
	case "ashby":
		var resp ashbyResponse
		if err := getJSON(ctx, "https://api.ashbyhq.com/posting-api/job-board/"+co.Slug, &resp); err != nil {
			return nil, err
		}
		return parseAshby(resp, co, since), nil
	}
	return nil, fmt.Errorf("unknown ats %q", co.ATS)
}

func careerJob(co config.Company, title, location, link string, posted time.Time) *models.Job {
	return &models.Job{
		Title:      utils.CleanText(title),
		Company:    co.Name,
		Location:   utils.CleanText(location),
		Link:       link,
		Source:     models.SourceCareers,
		PostedTime: posted,
		PostedAt:   posted.UTC().Format("2006-01-02"),
	}
}

// ── Greenhouse ───────────────────────────────────────────────────

type greenhouseResponse struct {
	Jobs []struct {
		AbsoluteURL    string `json:"absolute_url"`
		Title          string `json:"title"`
		FirstPublished string `json:"first_published"`
		UpdatedAt      string `json:"updated_at"`
		Location       struct {
			Name string `json:"name"`
		} `json:"location"`
	} `json:"jobs"`
}

func parseGreenhouse(resp greenhouseResponse, co config.Company, since time.Time) []*models.Job {
	var jobs []*models.Job
	for _, j := range resp.Jobs {
		posted := parseTime(j.FirstPublished)
		if posted.IsZero() {
			posted = parseTime(j.UpdatedAt)
		}
		if posted.Before(since) {
			continue
		}
		jobs = append(jobs, careerJob(co, j.Title, j.Location.Name, j.AbsoluteURL, posted))
	}
	return jobs
}

// withCountry appends country to place unless place already mentions it.
func withCountry(place, country string) string {
	if country == "" || strings.Contains(strings.ToLower(place), strings.ToLower(country)) {
		return place
	}
	return joinNonEmpty(", ", place, country)
}

// ── Lever ────────────────────────────────────────────────────────

type leverPosting struct {
	Text          string `json:"text"`
	HostedURL     string `json:"hostedUrl"`
	CreatedAt     int64  `json:"createdAt"` // ms since epoch
	Country       string `json:"country"`
	WorkplaceType string `json:"workplaceType"`
	Categories    struct {
		Location     string   `json:"location"`
		AllLocations []string `json:"allLocations"`
	} `json:"categories"`
}

func parseLever(resp []leverPosting, co config.Company, since time.Time) []*models.Job {
	var jobs []*models.Job
	for _, j := range resp {
		posted := time.UnixMilli(j.CreatedAt)
		if posted.Before(since) {
			continue
		}
		locs := j.Categories.AllLocations
		if len(locs) == 0 {
			locs = []string{j.Categories.Location}
		}
		country := ""
		if j.Country == "IN" {
			country = "India"
		}
		entries := make([]string, 0, len(locs))
		for _, l := range locs {
			e := withCountry(l, country)
			if j.WorkplaceType == "remote" && !strings.Contains(strings.ToLower(e), "remote") {
				e = joinNonEmpty(", ", "Remote", e)
			}
			entries = append(entries, e)
		}
		jobs = append(jobs, careerJob(co, j.Text, joinNonEmpty("; ", entries...), j.HostedURL, posted))
	}
	return jobs
}

// ── Ashby ────────────────────────────────────────────────────────

type ashbyAddress struct {
	PostalAddress struct {
		AddressCountry string `json:"addressCountry"`
	} `json:"postalAddress"`
}

type ashbyResponse struct {
	Jobs []struct {
		Title              string       `json:"title"`
		Location           string       `json:"location"`
		JobURL             string       `json:"jobUrl"`
		PublishedAt        string       `json:"publishedAt"`
		IsListed           bool         `json:"isListed"`
		IsRemote           bool         `json:"isRemote"`
		Address            ashbyAddress `json:"address"`
		SecondaryLocations []struct {
			Location string       `json:"location"`
			Address  ashbyAddress `json:"address"`
		} `json:"secondaryLocations"`
	} `json:"jobs"`
}

func parseAshby(resp ashbyResponse, co config.Company, since time.Time) []*models.Job {
	var jobs []*models.Job
	for _, j := range resp.Jobs {
		if !j.IsListed {
			continue
		}
		posted := parseTime(j.PublishedAt)
		if posted.Before(since) {
			continue
		}
		// One "place, country" entry per location, entries separated by "; "
		// so filters can't match "remote" from one entry and "India" from another.
		primary := withCountry(j.Location, j.Address.PostalAddress.AddressCountry)
		if j.IsRemote && !strings.Contains(strings.ToLower(primary), "remote") {
			primary = joinNonEmpty(", ", "Remote", primary)
		}
		entries := []string{primary}
		for _, s := range j.SecondaryLocations {
			entries = append(entries, withCountry(s.Location, s.Address.PostalAddress.AddressCountry))
		}
		jobs = append(jobs, careerJob(co, j.Title, joinNonEmpty("; ", entries...), j.JobURL, posted))
	}
	return jobs
}
