package feeds

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yourname/job-scraper/models"
	"github.com/yourname/job-scraper/utils"
)

// ── RemoteOK ─────────────────────────────────────────────────────
// https://remoteok.com/api — latest ~100 remote jobs. Their terms ask that we
// link back to Remote OK, which we do (job links point at remoteok.com).

type RemoteOK struct{}

func (RemoteOK) Name() string { return models.SourceRemoteOK }

type remoteOKJob struct {
	ID       string `json:"id"`
	Epoch    int64  `json:"epoch"`
	Company  string `json:"company"`
	Position string `json:"position"`
	Location string `json:"location"`
	URL      string `json:"url"`
}

func (RemoteOK) Fetch(ctx context.Context, since time.Time) ([]*models.Job, error) {
	var raw []remoteOKJob
	if err := getJSON(ctx, "https://remoteok.com/api", &raw); err != nil {
		return nil, err
	}
	return parseRemoteOK(raw, since), nil
}

func parseRemoteOK(raw []remoteOKJob, since time.Time) []*models.Job {
	var jobs []*models.Job
	for _, r := range raw {
		if r.ID == "" || r.URL == "" { // first element is a legal notice
			continue
		}
		posted := time.Unix(r.Epoch, 0)
		if posted.Before(since) {
			continue
		}
		jobs = append(jobs, &models.Job{
			Title:      utils.CleanText(r.Position),
			Company:    utils.CleanText(r.Company),
			Location:   joinNonEmpty(", ", "Remote", utils.CleanText(r.Location)),
			Link:       r.URL,
			Source:     models.SourceRemoteOK,
			PostedTime: posted,
			PostedAt:   posted.UTC().Format("2006-01-02 15:04 MST"),
		})
	}
	return jobs
}

// ── Remotive ─────────────────────────────────────────────────────
// https://remotive.com/api/remote-jobs — Remotive asks API users not to poll
// often, so this source only fetches on runs where the UTC hour is a
// multiple of 6 (4 times a day). Jobs are only skipped if they are older than
// the profile's window, so a 24h window loses nothing.

type Remotive struct {
	// Now is overridable for tests.
	Now func() time.Time
}

func (Remotive) Name() string { return models.SourceRemotive }

type remotiveResponse struct {
	Jobs []struct {
		URL             string `json:"url"`
		Title           string `json:"title"`
		CompanyName     string `json:"company_name"`
		PublicationDate string `json:"publication_date"`
		Location        string `json:"candidate_required_location"`
	} `json:"jobs"`
}

func (r Remotive) Fetch(ctx context.Context, since time.Time) ([]*models.Job, error) {
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	if now().UTC().Hour()%6 != 0 {
		return nil, nil
	}
	var resp remotiveResponse
	if err := getJSON(ctx, "https://remotive.com/api/remote-jobs?category=software-development", &resp); err != nil {
		return nil, err
	}
	return parseRemotive(resp, since), nil
}

func parseRemotive(resp remotiveResponse, since time.Time) []*models.Job {
	var jobs []*models.Job
	for _, r := range resp.Jobs {
		posted := parseTime(r.PublicationDate) // UTC, no zone suffix
		if !posted.IsZero() && posted.Before(since) {
			continue
		}
		jobs = append(jobs, &models.Job{
			Title:      utils.CleanText(r.Title),
			Company:    utils.CleanText(r.CompanyName),
			Location:   joinNonEmpty(", ", "Remote", utils.CleanText(r.Location)),
			Link:       r.URL,
			Source:     models.SourceRemotive,
			PostedTime: posted,
			PostedAt:   r.PublicationDate,
		})
	}
	return jobs
}

// ── Arbeitnow ────────────────────────────────────────────────────
// https://www.arbeitnow.com/api/job-board-api — European and remote jobs,
// newest first, 100 per page.

type Arbeitnow struct{}

func (Arbeitnow) Name() string { return models.SourceArbeitnow }

type arbeitnowResponse struct {
	Data []struct {
		CompanyName string `json:"company_name"`
		Title       string `json:"title"`
		Remote      bool   `json:"remote"`
		URL         string `json:"url"`
		Location    string `json:"location"`
		CreatedAt   int64  `json:"created_at"`
	} `json:"data"`
	Links struct {
		Next *string `json:"next"`
	} `json:"links"`
}

const arbeitnowMaxPages = 5

func (Arbeitnow) Fetch(ctx context.Context, since time.Time) ([]*models.Job, error) {
	var jobs []*models.Job
	for page := 1; page <= arbeitnowMaxPages; page++ {
		var resp arbeitnowResponse
		url := fmt.Sprintf("https://www.arbeitnow.com/api/job-board-api?page=%d", page)
		if err := getJSON(ctx, url, &resp); err != nil {
			if page > 1 {
				break // keep what we already have
			}
			return nil, err
		}
		pageJobs, reachedOld := parseArbeitnow(resp, since)
		jobs = append(jobs, pageJobs...)
		if reachedOld || resp.Links.Next == nil || len(resp.Data) == 0 {
			break
		}
	}
	return jobs, nil
}

func parseArbeitnow(resp arbeitnowResponse, since time.Time) (jobs []*models.Job, reachedOld bool) {
	for _, r := range resp.Data {
		posted := time.Unix(r.CreatedAt, 0)
		if posted.Before(since) {
			reachedOld = true
			continue
		}
		loc := utils.CleanText(r.Location)
		if r.Remote && !strings.Contains(strings.ToLower(loc), "remote") {
			loc = joinNonEmpty(", ", "Remote", loc)
		}
		jobs = append(jobs, &models.Job{
			Title:      utils.CleanText(r.Title),
			Company:    utils.CleanText(r.CompanyName),
			Location:   loc,
			Link:       r.URL,
			Source:     models.SourceArbeitnow,
			PostedTime: posted,
			PostedAt:   posted.UTC().Format("2006-01-02 15:04 MST"),
		})
	}
	return jobs, reachedOld
}
