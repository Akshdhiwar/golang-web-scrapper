// utils/helpers.go
package utils

import (
	"math/rand"
	"strings"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// UserAgents is a pool of realistic browser user-agent strings
var UserAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:124.0) Gecko/20100101 Firefox/124.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_3_1) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.3.1 Safari/605.1.15",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36 Edg/121.0.0.0",
}

// RandomUserAgent returns a random user agent from the pool
func RandomUserAgent() string {
	return UserAgents[rand.Intn(len(UserAgents))]
}

// RandomDelay sleeps for a random duration between min and max seconds
func RandomDelay(minSec, maxSec int) {
	if minSec >= maxSec {
		time.Sleep(time.Duration(minSec) * time.Second)
		return
	}
	delay := minSec + rand.Intn(maxSec-minSec)
	time.Sleep(time.Duration(delay) * time.Second)
}

// NewLogger creates a production-ready zap logger
func NewLogger() (*zap.Logger, error) {
	cfg := zap.NewProductionConfig()
	cfg.EncoderConfig.TimeKey = "timestamp"
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	cfg.Encoding = "console"

	return cfg.Build()
}

// Retry retries a function up to maxAttempts times with exponential backoff
func Retry(maxAttempts int, fn func() error) error {
	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		if err := fn(); err != nil {
			lastErr = err
			backoff := time.Duration(1<<uint(i)) * time.Second // 1s, 2s, 4s
			time.Sleep(backoff)
			continue
		}
		return nil
	}
	return lastErr
}

// ChunkSlice splits a slice into chunks of size n
func ChunkSlice[T any](slice []T, n int) [][]T {
	var chunks [][]T
	for i := 0; i < len(slice); i += n {
		end := i + n
		if end > len(slice) {
			end = len(slice)
		}
		chunks = append(chunks, slice[i:end])
	}
	return chunks
}

// Contains checks if a string slice contains a value (case-insensitive)
func Contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

// CleanText strips leading/trailing whitespace and collapses all internal
// whitespace (newlines, tabs, multiple spaces) down to a single space.
// Use this on any field scraped from HTML before storing or displaying it.
func CleanText(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}
