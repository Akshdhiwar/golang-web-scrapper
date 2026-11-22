// cmd/main.go
package main

import (
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/yourname/job-scraper/config"
	"github.com/yourname/job-scraper/db"
	"github.com/yourname/job-scraper/notifier"
	"github.com/yourname/job-scraper/scheduler"
	"github.com/yourname/job-scraper/scrapers"
	"github.com/yourname/job-scraper/scrapers/feeds"
	"github.com/yourname/job-scraper/utils"
)

func main() {
	// ── 1. Logger ──────────────────────────────────────────────
	logger, err := utils.NewLogger()
	if err != nil {
		panic("failed to initialize logger: " + err.Error())
	}
	defer logger.Sync()

	logger.Info("Starting Job Scraper", zap.String("version", "2.0.0"))

	// ── 2. Config & feeds ──────────────────────────────────────
	cfg := config.Load()
	profiles, err := config.LoadProfiles(cfg.ProfilesFile, cfg)
	if err != nil {
		logger.Fatal("Invalid profiles", zap.Error(err))
	}
	companies, err := config.LoadCompanies(cfg.CompaniesFile)
	if err != nil {
		logger.Fatal("Invalid companies", zap.Error(err))
	}

	profileIDs := make([]string, len(profiles))
	for i, p := range profiles {
		profileIDs[i] = p.ID
		logger.Info("Feed loaded",
			zap.String("id", p.ID),
			zap.String("label", p.Label),
			zap.Strings("sources", p.Sources),
		)
	}
	logger.Info("Configuration loaded",
		zap.Int("feeds", len(profiles)),
		zap.Int("career_pages", len(companies)),
		zap.String("schedule", cfg.CronSchedule),
		zap.Bool("dry_run", cfg.DryRun),
	)

	// ── 3. Store (Postgres if DATABASE_URL is set, else a local file) ──
	store, err := db.Open(cfg.DatabaseURL, cfg.StoreFile, profileIDs, cfg.DryRun, logger)
	if err != nil {
		logger.Fatal("Failed to open store", zap.Error(err))
	}
	defer store.Close()

	// ── 4. Notifier ────────────────────────────────────────────
	tgNotifier := notifier.NewTelegramNotifier(
		cfg.TelegramBotToken,
		cfg.TelegramChatID,
		cfg.DryRun,
		logger,
	)
	switch {
	case cfg.DryRun:
		logger.Info("DRY_RUN — Telegram messages will be printed, not sent")
	case tgNotifier.IsConfigured():
		logger.Info("Telegram notifications enabled")
	default:
		logger.Warn("Telegram not configured — notifications disabled. Set TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID.")
	}

	// ── 5. Sources ─────────────────────────────────────────────
	// Chromium is installed and launched lazily, the first time a browser
	// source actually needs it.
	browser := &scrapers.Browser{}
	defer browser.Close()

	scraperMap := map[string]scrapers.Scraper{
		config.SourceLinkedIn: scrapers.NewLinkedInScraper(logger, browser, cfg.MinDelaySeconds, cfg.MaxDelaySeconds, cfg.MaxRetries),
		config.SourceIndeed:   scrapers.NewIndeedScraper(logger, browser, cfg.MinDelaySeconds, cfg.MaxDelaySeconds, cfg.MaxRetries),
		config.SourceNaukri:   scrapers.NewNaukriScraper(logger, cfg.MinDelaySeconds, cfg.MaxDelaySeconds, cfg.MaxRetries),
	}
	feedMap := map[string]scrapers.Feed{
		config.SourceRemoteOK:  feeds.RemoteOK{},
		config.SourceRemotive:  feeds.Remotive{},
		config.SourceArbeitnow: feeds.Arbeitnow{},
		config.SourceHN:        feeds.HN{},
		config.SourceCareers:   feeds.Careers{Companies: companies, Logger: logger},
	}

	// ── 6. Pipeline / Scheduler ────────────────────────────────
	pipeline, err := scheduler.New(cfg, store, profiles, scraperMap, feedMap, tgNotifier, logger)
	if err != nil {
		logger.Fatal("Failed to build pipeline", zap.Error(err))
	}

	// ── RUN_ONCE mode (used by GitHub Actions) ─────────────────
	// When RUN_ONCE=true the scraper executes one full run then exits.
	// The external cron (GitHub Actions schedule) handles recurrence.
	if strings.EqualFold(os.Getenv("RUN_ONCE"), "true") {
		start := time.Now()
		logger.Info("RUN_ONCE mode — executing single pipeline run")
		pipeline.Run()
		elapsed := time.Since(start)
		logger.Info("RUN_ONCE complete",
			zap.Duration("elapsed", elapsed),
			zap.String("elapsed_human", elapsed.Round(time.Second).String()),
		)
		return
	}

	if err := pipeline.Start(); err != nil {
		logger.Fatal("Failed to start pipeline", zap.Error(err))
	}

	// ── 7. Graceful Shutdown ────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	sig := <-quit
	logger.Info("Shutdown signal received", zap.String("signal", sig.String()))

	pipeline.Stop()
	logger.Info("Job Scraper stopped cleanly")
}
