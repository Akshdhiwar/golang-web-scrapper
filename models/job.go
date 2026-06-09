// models/job.go
package models

import (
	"strings"
	"time"
)

// Job represents a single job posting
type Job struct {
	ID        int       `db:"id"`
	Title     string    `db:"title"`
	Company   string    `db:"company"`
	Location  string    `db:"location"`
	Link      string    `db:"link"`
	Source    string    `db:"source"`
	PostedAt  string    `db:"posted_at"`
	Tags      []string  `db:"-"` // extracted keywords (React, Node, etc.)
	Score     int       `db:"-"` // relevance score
	CreatedAt time.Time `db:"created_at"`
}

// Source constants
const (
	SourceLinkedIn = "linkedin"
	SourceIndeed   = "indeed"
	SourceNaukri   = "naukri"
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

// ScoreJob scores a job based on keyword relevance
func ScoreJob(job *Job, preferredKeywords []string) {
	titleLower := strings.ToLower(job.Title)
	score := 0
	tags := []string{}

	for _, kw := range RelevanceKeywords {
		if strings.Contains(strings.ToLower(titleLower), strings.ToLower(kw)) {
			tags = append(tags, kw)
			score += 10
		}
	}

	for _, pkw := range preferredKeywords {
		if strings.Contains(titleLower, strings.ToLower(pkw)) {
			score += 20
		}
	}

	job.Tags = tags
	job.Score = score
}
