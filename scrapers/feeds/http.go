// Package feeds contains sources that are fetched once per run from public
// JSON APIs (remote job boards, Hacker News, company ATS boards). Their jobs
// are then filtered per profile by the scheduler.
package feeds

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/yourname/job-scraper/utils"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

const userAgent = "job-scraper/1.0"

// getJSON GETs url and decodes the JSON body into out, retrying transient failures.
func getJSON(ctx context.Context, url string, out any) error {
	return utils.Retry(3, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "application/json")

		resp, err := httpClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
			return fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
		}
		return json.NewDecoder(resp.Body).Decode(out)
	})
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

// stripHTML removes tags, decodes entities and collapses whitespace.
func stripHTML(s string) string {
	return utils.CleanText(html.UnescapeString(tagRe.ReplaceAllString(s, " ")))
}

// joinNonEmpty joins the non-empty, de-duplicated parts with sep.
func joinNonEmpty(sep string, parts ...string) string {
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || seen[strings.ToLower(p)] {
			continue
		}
		seen[strings.ToLower(p)] = true
		out = append(out, p)
	}
	return strings.Join(out, sep)
}

// parseTime tries the timestamp layouts used by the APIs; zero if none match.
func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
