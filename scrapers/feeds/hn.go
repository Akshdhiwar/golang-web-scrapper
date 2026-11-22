package feeds

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yourname/job-scraper/models"
)

// HN reads top-level comments from the latest monthly "Ask HN: Who is hiring?"
// thread via the Algolia HN Search API. By convention each comment's first
// line reads "Company | Role | Location | ...", which becomes the job title
// and location so profile filters can match it.
type HN struct{}

func (HN) Name() string { return models.SourceHN }

const hnAPI = "https://hn.algolia.com/api/v1/search_by_date"

type hnStories struct {
	Hits []struct {
		ObjectID string `json:"objectID"`
		Title    string `json:"title"`
	} `json:"hits"`
}

type hnComments struct {
	Hits []hnComment `json:"hits"`
}

type hnComment struct {
	ObjectID    string `json:"objectID"`
	ParentID    int64  `json:"parent_id"`
	StoryID     int64  `json:"story_id"`
	CreatedAtI  int64  `json:"created_at_i"`
	CommentText string `json:"comment_text"`
}

func (HN) Fetch(ctx context.Context, since time.Time) ([]*models.Job, error) {
	var stories hnStories
	if err := getJSON(ctx, hnAPI+"?tags=story,author_whoishiring&hitsPerPage=5", &stories); err != nil {
		return nil, err
	}
	storyID := ""
	for _, s := range stories.Hits {
		if strings.HasPrefix(s.Title, "Ask HN: Who is hiring?") {
			storyID = s.ObjectID
			break
		}
	}
	if storyID == "" {
		return nil, fmt.Errorf("no recent 'Who is hiring?' thread found")
	}

	params := url.Values{}
	params.Set("tags", "comment,story_"+storyID)
	params.Set("numericFilters", fmt.Sprintf("created_at_i>%d", since.Unix()))
	params.Set("hitsPerPage", "1000")
	var comments hnComments
	if err := getJSON(ctx, hnAPI+"?"+params.Encode(), &comments); err != nil {
		return nil, err
	}
	return parseHN(comments), nil
}

func parseHN(c hnComments) []*models.Job {
	var jobs []*models.Job
	for _, h := range c.Hits {
		if h.ParentID != h.StoryID { // replies, not job posts
			continue
		}
		first, _, _ := strings.Cut(h.CommentText, "<p>")
		header := stripHTML(first)
		if header == "" || !strings.Contains(header, "|") {
			continue // not in the conventional format
		}
		company, _, _ := strings.Cut(header, "|")
		posted := time.Unix(h.CreatedAtI, 0)
		jobs = append(jobs, &models.Job{
			Title:      truncate(header, 160),
			Company:    strings.TrimSpace(company),
			Location:   header, // header usually contains location / REMOTE
			Link:       "https://news.ycombinator.com/item?id=" + h.ObjectID,
			Source:     models.SourceHN,
			PostedTime: posted,
			PostedAt:   posted.UTC().Format("2006-01-02 15:04 MST"),
		})
	}
	return jobs
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
