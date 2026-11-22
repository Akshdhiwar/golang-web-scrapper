// models/job.go
package models

import (
	"regexp"
	"strings"
	"time"
)

// Job represents a single job posting
type Job struct {
	ID       int    `db:"id"`
	Title    string `db:"title"`
	Company  string `db:"company"`
	Location string `db:"location"`
	Link     string `db:"link"`
	Source   string `db:"source"`
	PostedAt string `db:"posted_at"`
	// PostedTime is the parsed posting time when the source provides one
	// (APIs and career pages). Zero means unknown.
	PostedTime time.Time `db:"-"`
	Tags       []string  `db:"-"` // extracted keywords (React, Node, etc.)
	Score      int       `db:"-"` // relevance score
	CreatedAt  time.Time `db:"created_at"`
}

// Source constants
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

// RelevanceKeywords maps tags to keywords found in job titles/descriptions
var RelevanceKeywords = []string{
	"React", "Next.js", "Vue", "Angular",
	"Node.js", "Express", "NestJS",
	"Golang", "Go",
	"Python", "Django", "FastAPI",
	"Java", "Spring",
	"PostgreSQL", "MySQL", "MongoDB",
	"Docker", "Kubernetes", "AWS",
	"TypeScript", "JavaScript",
	"REST", "GraphQL", "gRPC",
}

// ScoreJob scores a job based on keyword relevance. Keywords match whole
// words only, so "Go" doesn't match "Google" and "Java" doesn't match
// "JavaScript".
func ScoreJob(job *Job, preferredKeywords []string) {
	score := 0
	tags := []string{}

	for i, kw := range RelevanceKeywords {
		if relevancePatterns[i].MatchString(job.Title) {
			tags = append(tags, kw)
			score += 10
		}
	}

	for _, pkw := range preferredKeywords {
		if wordPattern(pkw).MatchString(job.Title) {
			score += 20
		}
	}

	job.Tags = tags
	job.Score = score
}

// relevancePatterns are the compiled RelevanceKeywords, in the same order.
var relevancePatterns = func() []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(RelevanceKeywords))
	for i, kw := range RelevanceKeywords {
		out[i] = wordPattern(kw)
	}
	return out
}()

// wordPattern matches kw case-insensitively as a whole word: not preceded
// or followed by a letter or digit (punctuation such as "." in "Node.js"
// stays part of the keyword).
func wordPattern(kw string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])` + regexp.QuoteMeta(strings.TrimSpace(kw)) + `(?:$|[^\p{L}\p{N}])`)
}
