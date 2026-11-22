package scrapers

import "testing"

func TestCleanLinkedInLink(t *testing.T) {
	want := "https://www.linkedin.com/jobs/view/4440204415"
	for _, in := range []string{
		"https://id.linkedin.com/jobs/view/backend-engineer-golang-sde-ii-at-kredivo-group-4440204415?position=1&pageNum=0&refId=x",
		"https://in.linkedin.com/jobs/view/backend-engineer-golang-sde-ii-at-kredivo-group-4440204415/",
		"https://www.linkedin.com/jobs/view/4440204415/?trk=abc",
	} {
		if got := cleanLinkedInLink(in); got != want {
			t.Errorf("cleanLinkedInLink(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanIndeedLink(t *testing.T) {
	got := cleanIndeedLink("https://in.indeed.com/rc/clk?jk=e7176d961f65e547&bb=xyz&xkcb=1")
	if want := "https://in.indeed.com/viewjob?jk=e7176d961f65e547"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
