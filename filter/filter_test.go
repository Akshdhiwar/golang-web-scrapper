package filter

import (
	"testing"
	"time"

	"github.com/yourname/job-scraper/config"
	"github.com/yourname/job-scraper/models"
)

// TestShippedProfiles checks the real profiles.yaml filters against
// representative job titles and locations.
func TestShippedProfiles(t *testing.T) {
	profiles, err := config.LoadProfiles("../profiles.yaml", &config.Config{MaxPages: 3})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]*Filter{}
	for _, p := range profiles {
		f, err := New(p)
		if err != nil {
			t.Fatal(err)
		}
		byID[p.ID] = f
	}

	now := time.Now()
	cases := []struct {
		profile, title, location string
		want                     bool
	}{
		// Golang worldwide
		{"golang_world", "Senior Golang Engineer", "Berlin, Germany", true},
		{"golang_world", "Backend Engineer (Go)", "Remote", true},
		{"golang_world", "Software Engineer - Go/Kubernetes", "Austin, TX", true},
		{"golang_world", "Go-to-Market Manager", "Remote", false},
		{"golang_world", "GTM Engineer, Go to Market", "New York", false},
		{"golang_world", "Google Ads Specialist", "London", false},
		{"golang_world", "Senior Python Developer", "Remote", false},

		// Software engineer, Mumbai metro or remote India
		{"swe_mumbai", "Software Engineer II", "Mumbai, Maharashtra, India", true},
		{"swe_mumbai", "SDE 2 - Backend", "Navi Mumbai", true},
		{"swe_mumbai", "Full Stack Developer", "Thane, Maharashtra", true},
		{"swe_mumbai", "Senior Software Engineer", "Remote - India", true},
		{"swe_mumbai", "Software Engineer", "India (Remote)", true},
		{"swe_mumbai", "Software Engineer", "Bengaluru, Karnataka, India", false},
		{"swe_mumbai", "Civil Engineer", "Mumbai", false},
		{"swe_mumbai", "Sales Engineer", "Mumbai", false},
		{"swe_mumbai", "Software Engineer", "Remote, United States", false},
		{"swe_mumbai", "Software Engineer", "Remote, United States; Bengaluru, India", false},

		// Frontend, preferred cities
		{"frontend_india", "Frontend Developer", "Pune, Maharashtra, India", true},
		{"frontend_india", "React.js Developer", "Bangalore", true},
		{"frontend_india", "Senior Front-End Engineer", "Gurugram, Haryana", true},
		{"frontend_india", "Full Stack React Developer", "Mumbai", false},
		{"frontend_india", "Backend Engineer", "Hyderabad", false},
		{"frontend_india", "Frontend Developer", "Chennai, Tamil Nadu", false},
	}
	for _, c := range cases {
		f := byID[c.profile]
		if f == nil {
			t.Fatalf("profile %q not found in profiles.yaml", c.profile)
		}
		job := &models.Job{Title: c.title, Location: c.location}
		if got := f.Match(job, now); got != c.want {
			t.Errorf("%s: Match(%q @ %q) = %v, want %v", c.profile, c.title, c.location, got, c.want)
		}
	}
}

func TestFreshness(t *testing.T) {
	f, err := New(config.Profile{ID: "x", FeedMaxAgeHours: 24})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if !f.Match(&models.Job{Title: "a"}, now) {
		t.Error("job with unknown posting time should pass")
	}
	if !f.Match(&models.Job{Title: "a", PostedTime: now.Add(-23 * time.Hour)}, now) {
		t.Error("23h-old job should pass a 24h window")
	}
	if f.Match(&models.Job{Title: "a", PostedTime: now.Add(-25 * time.Hour)}, now) {
		t.Error("25h-old job should fail a 24h window")
	}
}
