package notifier

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/yourname/job-scraper/models"
)

var testNow = time.Date(2026, 11, 22, 15, 0, 0, 0, time.UTC)

func TestBuildMessagesCombinesFeeds(t *testing.T) {
	job := func(title string) *models.Job {
		return &models.Job{Title: title, Company: "Acme", Link: "https://x/" + title, Source: models.SourceLinkedIn}
	}
	msgs := buildMessages([]Digest{
		{Label: "🚀 Dev Jobs – India", Jobs: []*models.Job{job("a"), job("b")}},
		{Label: "🐹 Golang", Jobs: nil},
		{Label: "💼 SWE – Mumbai", Jobs: []*models.Job{job("c")}},
	}, RunStats{Duration: 41 * time.Second, Scraped: 7}, testNow)

	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	m := msgs[0]
	for _, want := range []string{
		"🔔 <b>3 new jobs</b> · 2 feeds\n\n<b>🚀 Dev Jobs – India</b> · 2\n\n<b>1.</b>",
		"\n\n<b>💼 SWE – Mumbai</b> · 1\n\n<b>1.</b>",
		"\n\n<i>⚡ 41s · 7 scraped</i>",
	} {
		if !strings.Contains(m, want) {
			t.Errorf("message missing %q:\n%s", want, m)
		}
	}
	if strings.Contains(m, "Golang") {
		t.Error("feed without jobs should not appear")
	}
	if !strings.HasSuffix(m, "</i>") {
		t.Error("footer should close the message")
	}
}

func TestBuildMessagesNothingNew(t *testing.T) {
	if msgs := buildMessages([]Digest{{Label: "x"}}, RunStats{}, testNow); msgs != nil {
		t.Errorf("got %d messages for no jobs, want none", len(msgs))
	}
}

func TestBuildMessagesSplitsOnlyAtLengthLimit(t *testing.T) {
	long := func(i int) *models.Job {
		return &models.Job{
			Title:    strings.Repeat("Very long HN header | ", 20),
			Location: strings.Repeat("Remote ", 40),
			Company:  "Acme",
			Link:     fmt.Sprintf("https://news.ycombinator.com/item?id=%d", i),
			Source:   models.SourceHN,
		}
	}
	var a, b []*models.Job
	for i := range 20 {
		a = append(a, long(i))
		b = append(b, long(100+i))
	}
	msgs := buildMessages([]Digest{{Label: "Feed A", Jobs: a}, {Label: "Feed B", Jobs: b}},
		RunStats{Duration: time.Minute, Scraped: 999}, testNow)
	if len(msgs) < 2 {
		t.Fatalf("40 long jobs should need several messages, got %d", len(msgs))
	}
	cards := 0
	for i, m := range msgs {
		if n := utf8.RuneCountInString(m); n > 4096 {
			t.Errorf("message %d is %d chars, Telegram max is 4096", i, n)
		}
		if want := fmt.Sprintf("· part %d/%d", i+1, len(msgs)); !strings.Contains(m, want) {
			t.Errorf("message %d missing %q", i, want)
		}
		if i > 0 && !strings.Contains(m, "(cont.)") && !strings.Contains(m, "<b>Feed B</b> · 20") {
			t.Errorf("message %d does not say which feed it continues", i)
		}
		cards += strings.Count(m, "news.ycombinator.com/item")
	}
	if cards != 40 {
		t.Errorf("messages hold %d jobs, want 40", cards)
	}
	if !strings.Contains(msgs[len(msgs)-1], "⚡") || strings.Contains(msgs[0], "⚡") {
		t.Error("footer should be on the last message only")
	}
}

func TestFormatJobCard(t *testing.T) {
	job := &models.Job{
		Title:    "SDE <2> & Go",
		Company:  "Zeta",
		Location: "Hyderabad, Telangana, India Actively Hiring Just now",
		Link:     "https://x.test/job?a=1&b=2",
		Source:   models.SourceLinkedIn,
		PostedAt: "2026-11-22",
		Tags:     []string{"Golang", "Node.js"},
	}
	got := formatJob(3, job, testNow)
	for _, want := range []string{
		`<b>3.</b> <a href="https://x.test/job?a=1&amp;b=2"><b>SDE &lt;2&gt; &amp; Go</b></a>`,
		"🏢 Zeta · 📍 Hyderabad, India\n",
		"🕐 today · LinkedIn · #Golang #NodeJS\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("card missing %q:\n%s", want, got)
		}
	}
}

func TestTitleAndFeedHeader(t *testing.T) {
	if got := formatTitle(1, 1, 1, 1); got != "🔔 <b>1 new job</b>\n\n" {
		t.Errorf("title = %q", got)
	}
	if got := formatTitle(12, 3, 2, 2); got != "🔔 <b>12 new jobs</b> · 3 feeds · part 2/2\n\n" {
		t.Errorf("split title = %q", got)
	}
	if got := formatFeedHeader("SWE & Co", 4, false); got != "<b>SWE &amp; Co</b> · 4\n\n" {
		t.Errorf("feed header = %q", got)
	}
	if got := formatFeedHeader("X", 4, true); got != "<b>X</b> (cont.)\n\n" {
		t.Errorf("cont header = %q", got)
	}
}

func TestFooter(t *testing.T) {
	got := formatFooter(RunStats{
		Duration: 41*time.Second + 300*time.Millisecond,
		Scraped:  7,
		Sources:  []string{"indeed", "linkedin"},
		NextRun:  testNow.Add(38 * time.Minute),
	}, testNow)
	want := "<i>⚡ 41s · 7 scraped · indeed, linkedin · next run ~38m</i>"
	if got != want {
		t.Errorf("footer = %q, want %q", got, want)
	}
}

func TestCleanLocation(t *testing.T) {
	cases := map[string]string{
		"Hyderabad, Telangana, India Actively Hiring Just now": "Hyderabad, India",
		"New Delhi, Delhi, India":                              "New Delhi, India",
		"Mumbai, Maharashtra, India Be an early applicant":     "Mumbai, India",
		"Bengaluru, India; Remote":                             "Bengaluru, India; Remote",
		"Jakarta Metropolitan Area":                            "Jakarta Metropolitan Area",
		"Pune Promoted 2 hours ago":                            "Pune",
		"":                                                     "",
	}
	for in, want := range cases {
		if got := cleanLocation(in); got != want {
			t.Errorf("cleanLocation(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanizeAge(t *testing.T) {
	cases := []struct {
		job  models.Job
		want string
	}{
		{models.Job{PostedTime: testNow.Add(-10 * time.Minute)}, "just now"},
		{models.Job{PostedTime: testNow.Add(-5 * time.Hour)}, "5h ago"},
		{models.Job{PostedTime: testNow.Add(-30 * time.Hour)}, "yesterday"},
		{models.Job{PostedTime: testNow.Add(-80 * time.Hour)}, "3d ago"},
		{models.Job{PostedAt: "2026-11-22"}, "today"},
		{models.Job{PostedAt: "2026-11-21"}, "yesterday"},
		{models.Job{PostedAt: "2026-11-18"}, "4d ago"},
		{models.Job{PostedAt: "Posted\n 3 days ago"}, "3 days ago"},
		{models.Job{PostedAt: "2 Days Ago"}, "2 days ago"},
		{models.Job{}, ""},
	}
	for _, c := range cases {
		if got := humanizeAge(&c.job, testNow); got != c.want {
			t.Errorf("humanizeAge(%+v) = %q, want %q", c.job.PostedAt, got, c.want)
		}
	}
}

func TestSortJobsByScoreThenRecency(t *testing.T) {
	a := &models.Job{Title: "a", Score: 10, PostedTime: testNow.Add(-time.Hour)}
	b := &models.Job{Title: "b", Score: 30}
	c := &models.Job{Title: "c", Score: 10, PostedTime: testNow}
	got := sortJobs([]*models.Job{a, b, c})
	if got[0] != b || got[1] != c || got[2] != a {
		t.Errorf("order = %s %s %s, want b c a", got[0].Title, got[1].Title, got[2].Title)
	}
}
