// config/profiles.go
package config

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"gopkg.in/yaml.v3"
)

// Source names that can appear in a profile's "sources" list.
const (
	SourceLinkedIn  = "linkedin"
	SourceIndeed    = "indeed"
	SourceNaukri    = "naukri"
	SourceRemoteOK  = "remoteok"
	SourceRemotive  = "remotive"
	SourceArbeitnow = "arbeitnow"
	SourceHN        = "hn"
	SourceCareers   = "careers"
)

// SearchSources run one query per keyword × location (browser / HTML scraping).
var SearchSources = map[string]bool{SourceLinkedIn: true, SourceIndeed: true, SourceNaukri: true}

// FeedSources are fetched once per run and then filtered per profile.
var FeedSources = map[string]bool{
	SourceRemoteOK: true, SourceRemotive: true, SourceArbeitnow: true,
	SourceHN: true, SourceCareers: true,
}

// Profile is one job feed: what to search for, where, and how to filter it.
// Every profile gets its own labelled Telegram messages and its own dedup.
type Profile struct {
	ID    string `yaml:"id"`
	Label string `yaml:"label"`

	// Keywords and Locations drive the search sources (LinkedIn, Indeed, Naukri).
	Keywords  []string `yaml:"keywords"`
	Locations []string `yaml:"locations"`
	Sources   []string `yaml:"sources"`

	// Regex filters applied to every job, from every source.
	// Include lists: a job must match at least one (empty = everything).
	// Exclude lists: a job matching any is dropped.
	TitleInclude    []string `yaml:"title_include"`
	TitleExclude    []string `yaml:"title_exclude"`
	LocationInclude []string `yaml:"location_include"`
	LocationExclude []string `yaml:"location_exclude"`

	// PostedWithinHours is the date filter sent to search sites (LinkedIn,
	// Indeed, Naukri).
	PostedWithinHours int `yaml:"posted_within_hours"`
	// FeedMaxAgeHours drops API / career-page jobs posted longer ago than
	// this. It is wider than PostedWithinHours because some APIs publish
	// with a delay (Remote OK ~24h); the DB still prevents repeats.
	FeedMaxAgeHours int `yaml:"feed_max_age_hours"`
	MaxPages        int `yaml:"max_pages"`
}

// Company is a career page hosted on a public ATS job board.
type Company struct {
	Name string `yaml:"name"`
	ATS  string `yaml:"ats"`  // greenhouse | lever | ashby
	Slug string `yaml:"slug"` // board identifier in the ATS URL
}

// HasSource reports whether the profile uses the given source.
func (p Profile) HasSource(name string) bool {
	return slices.Contains(p.Sources, name)
}

// LoadProfiles reads profiles from path. If the file does not exist, a single
// profile is built from the legacy KEYWORDS / LOCATIONS settings so older
// setups keep working.
func LoadProfiles(path string, cfg *Config) ([]Profile, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []Profile{{
			ID:                "dev_india",
			Label:             "🚀 Dev Jobs – India",
			Keywords:          cfg.Keywords,
			Locations:         cfg.Locations,
			Sources:           []string{SourceLinkedIn, SourceIndeed, SourceNaukri},
			PostedWithinHours: 24,
			FeedMaxAgeHours:   72,
			MaxPages:          cfg.MaxPages,
		}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read profiles: %w", err)
	}

	var file struct {
		Profiles []Profile `yaml:"profiles"`
	}
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(file.Profiles) == 0 {
		return nil, fmt.Errorf("%s defines no profiles", path)
	}

	seen := map[string]bool{}
	for i := range file.Profiles {
		p := &file.Profiles[i]
		if p.ID == "" || p.Label == "" {
			return nil, fmt.Errorf("profile #%d needs both id and label", i+1)
		}
		if seen[p.ID] {
			return nil, fmt.Errorf("duplicate profile id %q", p.ID)
		}
		seen[p.ID] = true
		if len(p.Sources) == 0 {
			return nil, fmt.Errorf("profile %q has no sources", p.ID)
		}
		for _, s := range p.Sources {
			if !SearchSources[s] && !FeedSources[s] {
				return nil, fmt.Errorf("profile %q: unknown source %q", p.ID, s)
			}
			if SearchSources[s] && (len(p.Keywords) == 0 || len(p.Locations) == 0) {
				return nil, fmt.Errorf("profile %q uses %s but has no keywords/locations", p.ID, s)
			}
		}
		if p.PostedWithinHours <= 0 {
			p.PostedWithinHours = 24
		}
		if p.FeedMaxAgeHours <= 0 {
			p.FeedMaxAgeHours = max(72, p.PostedWithinHours)
		}
		if p.MaxPages <= 0 {
			p.MaxPages = cfg.MaxPages
		}
	}
	return file.Profiles, nil
}

// LoadCompanies reads the career-page list. A missing file means no companies.
func LoadCompanies(path string) ([]Company, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read companies: %w", err)
	}
	var file struct {
		Companies []Company `yaml:"companies"`
	}
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for _, c := range file.Companies {
		switch c.ATS {
		case "greenhouse", "lever", "ashby":
		default:
			return nil, fmt.Errorf("company %q: unknown ats %q", c.Name, c.ATS)
		}
		if c.Slug == "" {
			return nil, fmt.Errorf("company %q has no slug", c.Name)
		}
	}
	return file.Companies, nil
}
