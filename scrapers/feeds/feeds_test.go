package feeds

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/yourname/job-scraper/config"
)

func decode[T any](t *testing.T, s string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

var (
	now   = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	since = now.Add(-24 * time.Hour)
)

func TestParseRemoteOK(t *testing.T) {
	raw := decode[[]remoteOKJob](t, `[
		{"legal": "notice"},
		{"id":"1","epoch":`+itoa(now.Add(-time.Hour).Unix())+`,"company":"Acme","position":"Senior Go Engineer","location":"Worldwide","url":"https://remoteOK.com/remote-jobs/1"},
		{"id":"2","epoch":`+itoa(now.Add(-48*time.Hour).Unix())+`,"company":"Old","position":"Old job","location":"","url":"https://remoteOK.com/remote-jobs/2"}
	]`)
	jobs := parseRemoteOK(raw, since)
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1 (notice and old job skipped)", len(jobs))
	}
	if j := jobs[0]; j.Title != "Senior Go Engineer" || j.Location != "Remote, Worldwide" || j.Company != "Acme" {
		t.Errorf("unexpected job %+v", j)
	}
}

func TestParseRemotive(t *testing.T) {
	resp := decode[remotiveResponse](t, `{"jobs":[
		{"url":"https://remotive.com/1","title":"Golang Dev","company_name":"A","publication_date":"2026-11-22T10:00:00","candidate_required_location":"India"},
		{"url":"https://remotive.com/2","title":"Old","company_name":"B","publication_date":"2026-11-01T10:00:00","candidate_required_location":"USA"}
	]}`)
	jobs := parseRemotive(resp, since)
	if len(jobs) != 1 || jobs[0].Location != "Remote, India" {
		t.Fatalf("unexpected jobs %+v", jobs)
	}
}

func TestParseArbeitnow(t *testing.T) {
	resp := decode[arbeitnowResponse](t, `{"data":[
		{"company_name":"A","title":"Go Developer","remote":true,"url":"https://arbeitnow.com/1","location":"Berlin","created_at":`+itoa(now.Add(-time.Hour).Unix())+`},
		{"company_name":"B","title":"Old","remote":false,"url":"https://arbeitnow.com/2","location":"Köln","created_at":`+itoa(now.Add(-72*time.Hour).Unix())+`}
	],"links":{"next":"https://arbeitnow.com/api?page=2"}}`)
	jobs, reachedOld := parseArbeitnow(resp, since)
	if len(jobs) != 1 || !reachedOld || jobs[0].Location != "Remote, Berlin" {
		t.Fatalf("jobs=%+v reachedOld=%v", jobs, reachedOld)
	}
}

func TestParseHN(t *testing.T) {
	c := decode[hnComments](t, `{"hits":[
		{"objectID":"10","parent_id":1,"story_id":1,"created_at_i":`+itoa(now.Unix())+`,"comment_text":"Acme Corp | Senior Backend Engineer (Go) | REMOTE (US, EU) | $150k<p>We build things &amp; stuff."},
		{"objectID":"11","parent_id":10,"story_id":1,"created_at_i":`+itoa(now.Unix())+`,"comment_text":"Is this still open?"},
		{"objectID":"12","parent_id":1,"story_id":1,"created_at_i":`+itoa(now.Unix())+`,"comment_text":"Not in the standard format"}
	]}`)
	jobs := parseHN(c)
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1", len(jobs))
	}
	j := jobs[0]
	if j.Company != "Acme Corp" || j.Title != "Acme Corp | Senior Backend Engineer (Go) | REMOTE (US, EU) | $150k" ||
		j.Link != "https://news.ycombinator.com/item?id=10" {
		t.Errorf("unexpected job %+v", j)
	}
}

func TestParseCareers(t *testing.T) {
	co := config.Company{Name: "Acme", Slug: "acme"}

	gh := decode[greenhouseResponse](t, `{"jobs":[
		{"absolute_url":"https://boards.greenhouse.io/acme/1","title":"Frontend Engineer","first_published":"2026-11-22T03:00:00-04:00","location":{"name":"Mumbai, India"}},
		{"absolute_url":"https://boards.greenhouse.io/acme/2","title":"Old","first_published":"2025-01-01T00:00:00Z","location":{"name":"Pune"}}
	]}`)
	if jobs := parseGreenhouse(gh, co, since); len(jobs) != 1 || jobs[0].Location != "Mumbai, India" || jobs[0].Company != "Acme" {
		t.Errorf("greenhouse: %+v", jobs)
	}

	lv := decode[[]leverPosting](t, `[
		{"text":"SDE 2","hostedUrl":"https://jobs.lever.co/acme/1","createdAt":`+itoa(now.Add(-time.Hour).UnixMilli())+`,"country":"IN","workplaceType":"remote","categories":{"location":"Remote","allLocations":["Remote"]}}
	]`)
	if jobs := parseLever(lv, co, since); len(jobs) != 1 || jobs[0].Location != "Remote, India" {
		t.Errorf("lever: %+v", jobs)
	}

	as := decode[ashbyResponse](t, `{"jobs":[
		{"title":"Go Engineer","location":"Bengaluru","jobUrl":"https://jobs.ashbyhq.com/acme/1","publishedAt":"2026-11-22T08:00:00.000+00:00","isListed":true,"isRemote":true,
		 "address":{"postalAddress":{"addressCountry":"India"}},
		 "secondaryLocations":[{"location":"Remote (US)","address":{"postalAddress":{"addressCountry":"United States"}}}]},
		{"title":"Hidden","location":"X","jobUrl":"u","publishedAt":"2026-11-22T08:00:00Z","isListed":false}
	]}`)
	if jobs := parseAshby(as, co, since); len(jobs) != 1 || jobs[0].Location != "Remote, Bengaluru, India; Remote (US), United States" {
		t.Errorf("ashby: %+v", jobs)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
