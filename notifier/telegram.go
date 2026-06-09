// notifier/telegram.go
package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/yourname/job-scraper/models"
	"github.com/yourname/job-scraper/utils"
)

const telegramAPIBase = "https://api.telegram.org/bot"

// TelegramNotifier sends job notifications via Telegram Bot API
type TelegramNotifier struct {
	botToken  string
	chatID    string
	batchSize int
	logger    *zap.Logger
	client    *http.Client
}

// NewTelegramNotifier creates a new Telegram notifier
func NewTelegramNotifier(botToken, chatID string, batchSize int, logger *zap.Logger) *TelegramNotifier {
	return &TelegramNotifier{
		botToken:  botToken,
		chatID:    chatID,
		batchSize: batchSize,
		logger:    logger,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// IsConfigured returns true if bot credentials are provided
func (t *TelegramNotifier) IsConfigured() bool {
	return t.botToken != "" && t.chatID != ""
}

// SendJobsBatch sends a batch of new jobs as a single Telegram message (markdown)
func (t *TelegramNotifier) SendJobsBatch(jobs []*models.Job) error {
	if !t.IsConfigured() {
		t.logger.Warn("Telegram not configured — skipping notifications")
		return nil
	}

	if len(jobs) == 0 {
		return nil
	}

	// Split into batches to avoid Telegram message length limits
	batches := utils.ChunkSlice(jobs, t.batchSize)

	for i, batch := range batches {
		message := formatBatchMessage(batch, i+1, len(batches))

		if err := t.sendMessage(message); err != nil {
			t.logger.Error("Failed to send Telegram batch",
				zap.Int("batch", i+1),
				zap.Error(err),
			)
			// Continue sending remaining batches
			continue
		}

		t.logger.Info("Sent Telegram notification batch",
			zap.Int("batch", i+1),
			zap.Int("jobs_in_batch", len(batch)),
		)

		// Small delay between batches to avoid Telegram rate limiting (30 msg/sec)
		if i < len(batches)-1 {
			time.Sleep(500 * time.Millisecond)
		}
	}

	return nil
}

// SendSummary sends a summary message after the scraping run
func (t *TelegramNotifier) SendSummary(total, newJobs int, sources []string, duration time.Duration) error {
	if !t.IsConfigured() {
		return nil
	}

	msg := fmt.Sprintf(
		"📊 *Scraping Run Complete*\n\n"+
			"⏱ Duration: `%s`\n"+
			"🔍 Sources: `%s`\n"+
			"📦 Total Scraped: `%d`\n"+
			"✅ New Jobs Found: `%d`\n"+
			"🕐 Next run in: `~1 hour`",
		duration.Round(time.Second).String(),
		strings.Join(sources, ", "),
		total,
		newJobs,
	)

	return t.sendMessage(msg)
}

// SendAlert sends a plain error alert message
func (t *TelegramNotifier) SendAlert(alertMsg string) error {
	if !t.IsConfigured() {
		return nil
	}
	msg := fmt.Sprintf("⚠️ *Job Scraper Alert*\n\n%s", alertMsg)
	return t.sendMessage(msg)
}

// sendMessage sends a single Markdown-formatted message to the configured chat
func (t *TelegramNotifier) sendMessage(text string) error {
	apiURL := fmt.Sprintf("%s%s/sendMessage", telegramAPIBase, t.botToken)

	payload := map[string]interface{}{
		"chat_id":    t.chatID,
		"text":       text,
		"parse_mode": "Markdown",
		// Disable link previews to keep messages clean
		"disable_web_page_preview": true,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal telegram payload: %w", err)
	}

	var resp *http.Response
	retryErr := utils.Retry(3, func() error {
		var reqErr error
		resp, reqErr = t.client.Post(apiURL, "application/json", bytes.NewReader(body))
		return reqErr
	})

	if retryErr != nil {
		return fmt.Errorf("telegram API request failed: %w", retryErr)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram API returned status %d", resp.StatusCode)
	}

	return nil
}

// formatBatchMessage formats a batch of jobs into a clean Telegram message
func formatBatchMessage(jobs []*models.Job, batchNum, totalBatches int) string {
	var sb strings.Builder

	if totalBatches > 1 {
		sb.WriteString(fmt.Sprintf("🚀 *New Jobs Found* (Batch %d/%d)\n", batchNum, totalBatches))
	} else {
		sb.WriteString("🚀 *New Jobs Found!*\n")
	}
	sb.WriteString(fmt.Sprintf("_%d new listings_\n", len(jobs)))
	sb.WriteString("━━━━━━━━━━━━━━━━━━\n\n")

	for _, job := range jobs {
		// cleanField collapses any HTML whitespace noise before displaying
		title := utils.CleanText(job.Title)
		company := utils.CleanText(job.Company)
		location := utils.CleanText(job.Location)
		postedAt := utils.CleanText(job.PostedAt)

		sb.WriteString(fmt.Sprintf("*%s*\n", escapeMarkdown(title)))
		sb.WriteString(fmt.Sprintf("🏢 %s\n", escapeMarkdown(company)))
		if location != "" {
			sb.WriteString(fmt.Sprintf("📍 %s\n", escapeMarkdown(location)))
		}
		sb.WriteString(fmt.Sprintf("🌐 `%s`\n", sourceLabel(job.Source)))

		if postedAt != "" {
			sb.WriteString(fmt.Sprintf("🕐 %s\n", postedAt))
		}

		if len(job.Tags) > 0 {
			sb.WriteString(fmt.Sprintf("🏷 %s\n", strings.Join(job.Tags, " · ")))
		}

		sb.WriteString(fmt.Sprintf("[🔗 Apply Here](%s)\n", job.Link))
		sb.WriteString("\n")
	}

	return sb.String()
}

// escapeMarkdown escapes special Markdown characters
func escapeMarkdown(s string) string {
	// Escape characters that break Telegram's Markdown v1
	replacer := strings.NewReplacer(
		"_", "\\_",
		"*", "\\*",
		"`", "\\`",
		"[", "\\[",
	)
	return replacer.Replace(s)
}

// sourceLabel returns a human-readable label for a source
func sourceLabel(source string) string {
	switch source {
	case models.SourceLinkedIn:
		return "LinkedIn"
	case models.SourceIndeed:
		return "Indeed"
	case models.SourceNaukri:
		return "Naukri"
	default:
		return source
	}
}
