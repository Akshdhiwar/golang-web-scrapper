// scheduler/scheduler.go
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robfig/cron/v3"
	"go.uber.org/zap"

	"github.com/yourname/job-scraper/config"
	"github.com/yourname/job-scraper/db"
	"github.com/yourname/job-scraper/filter"
	"github.com/yourname/job-scraper/models"
	"github.com/yourname/job-scraper/notifier"
	"github.com/yourname/job-scraper/scrapers"
)

// feedTimeout bounds how long a single feed source (API / career pages) may take.
const feedTimeout = 5 * time.Minute

// Pipeline orchestrates scraping, filtering, deduplication, storage, and
// notifications for every profile (feed).
type Pipeline struct {
	cfg      *config.Config
	store    db.Store
	profiles []config.Profile
	filters  map[string]*filter.Filter
	scrapers map[string]scrapers.Scraper
	feeds    map[string]scrapers.Feed
	notifier *notifier.TelegramNotifier
	logger   *zap.Logger
	cron     *cron.Cron
	mu       sync.Mutex // prevents overlapping runs
	running  bool
}

// New creates a Pipeline. Every source a profile names must be provided in
// scraperMap or feedMap.
func New(
	cfg *config.Config,
	store db.Store,
	profiles []config.Profile,
	scraperMap map[string]scrapers.Scraper,
	feedMap map[string]scrapers.Feed,
	tgNotifier *notifier.TelegramNotifier,
	logger *zap.Logger,
) (*Pipeline, error) {
	filters := make(map[string]*filter.Filter, len(profiles))
	for _, p := range profiles {
		f, err := filter.New(p)
		if err != nil {
			return nil, err
		}
		filters[p.ID] = f
		for _, s := range p.Sources {
			if scraperMap[s] == nil && feedMap[s] == nil {
				return nil, fmt.Errorf("profile %q: source %q is not available", p.ID, s)
			}
		}
	}
	return &Pipeline{
		cfg:      cfg,
		store:    store,
		profiles: profiles,
		filters:  filters,
		scrapers: scraperMap,
		feeds:    feedMap,
		notifier: tgNotifier,
		logger:   logger,
		cron:     cron.New(), // standard 5-field cron, e.g. "0 * * * *"
	}, nil
}

// Start registers the cron job and starts the scheduler
func (p *Pipeline) Start() error {
	entryID, err := p.cron.AddFunc(p.cfg.CronSchedule, p.Run)
	if err != nil {
		return fmt.Errorf("failed to register cron job: %w", err)
	}

	// Run immediately on startup, then on schedule
	go p.Run()
	p.cron.Start()
	p.logger.Info("Scheduler started",
		zap.String("schedule", p.cfg.CronSchedule),
		zap.Int("entry_id", int(entryID)),
	)
	return nil
}

// Stop gracefully stops the scheduler
func (p *Pipeline) Stop() {
	ctx := p.cron.Stop()
	<-ctx.Done()
	p.logger.Info("Scheduler stopped")
}

// searchTask is one query against one search source, shared by every
// profile that asked for exactly the same query.
type searchTask struct {
	source   string
	query    scrapers.Query
	profiles []string
}

// result is the output of one search task or one feed fetch.
type result struct {
	source  string
	task    *searchTask // nil for feeds
	jobs    []*models.Job
	err     error
	elapsed time.Duration
}

// buildTasks expands every profile's keywords × locations × search sources
// and merges identical queries across profiles.
func (p *Pipeline) buildTasks() []*searchTask {
	byKey := map[string]*searchTask{}
	var tasks []*searchTask
	for _, prof := range p.profiles {
		for _, src := range prof.Sources {
			if !config.SearchSources[src] {
				continue
			}
			for _, kw := range prof.Keywords {
				for _, loc := range prof.Locations {
					q := scrapers.Query{
						Keyword:           kw,
						Location:          loc,
						MaxPages:          prof.MaxPages,
						PostedWithinHours: prof.PostedWithinHours,
						Seen:              p.seenBefore,
					}
					key := strings.ToLower(fmt.Sprintf("%s|%s|%s|%d|%d", src, kw, loc, q.MaxPages, q.PostedWithinHours))
					if t, ok := byKey[key]; ok {
						if !slices.Contains(t.profiles, prof.ID) {
							t.profiles = append(t.profiles, prof.ID)
						}
						continue
					}
					t := &searchTask{source: src, query: q, profiles: []string{prof.ID}}
					byKey[key] = t
					tasks = append(tasks, t)
				}
			}
		}
	}
	return tasks
}

// feedWindows returns, for each feed source in use, the oldest posting time
// any profile using it cares about.
func (p *Pipeline) feedWindows(now time.Time) map[string]time.Time {
	since := map[string]time.Time{}
	for _, prof := range p.profiles {
		for _, src := range prof.Sources {
			if !config.FeedSources[src] {
				continue
			}
			s := now.Add(-time.Duration(prof.FeedMaxAgeHours) * time.Hour)
			if cur, ok := since[src]; !ok || s.Before(cur) {
				since[src] = s
			}
		}
	}
	return since
}

// Run executes one full scraping pipeline run
func (p *Pipeline) Run() {
	p.mu.Lock()
	if p.running {
		p.logger.Warn("Pipeline already running — skipping this tick")
		p.mu.Unlock()
		return
	}
	p.running = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.running = false
		p.mu.Unlock()
	}()

	startTime := time.Now()
	// A store that started empty is seeded on its first run instead of
	// flooding the chat with every job currently listed.
	seedOnly := p.store.Fresh() && !p.cfg.NotifyFirstRun
	tasks := p.buildTasks()
	windows := p.feedWindows(startTime)
	p.logger.Info("=== Pipeline run started ===",
		zap.Int("profiles", len(p.profiles)),
		zap.Int("search_tasks", len(tasks)),
		zap.Int("feeds", len(windows)),
		zap.Int("max_concurrency", p.cfg.MaxConcurrency),
		zap.Bool("seed_only", seedOnly),
	)

	results := p.collect(tasks, windows)

	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	if err := p.store.RecordScraped(ctx, scrapedLinks(results)); err != nil {
		p.logger.Warn("Failed to record scraped links (early paging stop disabled next run)", zap.Error(err))
	}
	newByProfile, total, sourceNames, err := p.process(ctx, results, startTime)
	if err != nil {
		p.logger.Error("Failed to store jobs", zap.Error(err))
		_ = p.notifier.SendAlert("Could not save this run's jobs", []string{err.Error()})
	}
	duration := time.Since(startTime)

	p.logTimings(results, duration)
	p.alertFailures(results)

	var digests []notifier.Digest
	newTotal := 0
	for _, prof := range p.profiles {
		jobs := newByProfile[prof.ID]
		newTotal += len(jobs)
		if len(jobs) > 0 {
			digests = append(digests, notifier.Digest{Label: prof.Label, Jobs: jobs})
		}
	}

	p.logger.Info("=== Pipeline run complete ===",
		zap.Duration("duration", duration),
		zap.Int("total_scraped", total),
		zap.Int("new_jobs", newTotal),
	)
	if seedOnly {
		if p.cfg.DryRun {
			p.logger.Info("Empty store — a real first run would only record these jobs, not send them (dry run: nothing saved). Set NOTIFY_FIRST_RUN=true to preview them",
				zap.Int("jobs", newTotal))
		} else {
			p.logger.Info("First run on an empty store — seeded jobs, nothing sent (set NOTIFY_FIRST_RUN=true to send them)",
				zap.Int("seeded", newTotal))
		}
		return
	}
	stats := notifier.RunStats{
		Duration: duration,
		Scraped:  total,
		Sources:  sourceNames,
		NextRun:  p.nextRun(time.Now()),
	}
	if err := p.notifier.SendDigests(digests, stats); err != nil {
		p.logger.Error("Failed to send job notifications", zap.Error(err))
	}
}

// storeTimeout bounds the store calls made after scraping.
const storeTimeout = 2 * time.Minute

// seenBefore is handed to scrapers (Query.Seen) so they can stop paging once
// they reach results an earlier run already scraped. Errors just disable it.
func (p *Pipeline) seenBefore(links []string) map[string]bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	seen, err := p.store.SeenLinks(ctx, links)
	if err != nil {
		p.logger.Debug("Seen-links lookup failed", zap.Error(err))
		return nil
	}
	return seen
}

// nextRun returns when the cron schedule fires next (zero if unparsable).
func (p *Pipeline) nextRun(now time.Time) time.Time {
	sched, err := cron.ParseStandard(p.cfg.CronSchedule)
	if err != nil {
		return time.Time{}
	}
	return sched.Next(now)
}

// scrapedLinks lists every link returned this run.
func scrapedLinks(results []result) []string {
	var links []string
	for _, r := range results {
		for _, j := range r.jobs {
			links = append(links, j.Link)
		}
	}
	return links
}

// collect runs feed fetches (in parallel) and search tasks (through a worker
// pool of MaxConcurrency) and returns every result.
func (p *Pipeline) collect(tasks []*searchTask, windows map[string]time.Time) []result {
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		results []result
	)
	add := func(r result) {
		mu.Lock()
		results = append(results, r)
		mu.Unlock()
	}

	for name, since := range windows {
		feed := p.feeds[name]
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), feedTimeout)
			defer cancel()
			t0 := time.Now()
			jobs, err := feed.Fetch(ctx, since)
			p.logger.Info("Feed fetched", zap.String("source", name),
				zap.Int("jobs", len(jobs)), zap.Duration("elapsed", time.Since(t0)), zap.Error(err))
			add(result{source: name, jobs: jobs, err: err, elapsed: time.Since(t0)})
		}()
	}

	// Each search source gets its own small worker pool so a slow or
	// throttled site (LinkedIn) can't starve the others.
	bySource := map[string][]*searchTask{}
	for _, t := range tasks {
		bySource[t.source] = append(bySource[t.source], t)
	}
	for src, srcTasks := range bySource {
		p.runSource(src, srcTasks, &wg, add)
	}
	wg.Wait()
	return results
}

// sourceLimits caps concurrent tasks per search source (further capped by
// MAX_CONCURRENCY). LinkedIn throttles logged-out visitors quickly.
var sourceLimits = map[string]int{"linkedin": 2, "indeed": 2}

// sourcePause is the delay a worker waits between tasks for that source.
var sourcePause = map[string]time.Duration{"linkedin": 2 * time.Second}

// maxConsecutiveBlocks stops a source for the rest of the run after this many
// blocked tasks in a row — more requests would only deepen the block.
const maxConsecutiveBlocks = 3

func (p *Pipeline) runSource(src string, tasks []*searchTask, wg *sync.WaitGroup, add func(result)) {
	workers := min(p.cfg.MaxConcurrency, len(tasks))
	if lim, ok := sourceLimits[src]; ok {
		workers = min(workers, lim)
	}
	queue := make(chan *searchTask, len(tasks))
	for _, t := range tasks {
		queue <- t
	}
	close(queue)

	var blocks atomic.Int32
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range queue {
				if blocks.Load() >= maxConsecutiveBlocks {
					add(result{source: src, task: t, err: fmt.Errorf("skipped: %w", scrapers.ErrBlocked)})
					continue
				}
				t0 := time.Now()
				jobs, err := p.scrapers[src].Scrape(t.query)
				if errors.Is(err, scrapers.ErrBlocked) {
					if blocks.Add(1) == maxConsecutiveBlocks {
						p.logger.Warn("Source keeps blocking us — skipping its remaining tasks this run",
							zap.String("source", src))
					}
				} else if err == nil {
					blocks.Store(0)
				}
				p.logger.Info("Search task done",
					zap.String("source", src),
					zap.String("keyword", t.query.Keyword),
					zap.String("location", t.query.Location),
					zap.Int("jobs", len(jobs)),
					zap.Duration("elapsed", time.Since(t0)),
					zap.Error(err),
				)
				add(result{source: src, task: t, jobs: jobs, err: err, elapsed: time.Since(t0)})
				time.Sleep(sourcePause[src])
			}
		}()
	}
}

// process routes results to profiles, filters them, stores them and returns
// the jobs that are new for each profile.
func (p *Pipeline) process(ctx context.Context, results []result, now time.Time) (map[string][]*models.Job, int, []string, error) {
	// Candidate jobs per profile, in arrival order
	candidates := map[string][]*models.Job{}
	total := 0
	sourceSet := map[string]bool{}

	for _, r := range results {
		total += len(r.jobs)
		if len(r.jobs) > 0 {
			sourceSet[r.source] = true
		}
		var targets []string
		if r.task != nil {
			targets = r.task.profiles
			for _, j := range r.jobs {
				if j.Location == "" { // trust the searched location when the site gave none
					j.Location = r.task.query.Location
				}
			}
		} else {
			for _, prof := range p.profiles {
				if prof.HasSource(r.source) {
					targets = append(targets, prof.ID)
				}
			}
		}
		for _, id := range targets {
			candidates[id] = append(candidates[id], r.jobs...)
		}
	}

	// Filter per profile, then record every match in one bulk store call.
	matched := map[string][]*models.Job{}
	var marks []db.Mark
	for _, prof := range p.profiles {
		f := p.filters[prof.ID]
		seen := map[string]bool{}
		for _, job := range candidates[prof.ID] {
			if job.Link == "" || seen[job.Link] || !f.Match(job, now) {
				continue
			}
			seen[job.Link] = true
			matched[prof.ID] = append(matched[prof.ID], job)
			marks = append(marks, db.Mark{Profile: prof.ID, Job: job})
		}
	}

	sourceNames := make([]string, 0, len(sourceSet))
	for s := range sourceSet {
		sourceNames = append(sourceNames, s)
	}
	slices.Sort(sourceNames)

	isNew, err := p.store.MarkNew(ctx, marks)
	if err != nil {
		return nil, total, sourceNames, err
	}

	newByProfile := map[string][]*models.Job{}
	for _, prof := range p.profiles {
		for _, job := range matched[prof.ID] {
			if !isNew[db.Key{Profile: prof.ID, Link: job.Link}] {
				continue
			}
			// Copy: the same job can be new for several profiles, and each
			// scores it against its own keywords.
			jc := *job
			models.ScoreJob(&jc, prof.Keywords)
			newByProfile[prof.ID] = append(newByProfile[prof.ID], &jc)
		}
		p.logger.Info("Feed processed",
			zap.String("feed", prof.ID),
			zap.Int("candidates", len(candidates[prof.ID])),
			zap.Int("matched_filters", len(matched[prof.ID])),
			zap.Int("new", len(newByProfile[prof.ID])),
		)
	}
	return newByProfile, total, sourceNames, nil
}

// alertFailures sends one Telegram alert listing every failing source.
// Indeed is left out because it fails often and would spam the chat.
func (p *Pipeline) alertFailures(results []result) {
	type failure struct {
		failed, total int
		firstErr      error
	}
	bySource := map[string]*failure{}
	var order []string
	for _, r := range results {
		f, ok := bySource[r.source]
		if !ok {
			f = &failure{}
			bySource[r.source] = f
			order = append(order, r.source)
		}
		f.total++
		if r.err != nil {
			f.failed++
			if f.firstErr == nil {
				f.firstErr = r.err
			}
		}
	}
	slices.Sort(order)
	var lines []string
	for _, src := range order {
		f := bySource[src]
		if f.failed == 0 {
			continue
		}
		p.logger.Error("Source had failures",
			zap.String("source", src), zap.Int("failed", f.failed), zap.Int("total", f.total), zap.Error(f.firstErr))
		if src == models.SourceIndeed {
			continue
		}
		if errors.Is(f.firstErr, scrapers.ErrBlocked) {
			lines = append(lines, fmt.Sprintf("%s — blocked on %d/%d tasks", src, f.failed, f.total))
		} else {
			lines = append(lines, fmt.Sprintf("%s — failed on %d/%d tasks: %v", src, f.failed, f.total, f.firstErr))
		}
	}
	if len(lines) > 0 {
		_ = p.notifier.SendAlert("Some sources had trouble", lines)
	}
}

// logTimings logs the slowest task and per-source averages.
func (p *Pipeline) logTimings(results []result, wall time.Duration) {
	if len(results) == 0 {
		return
	}
	elapsed := map[string]time.Duration{}
	count := map[string]int{}
	slowest := results[0]
	for _, r := range results {
		elapsed[r.source] += r.elapsed
		count[r.source]++
		if r.elapsed > slowest.elapsed {
			slowest = r
		}
	}
	desc := slowest.source
	if slowest.task != nil {
		desc = fmt.Sprintf("%s/%s/%s", slowest.source, slowest.task.query.Keyword, slowest.task.query.Location)
	}
	p.logger.Info("=== Timing Summary ===",
		zap.Duration("wall_clock_total", wall),
		zap.String("slowest_task", fmt.Sprintf("%s (%s)", desc, slowest.elapsed.Round(time.Millisecond))),
	)
	sources := make([]string, 0, len(elapsed))
	for s := range elapsed {
		sources = append(sources, s)
	}
	slices.Sort(sources)
	for _, s := range sources {
		p.logger.Info("Source avg",
			zap.String("source", s),
			zap.Duration("avg_per_task", elapsed[s]/time.Duration(count[s])),
			zap.Int("tasks", count[s]),
		)
	}
}
