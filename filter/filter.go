// filter/filter.go
package filter

import (
	"fmt"
	"regexp"
	"time"

	"github.com/yourname/job-scraper/config"
	"github.com/yourname/job-scraper/models"
)

// Filter decides whether a job belongs to a profile.
type Filter struct {
	titleInclude, titleExclude []*regexp.Regexp
	locInclude, locExclude     []*regexp.Regexp
	maxAge                     time.Duration
}

// New compiles a profile's regex filters.
func New(p config.Profile) (*Filter, error) {
	// Only jobs with a known PostedTime (API / career-page jobs) are age-checked;
	// search sites are already date-filtered by their query.
	f := &Filter{maxAge: time.Duration(p.FeedMaxAgeHours) * time.Hour}
	var err error
	for _, set := range []struct {
		dst  *[]*regexp.Regexp
		src  []string
		name string
	}{
		{&f.titleInclude, p.TitleInclude, "title_include"},
		{&f.titleExclude, p.TitleExclude, "title_exclude"},
		{&f.locInclude, p.LocationInclude, "location_include"},
		{&f.locExclude, p.LocationExclude, "location_exclude"},
	} {
		if *set.dst, err = compile(set.src); err != nil {
			return nil, fmt.Errorf("profile %q %s: %w", p.ID, set.name, err)
		}
	}
	return f, nil
}

func compile(patterns []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, re)
	}
	return out, nil
}

// Match reports whether job passes the profile's title, location and
// freshness filters at time now.
func (f *Filter) Match(job *models.Job, now time.Time) bool {
	if !job.PostedTime.IsZero() && f.maxAge > 0 && now.Sub(job.PostedTime) > f.maxAge {
		return false
	}
	return matches(f.titleInclude, f.titleExclude, job.Title) &&
		matches(f.locInclude, f.locExclude, job.Location)
}

func matches(include, exclude []*regexp.Regexp, s string) bool {
	for _, re := range exclude {
		if re.MatchString(s) {
			return false
		}
	}
	if len(include) == 0 {
		return true
	}
	for _, re := range include {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}
