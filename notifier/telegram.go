// notifier/telegram.go
package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"go.uber.org/zap"

	"github.com/yourname/job-scraper/models"
	"github.com/yourname/job-scraper/utils"
)

const telegramAPIBase = "https://api.telegram.org/bot"

// TelegramNotifier sends job notifications via Telegram Bot API
type TelegramNotifier struct {
	botToken string
	chatID   string
	dryRun   bool
	logger   *zap.Logger
	client   *http.Client
}

// NewTelegramNotifier creates a new Telegram notifier
// In dry-run mode messages are printed to stdout instead of being sent.
func NewTelegramNotifier(botToken, chatID string, dryRun bool, logger *zap.Logger) *TelegramNotifier {
	return &TelegramNotifier{
		botToken: botToken,
		chatID:   chatID,
		dryRun:   dryRun,
		logger:   logger,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// IsConfigured returns true if bot credentials are provided
func (t *TelegramNotifier) IsConfigured() bool {
	return t.dryRun || (t.botToken != "" && t.chatID != "")
}

// Digest is the new jobs of one feed (profile).
type Digest struct {
	Label string
	Jobs  []*models.Job
}

// RunStats goes in the footer of the run's last message.
type RunStats struct {
	Duration time.Duration
	Scraped  int
	Sources  []string
	NextRun  time.Time // zero = unknown
}

// SendDigests sends every feed's new jobs as one combined message — a
// section per feed, stats in the footer. It only splits into "part 1/2"
// messages when Telegram's length limit forces it. Nothing is sent when
// there are no new jobs.
func (t *TelegramNotifier) SendDigests(digests []Digest, stats RunStats) error {
	if !t.IsConfigured() {
		t.logger.Warn("Telegram not configured — skipping notifications")
		return nil
	}
	now := time.Now()
	messages := buildMessages(digests, stats, now)
	if len(messages) == 0 {
		return nil
	}

	var firstErr error
	for i, msg := range messages {
		if i > 0 {
			time.Sleep(time.Second) // stay under Telegram's rate limit
		}
		if err := t.sendMessage(msg); err != nil {
			t.logger.Error("Failed to send Telegram message", zap.Int("message", i+1), zap.Error(err))
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	t.logger.Info("Sent Telegram digest", zap.Int("feeds", len(digests)), zap.Int("messages", len(messages)))
	return firstErr
}

// SendAlert sends one warning with a bullet per line.
func (t *TelegramNotifier) SendAlert(title string, lines []string) error {
	if !t.IsConfigured() {
		return nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "⚠️ <b>%s</b>\n", esc(title))
	for _, l := range lines {
		fmt.Fprintf(&sb, "• %s\n", esc(truncate(l, 300)))
	}
	return t.sendMessage(sb.String())
}

// sendMessage sends a single HTML-formatted message to the configured chat
func (t *TelegramNotifier) sendMessage(text string) error {
	if t.dryRun {
		fmt.Printf("\n──── Telegram message (dry run) ────\n%s\n", text)
		return nil
	}
	apiURL := fmt.Sprintf("%s%s/sendMessage", telegramAPIBase, t.botToken)

	payload := map[string]any{
		"chat_id":    t.chatID,
		"text":       text,
		"parse_mode": "HTML",
		// No link previews: a digest has many links and previews bury them
		"link_preview_options": map[string]any{"is_disabled": true},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal telegram payload: %w", err)
	}

	// Telegram limits groups to ~20 messages/minute; on 429 it says how long
	// to wait. Honour that a few times before giving up.
	for range 4 {
		var resp *http.Response
		retryErr := utils.Retry(3, func() error {
			var reqErr error
			resp, reqErr = t.client.Post(apiURL, "application/json", bytes.NewReader(body))
			return reqErr
		})
		if retryErr != nil {
			return fmt.Errorf("telegram API request failed: %w", retryErr)
		}

		var result struct {
			Description string `json:"description"`
			Parameters  struct {
				RetryAfter int `json:"retry_after"`
			} `json:"parameters"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		switch {
		case resp.StatusCode == http.StatusOK:
			return nil
		case resp.StatusCode == http.StatusTooManyRequests && result.Parameters.RetryAfter > 0:
			wait := time.Duration(result.Parameters.RetryAfter+1) * time.Second
			t.logger.Warn("Telegram rate limit hit, waiting", zap.Duration("wait", wait))
			time.Sleep(wait)
		default:
			return fmt.Errorf("telegram API returned status %d: %s", resp.StatusCode, result.Description)
		}
	}
	return fmt.Errorf("telegram API still rate limiting after retries")
}

// maxMessageLen stays safely under Telegram's 4096-character limit (which
// counts visible text; we count the raw HTML, so this over-estimates).
const maxMessageLen = 3800

// titleReserve is room kept for the title line added once the number of
// parts is known.
const titleReserve = 80

// buildMessages renders all digests into as few messages as possible:
//
//	🔔 3 new jobs · 2 feeds
//
//	🚀 Dev Jobs – India · 2
//	1. …
//	2. …
//
//	💼 Software Engineer – Mumbai · 1
//	1. …
//
//	⚡ 41s · 1277 scraped · … · next run ~38m
//
// A feed that spills over into the next message repeats its header with
// "(cont.)"; numbering continues across parts.
func buildMessages(digests []Digest, stats RunStats, now time.Time) []string {
	total, feeds := 0, 0
	for _, d := range digests {
		if len(d.Jobs) > 0 {
			total += len(d.Jobs)
			feeds++
		}
	}
	if total == 0 {
		return nil
	}
	footer := formatFooter(stats, now)
	limit := maxMessageLen - titleReserve - utf8.RuneCountInString(footer)

	var bodies []string
	var cur strings.Builder
	curLen := 0
	flush := func() {
		bodies = append(bodies, strings.TrimRight(cur.String(), "\n"))
		cur.Reset()
		curLen = 0
	}
	write := func(s string) {
		cur.WriteString(s)
		curLen += utf8.RuneCountInString(s)
	}

	for _, d := range digests {
		if len(d.Jobs) == 0 {
			continue
		}
		headerWritten := false
		for i, job := range sortJobs(d.Jobs) {
			entry := formatJob(i+1, job, now) + "\n"
			header := formatFeedHeader(d.Label, len(d.Jobs), i > 0)
			need := utf8.RuneCountInString(entry)
			if !headerWritten {
				need += utf8.RuneCountInString(header)
			}
			if curLen > 0 && curLen+need > limit {
				flush()
				headerWritten = false
				header = formatFeedHeader(d.Label, len(d.Jobs), i > 0)
			}
			if !headerWritten {
				write(header)
				headerWritten = true
			}
			write(entry)
		}
	}
	flush()

	messages := make([]string, len(bodies))
	for i, body := range bodies {
		msg := formatTitle(total, feeds, i+1, len(bodies)) + body
		if i == len(bodies)-1 {
			msg += "\n\n" + footer
		}
		messages[i] = msg
	}
	return messages
}

// formatTitle is the first line of each message.
func formatTitle(total, feeds, part, parts int) string {
	noun := "new jobs"
	if total == 1 {
		noun = "new job"
	}
	t := fmt.Sprintf("🔔 <b>%d %s</b>", total, noun)
	if feeds > 1 {
		t += fmt.Sprintf(" · %d feeds", feeds)
	}
	if parts > 1 {
		t += fmt.Sprintf(" · part %d/%d", part, parts)
	}
	return t + "\n\n"
}

// formatFeedHeader starts a feed's section ("(cont.)" when it continues
// from the previous message).
func formatFeedHeader(label string, count int, cont bool) string {
	if cont {
		return fmt.Sprintf("<b>%s</b> (cont.)\n\n", esc(label))
	}
	return fmt.Sprintf("<b>%s</b> · %d\n\n", esc(label), count)
}

// formatJob renders one numbered job card.
//
//  1. Senior Developer            (title links to the posting)
//     🏢 ICE · 📍 Hyderabad, India
//     🕐 2h ago · LinkedIn · #Golang #AWS
func formatJob(n int, job *models.Job, now time.Time) string {
	title := truncate(utils.CleanText(job.Title), 140)
	company := truncate(utils.CleanText(job.Company), 60)
	location := truncate(cleanLocation(job.Location), 80)
	if strings.EqualFold(location, title) {
		location = ""
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "<b>%d.</b> <a href=\"%s\"><b>%s</b></a>\n", n, esc(job.Link), esc(title))

	var where []string
	if company != "" {
		where = append(where, "🏢 "+esc(company))
	}
	if location != "" {
		where = append(where, "📍 "+esc(location))
	}
	if len(where) > 0 {
		sb.WriteString(strings.Join(where, " · ") + "\n")
	}

	var meta []string
	if age := humanizeAge(job, now); age != "" {
		meta = append(meta, "🕐 "+esc(age))
	}
	meta = append(meta, sourceLabel(job.Source))
	if len(job.Tags) > 0 {
		tags := make([]string, len(job.Tags))
		for i, tag := range job.Tags {
			tags[i] = "#" + hashtag(tag)
		}
		meta = append(meta, strings.Join(tags, " "))
	}
	sb.WriteString(strings.Join(meta, " · ") + "\n")
	return sb.String()
}

// formatFooter is the small stats line closing the run's last message.
func formatFooter(s RunStats, now time.Time) string {
	parts := []string{"⚡ " + s.Duration.Round(time.Second).String(), fmt.Sprintf("%d scraped", s.Scraped)}
	if len(s.Sources) > 0 {
		parts = append(parts, strings.Join(s.Sources, ", "))
	}
	if !s.NextRun.IsZero() {
		parts = append(parts, "next run "+untilText(s.NextRun.Sub(now)))
	}
	return "<i>" + esc(strings.Join(parts, " · ")) + "</i>"
}

// untilText renders a wait as "~45m" or "~2h".
func untilText(d time.Duration) string {
	if d < 55*time.Minute {
		return fmt.Sprintf("~%dm", max(int(d.Round(time.Minute).Minutes()), 1))
	}
	return fmt.Sprintf("~%dh", int(d.Round(time.Hour).Hours()))
}

// sortJobs orders jobs by relevance, then newest first (stable).
func sortJobs(jobs []*models.Job) []*models.Job {
	out := slices.Clone(jobs)
	slices.SortStableFunc(out, func(a, b *models.Job) int {
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		return b.PostedTime.Compare(a.PostedTime)
	})
	return out
}

// locationNoise is text LinkedIn & co. append to the location field.
var locationNoise = regexp.MustCompile(`(?i)\b(actively hiring|just now|be an early applicant|promoted|reposted|easy apply|\d+\s+(second|minute|hour|day|week|month)s?\s+ago)\b`)

// cleanLocation strips badge text and shortens "City, State, Country" to
// "City, Country". Multi-location values ("A; B") are only cleaned.
func cleanLocation(loc string) string {
	loc = utils.CleanText(locationNoise.ReplaceAllString(loc, " "))
	loc = strings.Trim(loc, " ,·-|")
	if strings.Contains(loc, ";") {
		return loc
	}
	parts := strings.Split(loc, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	if len(parts) >= 3 {
		return parts[0] + ", " + parts[len(parts)-1]
	}
	return strings.Join(parts, ", ")
}

// humanizeAge turns the posting time into "just now", "3h ago", "today",
// "yesterday" or "4d ago". Unparseable site text is passed through tidied.
func humanizeAge(job *models.Job, now time.Time) string {
	if !job.PostedTime.IsZero() {
		return relative(now.Sub(job.PostedTime))
	}
	raw := utils.CleanText(job.PostedAt)
	if raw == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return relative(now.Sub(t))
	}
	if d, err := time.Parse("2006-01-02", raw); err == nil {
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		days := int(today.Sub(d).Hours() / 24)
		switch {
		case days <= 0:
			return "today"
		case days == 1:
			return "yesterday"
		default:
			return fmt.Sprintf("%dd ago", days)
		}
	}
	lower := strings.ToLower(raw)
	for _, prefix := range []string{"posted ", "employer ", "active "} {
		lower = strings.TrimPrefix(lower, prefix)
	}
	return lower
}

func relative(d time.Duration) string {
	switch {
	case d < time.Hour:
		return "just now"
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// hashtag makes a tag usable as a Telegram hashtag ("Node.js" → "NodeJS").
func hashtag(tag string) string {
	tag = strings.ReplaceAll(tag, ".js", "JS")
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			return r
		}
		return -1
	}, tag)
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n-1])) + "…"
}

// esc escapes text for Telegram's HTML parse mode.
func esc(s string) string { return html.EscapeString(s) }

// sourceLabel returns a human-readable label for a source
func sourceLabel(source string) string {
	switch source {
	case models.SourceLinkedIn:
		return "LinkedIn"
	case models.SourceIndeed:
		return "Indeed"
	case models.SourceNaukri:
		return "Naukri"
	case models.SourceRemoteOK:
		return "Remote OK"
	case models.SourceRemotive:
		return "Remotive"
	case models.SourceArbeitnow:
		return "Arbeitnow"
	case models.SourceHN:
		return "HN Who's Hiring"
	case models.SourceCareers:
		return "Career page"
	default:
		return source
	}
}
