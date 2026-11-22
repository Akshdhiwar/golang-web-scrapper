package scrapers

import (
	"errors"
	"os"
	"testing"
)

func TestParseLinkedInCards(t *testing.T) {
	raw, err := os.ReadFile("testdata/linkedin_guest.html")
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := parseLinkedInCards(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs, want 2", len(jobs))
	}
	j := jobs[0]
	want := map[string][2]string{
		"title":    {j.Title, "Backend Engineer - Golang (SDE II)"},
		"company":  {j.Company, "Kredivo Group"},
		"location": {j.Location, "Jakarta Metropolitan Area"},
		"link":     {j.Link, "https://www.linkedin.com/jobs/view/4440204415"},
		"posted":   {j.PostedAt, "2026-11-21"},
	}
	for field, gw := range want {
		if gw[0] != gw[1] {
			t.Errorf("%s = %q, want %q", field, gw[0], gw[1])
		}
	}
	if jobs[1].Location != "Sofia, Sofia City, Bulgaria" {
		t.Errorf("second location = %q", jobs[1].Location)
	}
}

func TestParseLinkedInCardsEmptyAndUnknown(t *testing.T) {
	if jobs, err := parseLinkedInCards("<!DOCTYPE html>\n  "); err != nil || len(jobs) != 0 {
		t.Errorf("empty body: got %d jobs, err %v", len(jobs), err)
	}
	if _, err := parseLinkedInCards("<ul><li><div class=\"new-layout\">x</div></li></ul>"); !errors.Is(err, errLinkedInMarkup) {
		t.Errorf("unknown markup: err = %v, want errLinkedInMarkup", err)
	}
}

func TestQueryMostlySeen(t *testing.T) {
	links := []string{"a", "b", "c", "d", "e"}
	seenOf := func(n int) func([]string) map[string]bool {
		return func(l []string) map[string]bool {
			m := map[string]bool{}
			for _, x := range l[:n] {
				m[x] = true
			}
			return m
		}
	}
	if (Query{}).mostlySeen(links) {
		t.Error("nil Seen must never stop paging")
	}
	if (Query{Seen: seenOf(3)}).mostlySeen(links) {
		t.Error("3/5 seen should keep paging")
	}
	if !(Query{Seen: seenOf(4)}).mostlySeen(links) {
		t.Error("4/5 seen should stop paging")
	}
}

func TestLinkedInLocationAliases(t *testing.T) {
	for in, want := range map[string]string{"Delhi": "Delhi, India", "Mumbai": "Mumbai", "Worldwide": "Worldwide"} {
		if got := linkedInLocation(in); got != want {
			t.Errorf("linkedInLocation(%q) = %q, want %q", in, got, want)
		}
	}
}
