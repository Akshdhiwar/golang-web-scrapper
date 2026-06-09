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
	"github.com/yourname/job-scraper/utils"
)

func main() {
	// ── 1. Logger ──────────────────────────────────────────────
	logger, err := utils.NewLogger()
	if err != nil {
		panic("failed to initialize logger: " + err.Error())
	}
	defer logger.Sync()

	logger.Info("Starting Job Scraper",
		zap.String("version", "1.0.0"),
	)

	// ── 2. Config ──────────────────────────────────────────────
	cfg := config.Load()
	logger.Info("Configuration loaded",
		zap.Strings("keywords", cfg.Keywords),
		zap.Strings("locations", cfg.Locations),
		zap.String("schedule", cfg.CronSchedule),
	)

	// ── 3. Database ────────────────────────────────────────────
	database, err := db.New(cfg.DatabaseURL, logger)
	if err != nil {
		logger.Fatal("Failed to connect to database", zap.Error(err))
	}
	defer database.Close()

	if err := database.Migrate(); err != nil {
		logger.Fatal("Database migration failed", zap.Error(err))
	}

	// ── 4. Notifier ────────────────────────────────────────────
	tgNotifier := notifier.NewTelegramNotifier(
		cfg.TelegramBotToken,
		cfg.TelegramChatID,
		cfg.NotificationBatchSize,
		logger,
	)

	if tgNotifier.IsConfigured() {
		logger.Info("Telegram notifications enabled")
	} else {
		logger.Warn("Telegram not configured — notifications disabled. Set TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID.")
	}

	// ── 5. Scrapers ────────────────────────────────────────────
	scraperList := []scrapers.Scraper{
		scrapers.NewIndeedScraper(logger, cfg.MinDelaySeconds, cfg.MaxDelaySeconds, cfg.MaxRetries),
		scrapers.NewNaukriScraper(logger, cfg.MinDelaySeconds, cfg.MaxDelaySeconds, cfg.MaxRetries),
		scrapers.NewLinkedInScraper(logger, cfg.MinDelaySeconds, cfg.MaxDelaySeconds, cfg.MaxRetries),
	}

	logger.Info("Scrapers registered",
		zap.Int("count", len(scraperList)),
	)

	// ── 6. Pipeline / Scheduler ────────────────────────────────
	pipeline := scheduler.New(cfg, database, scraperList, tgNotifier, logger)

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
