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
	// Database
	DatabaseURL string

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
	NotificationBatchSize int
}

// Load reads config from environment variables (and optional .env file)
func Load() *Config {
	// Load .env file if present (ignored if missing in production)
	_ = godotenv.Load()

	return &Config{
		DatabaseURL:      getEnv("DATABASE_URL", "postgres://postgres:password@localhost:5432/jobscraper?sslmode=disable"),
		TelegramBotToken: getEnv("TELEGRAM_BOT_TOKEN", ""),
		TelegramChatID:   getEnv("TELEGRAM_CHAT_ID", ""),

		Keywords:  splitEnv("KEYWORDS", []string{"frontend developer", "backend developer", "fullstack developer"}),
		Locations: splitEnv("LOCATIONS", []string{"Mumbai", "Bangalore", "Hyderabad", "Delhi", "Pune"}),

		MaxPages:        getEnvInt("MAX_PAGES", 3),
		MinDelaySeconds: getEnvInt("MIN_DELAY_SECONDS", 1),
		MaxDelaySeconds: getEnvInt("MAX_DELAY_SECONDS", 3),
		MaxRetries:      getEnvInt("MAX_RETRIES", 3),

		CronSchedule: getEnv("CRON_SCHEDULE", "0 * * * *"), // every hour

		NotificationBatchSize: getEnvInt("NOTIFICATION_BATCH_SIZE", 10),
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
