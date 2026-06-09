// scheduler/scheduler.go
package scheduler

import (
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"go.uber.org/zap"

	"github.com/yourname/job-scraper/config"
	"github.com/yourname/job-scraper/db"
	"github.com/yourname/job-scraper/models"
	"github.com/yourname/job-scraper/notifier"
	"github.com/yourname/job-scraper/scrapers"
)

// Pipeline orchestrates scraping, deduplication, storage, and notifications
type Pipeline struct {
	cfg      *config.Config
	db       *db.DB
	scrapers []scrapers.Scraper
	notifier *notifier.TelegramNotifier
	logger   *zap.Logger
	cron     *cron.Cron
	mu       sync.Mutex // prevents overlapping runs
	running  bool
}

// New creates a new Pipeline
func New(
	cfg *config.Config,
	database *db.DB,
	scraperList []scrapers.Scraper,
	tgNotifier *notifier.TelegramNotifier,
	logger *zap.Logger,
) *Pipeline {
	return &Pipeline{
		cfg:      cfg,
		db:       database,
		scrapers: scraperList,
		notifier: tgNotifier,
		logger:   logger,
		cron:     cron.New(cron.WithSeconds()),
	}
}

// Start registers the cron job and starts the scheduler
func (p *Pipeline) Start() error {
	// Run immediately on startup
	go p.Run()

	// Schedule recurring runs
	entryID, err := p.cron.AddFunc(p.cfg.CronSchedule, func() {
		p.Run()
	})
	if err != nil {
		return fmt.Errorf("failed to register cron job: %w", err)
	}

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
	p.logger.Info("=== Pipeline run started ===",
		zap.Time("at", startTime),
		zap.Strings("locations", p.cfg.Locations),
		zap.Int("combinations", len(p.scrapers)*len(p.cfg.Keywords)*len(p.cfg.Locations)),
	)

	var (
		allNewJobs  []*models.Job
		totalJobs   int
		sourceNames []string
		wg          sync.WaitGroup
		mu          sync.Mutex
	)

	// Run each scraper concurrently per keyword × location combination
	totalCombinations := len(p.scrapers) * len(p.cfg.Keywords) * len(p.cfg.Locations)
	resultCh := make(chan scrapers.ScrapeResult, totalCombinations)

	for _, scraper := range p.scrapers {
		for _, keyword := range p.cfg.Keywords {
			for _, location := range p.cfg.Locations {
				wg.Add(1)
				go func(sc scrapers.Scraper, kw, loc string) {
					defer wg.Done()
					p.logger.Info("Starting scraper",
						zap.String("source", sc.Name()),
						zap.String("keyword", kw),
						zap.String("location", loc),
					)
					jobs, err := sc.Scrape(kw, loc, p.cfg.MaxPages)
					resultCh <- scrapers.ScrapeResult{
						Source: sc.Name(),
						Jobs:   jobs,
						Err:    err,
					}
				}(scraper, keyword, location)
			}
		}
	}

	// Close channel when all goroutines finish
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Process results as they arrive
	sourceSet := make(map[string]bool)
	for result := range resultCh {
		if result.Err != nil {
			p.logger.Error("Scraper failed",
				zap.String("source", result.Source),
				zap.Error(result.Err),
			)
			// Alert via Telegram but don't abort the whole pipeline
			_ = p.notifier.SendAlert(fmt.Sprintf("Scraper `%s` failed: %v", result.Source, result.Err))
			continue
		}

		sourceSet[result.Source] = true
		p.logger.Info("Scraper finished",
			zap.String("source", result.Source),
			zap.Int("raw_jobs", len(result.Jobs)),
		)

		// Deduplicate and insert
		newJobs := p.processJobs(result.Jobs)

		mu.Lock()
		totalJobs += len(result.Jobs)
		allNewJobs = append(allNewJobs, newJobs...)
		mu.Unlock()
	}

	for src := range sourceSet {
		sourceNames = append(sourceNames, src)
	}

	duration := time.Since(startTime)
	p.logger.Info("=== Pipeline run complete ===",
		zap.Duration("duration", duration),
		zap.Int("total_scraped", totalJobs),
		zap.Int("new_jobs", len(allNewJobs)),
	)

	// Send notifications for new jobs
	if len(allNewJobs) > 0 {
		if err := p.notifier.SendJobsBatch(allNewJobs); err != nil {
			p.logger.Error("Failed to send job notifications", zap.Error(err))
		}
	}

	// Send run summary
	_ = p.notifier.SendSummary(totalJobs, len(allNewJobs), sourceNames, duration)
}

// processJobs deduplicates against the database and inserts new jobs
// Returns only the newly inserted jobs
func (p *Pipeline) processJobs(jobs []*models.Job) []*models.Job {
	var newJobs []*models.Job

	for _, job := range jobs {
		// Score and tag the job
		models.ScoreJob(job, p.cfg.Keywords)

		inserted, err := p.db.InsertJob(job)
		if err != nil {
			p.logger.Error("Failed to insert job",
				zap.String("title", job.Title),
				zap.String("link", job.Link),
				zap.Error(err),
			)
			continue
		}

		if inserted {
			newJobs = append(newJobs, job)
			p.logger.Info("New job inserted",
				zap.String("title", job.Title),
				zap.String("company", job.Company),
				zap.String("source", job.Source),
				zap.Int("score", job.Score),
			)
		} else {
			p.logger.Debug("Duplicate job skipped",
				zap.String("link", job.Link),
			)
		}
	}

	return newJobs
}
