package models

import (
	"slices"
	"testing"
)

func TestScoreJobMatchesWholeWords(t *testing.T) {
	cases := map[string][]string{
		"Application Developer-Google Cloud FullStack": {},
		"Backend Engineer (Go)":                        {"Go"},
		"Senior Golang Developer":                      {"Golang"},
		"Go/Python Engineer":                           {"Go", "Python"},
		"JavaScript Developer":                         {"JavaScript"},
		"Java Developer":                               {"Java"},
		"Node.js & React Engineer":                     {"React", "Node.js"},
		"Restaurant Manager":                           {},
		"AWS DevOps":                                   {"AWS"},
	}
	for title, want := range cases {
		j := &Job{Title: title}
		ScoreJob(j, nil)
		if !slices.Equal(j.Tags, want) {
			t.Errorf("%q tags = %v, want %v", title, j.Tags, want)
		}
	}
}

func TestScoreJobPreferredKeywords(t *testing.T) {
	j := &Job{Title: "Senior Go Developer at Google"}
	ScoreJob(j, []string{"go developer", "golang"})
	if j.Score != 10+20 { // tag "Go" + one preferred keyword
		t.Errorf("score = %d, want 30", j.Score)
	}
}
