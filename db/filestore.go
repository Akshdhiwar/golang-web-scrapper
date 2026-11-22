// db/filestore.go
package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// How long the file store remembers links.
const (
	sentRetention    = 30 * 24 * time.Hour
	scrapedRetention = 14 * 24 * time.Hour
)

// FileStore keeps dedup state in a local JSON file so the scraper runs
// without any database. It is loaded once and rewritten atomically after
// each update.
type FileStore struct {
	path     string
	mu       sync.Mutex
	fresh    bool
	readOnly bool // dry run: answer queries, never write
	data     fileData
}

type fileData struct {
	// Profiles maps profile ID → job link → unix time it was first sent.
	Profiles map[string]map[string]int64 `json:"profiles"`
	// Scraped maps link → unix time it was last scraped.
	Scraped map[string]int64 `json:"scraped"`
}

// OpenFile loads (or starts) the store at path.
func OpenFile(path string) (*FileStore, error) {
	s := &FileStore{path: path, data: fileData{
		Profiles: map[string]map[string]int64{},
		Scraped:  map[string]int64{},
	}}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		s.fresh = true
		return s, nil
	case err != nil:
		return nil, fmt.Errorf("read store file: %w", err)
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		return nil, fmt.Errorf("parse store file %s: %w", path, err)
	}
	if s.data.Profiles == nil {
		s.data.Profiles = map[string]map[string]int64{}
	}
	if s.data.Scraped == nil {
		s.data.Scraped = map[string]int64{}
	}
	s.prune(time.Now())
	return s, nil
}

func (s *FileStore) MarkNew(_ context.Context, marks []Mark) (map[Key]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().Unix()
	isNew := map[Key]bool{}
	for _, m := range marks {
		if m.Job == nil || m.Job.Link == "" {
			continue
		}
		sent := s.data.Profiles[m.Profile]
		if sent == nil {
			sent = map[string]int64{}
			s.data.Profiles[m.Profile] = sent
		}
		if _, ok := sent[m.Job.Link]; ok {
			continue
		}
		isNew[Key{m.Profile, m.Job.Link}] = true
		if !s.readOnly {
			sent[m.Job.Link] = now
		}
	}
	s.fresh = false
	return isNew, s.saveLocked()
}

func (s *FileStore) SeenLinks(_ context.Context, links []string) (map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	for _, l := range links {
		if _, ok := s.data.Scraped[l]; ok {
			seen[l] = true
		}
	}
	return seen, nil
}

func (s *FileStore) RecordScraped(_ context.Context, links []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readOnly {
		return nil
	}
	now := time.Now().Unix()
	for _, l := range links {
		if l != "" {
			s.data.Scraped[l] = now
		}
	}
	return s.saveLocked()
}

func (s *FileStore) Fresh() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fresh
}

func (s *FileStore) Close() error { return nil }

// prune drops entries older than the retention windows.
func (s *FileStore) prune(now time.Time) {
	sentCut := now.Add(-sentRetention).Unix()
	for _, sent := range s.data.Profiles {
		for link, t := range sent {
			if t < sentCut {
				delete(sent, link)
			}
		}
	}
	scrapedCut := now.Add(-scrapedRetention).Unix()
	for link, t := range s.data.Scraped {
		if t < scrapedCut {
			delete(s.data.Scraped, link)
		}
	}
}

// saveLocked writes the file atomically (temp file + rename).
func (s *FileStore) saveLocked() error {
	if s.readOnly {
		return nil
	}
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create store dir: %w", err)
		}
	}
	raw, err := json.Marshal(s.data)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".seen-*.json")
	if err != nil {
		return fmt.Errorf("write store file: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("write store file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("replace store file: %w", err)
	}
	return nil
}
