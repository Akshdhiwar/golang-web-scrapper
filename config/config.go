// config/config.go
package config

import (
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all application configuration
type Config struct {
	// Storage: Postgres when DatabaseURL is set, otherwise a local JSON file
	DatabaseURL string
	StoreFile   string

	// Telegram
	TelegramBotToken string
	TelegramChatID   string

	// Scraping targets
	Keywords  []string
	Locations []string // supports multiple cities e.g. Mumbai, Bangalore, Hyderabad

	// Scraping behavior
	MaxPages        int
	MinDelaySeconds int
	MaxDelaySeconds int
	MaxRetries      int

	// Scheduler
	CronSchedule string

	// Notifications
	DryRun bool // print Telegram messages instead of sending them
	// NotifyFirstRun sends jobs found on the very first run against an empty
	// store; by default that run only seeds the store, to avoid a flood.
	NotifyFirstRun bool

	// Feeds
	ProfilesFile   string
	CompaniesFile  string
	MaxConcurrency int // max browser-based scrape tasks running at once
}

// Load reads config from environment variables (and optional .env file)
func Load() *Config {
	// Load .env file if present (ignored if missing in production)
	_ = godotenv.Load()

	return &Config{
		DatabaseURL:      getEnv("DATABASE_URL", ""), // empty → local file store
		StoreFile:        getEnv("STORE_FILE", ".data/seen.json"),
		TelegramBotToken: getEnv("TELEGRAM_BOT_TOKEN", ""),
		TelegramChatID:   getEnv("TELEGRAM_CHAT_ID", ""),

		Keywords:  splitEnv("KEYWORDS", []string{"frontend developer", "backend developer", "fullstack developer"}),
		Locations: splitEnv("LOCATIONS", []string{"Mumbai", "Bangalore", "Hyderabad", "Delhi", "Pune"}),

		MaxPages:        getEnvInt("MAX_PAGES", 3),
		MinDelaySeconds: getEnvInt("MIN_DELAY_SECONDS", 1),
		MaxDelaySeconds: getEnvInt("MAX_DELAY_SECONDS", 3),
		MaxRetries:      getEnvInt("MAX_RETRIES", 3),

		CronSchedule: getEnv("CRON_SCHEDULE", "0 * * * *"), // every hour

		DryRun:         strings.EqualFold(os.Getenv("DRY_RUN"), "true"),
		NotifyFirstRun: strings.EqualFold(os.Getenv("NOTIFY_FIRST_RUN"), "true"),

		ProfilesFile:   getEnv("PROFILES_FILE", "profiles.yaml"),
		CompaniesFile:  getEnv("COMPANIES_FILE", "companies.yaml"),
		MaxConcurrency: max(getEnvInt("MAX_CONCURRENCY", 4), 1),
	}
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultVal
}

func splitEnv(key string, defaultVal []string) []string {
	if v := os.Getenv(key); v != "" {
		parts := strings.Split(v, ",")
		result := make([]string, 0, len(parts))
		for _, p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	}
	return defaultVal
}
