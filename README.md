# 🕵️ Job Scraper — Production-Ready Go Job Aggregator

A production-grade job scraping system that runs every hour and fetches the latest
**Frontend**, **Backend**, and **Fullstack** developer job postings from LinkedIn,
Indeed, and Naukri (India). New jobs are stored in PostgreSQL and pushed to a
Telegram channel — zero duplicates, zero spam.

---

## 📐 Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                        SCHEDULER (cron)                         │
│                     Fires every 1 hour                          │
└────────────────────────────┬────────────────────────────────────┘
                             │
          ┌──────────────────┼──────────────────┐
          ▼                  ▼                  ▼
   ┌─────────────┐  ┌─────────────┐  ┌─────────────┐
   │  LinkedIn   │  │   Indeed    │  │   Naukri    │
   │ (Playwright)│  │   (Colly)   │  │   (Colly)   │
   └──────┬──────┘  └──────┬──────┘  └──────┬──────┘
          └────────────────┼─────────────────┘
                           │  raw []Job
                           ▼
                 ┌─────────────────┐
                 │  PARSER LAYER   │
                 │ score + tag     │
                 └────────┬────────┘
                          │
                          ▼
                 ┌─────────────────┐
                 │  DEDUP LAYER    │◄── PostgreSQL UNIQUE(link)
                 │ INSERT OR SKIP  │
                 └────────┬────────┘
                          │ new jobs only
                          ▼
                 ┌─────────────────┐
                 │   NOTIFIER      │
                 │  Telegram Bot   │
                 └─────────────────┘
```

---

## 🗂️ Project Structure

```
job-scraper/
├── cmd/
│   └── main.go              # Entry point — wires everything together
├── config/
│   └── config.go            # Env-based config with sane defaults
├── scrapers/
│   ├── scraper.go           # Scraper interface definition
│   ├── indeed.go            # Indeed India (Colly, static HTML)
│   ├── naukri.go            # Naukri.com (Colly, JSON + HTML fallback)
│   └── linkedin.go          # LinkedIn Jobs (Playwright, headless Chrome)
├── models/
│   └── job.go               # Job struct + relevance scoring
├── db/
│   └── postgres.go          # PostgreSQL connection, migration, CRUD
├── notifier/
│   └── telegram.go          # Telegram Bot API notifications
├── scheduler/
│   └── scheduler.go         # Cron pipeline orchestrator
├── utils/
│   └── helpers.go           # User agents, delays, retry, logger
├── migrations/
│   └── 001_create_jobs.sql  # Raw SQL migration (auto-applied on boot)
├── .env.example             # Config template
├── docker-compose.yml       # Postgres + App via Docker
├── Dockerfile               # Multi-stage Go + Playwright build
└── go.mod
```

---

## ⚡ Quick Start

### Option A — Docker Compose (Recommended)

```bash
# 1. Clone and enter the project
git clone https://github.com/yourname/job-scraper.git
cd job-scraper

# 2. Copy and edit config
cp .env.example .env
# Edit .env — set TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID at minimum

# 3. Start everything
docker compose up -d

# 4. Watch logs
docker compose logs -f scraper
```

### Option B — Run Locally

**Prerequisites:**
- Go 1.21+
- PostgreSQL 14+
- Node.js 18+ (required by Playwright internals)

```bash
# 1. Install Go dependencies
go mod download

# 2. Install Playwright browser (one-time)
go run github.com/playwright-community/playwright-go/cmd/playwright install chromium

# 3. Set up environment
cp .env.example .env
# Edit .env with your DB credentials and Telegram tokens

# 4. Create the database
createdb jobscraper
# (the app runs migrations automatically on boot)

# 5. Run
go run ./cmd/main.go
```

---

## 🤖 Telegram Bot Setup (Step-by-Step)

### Step 1 — Create your bot
1. Open Telegram and search for **@BotFather**
2. Send `/newbot`
3. Choose a name (e.g. *My Job Alert Bot*)
4. Choose a username ending in `bot` (e.g. *myjobsalert_bot*)
5. BotFather replies with your **Bot Token** — looks like:
   ```
   123456789:ABCdefGHIjklMNOpqrSTUvwxYZ
   ```

### Step 2 — Get your Chat ID

**For personal notifications:**
1. Search for your new bot in Telegram and send `/start`
2. Visit this URL in your browser (replace TOKEN):
   ```
   https://api.telegram.org/bot<TOKEN>/getUpdates
   ```
3. Find `"chat":{"id": 123456789}` — that number is your Chat ID

**For a group/channel:**
1. Add your bot to the group/channel as Admin
2. Send any message in the group
3. Visit the same `getUpdates` URL — the `chat.id` will be negative (e.g. `-1001234567890`)

### Step 3 — Set in `.env`
```env
TELEGRAM_BOT_TOKEN=123456789:ABCdefGHIjklMNOpqrSTUvwxYZ
TELEGRAM_CHAT_ID=-1001234567890
```

### Step 4 — Test the connection
```bash
curl -X POST "https://api.telegram.org/bot<TOKEN>/sendMessage" \
  -H "Content-Type: application/json" \
  -d '{"chat_id": "<CHAT_ID>", "text": "Bot is working! 🎉"}'
```

---

## 🗄️ Database Schema

```sql
CREATE TABLE jobs (
    id         SERIAL PRIMARY KEY,
    title      TEXT        NOT NULL,
    company    TEXT        NOT NULL,
    location   TEXT,
    link       TEXT        UNIQUE NOT NULL,  -- deduplication key
    source     TEXT        NOT NULL,         -- linkedin | indeed | naukri
    posted_at  TEXT,
    created_at TIMESTAMP   DEFAULT NOW()
);
```

### Useful Queries

```sql
-- Jobs found in last hour
SELECT * FROM jobs WHERE created_at >= NOW() - INTERVAL '1 hour' ORDER BY created_at DESC;

-- Jobs per source today
SELECT source, COUNT(*) FROM jobs
WHERE created_at::date = CURRENT_DATE GROUP BY source;

-- Full-text search
SELECT * FROM jobs
WHERE to_tsvector('english', title) @@ plainto_tsquery('react golang');

-- Top companies hiring
SELECT company, COUNT(*) AS openings
FROM jobs GROUP BY company ORDER BY openings DESC LIMIT 20;
```

---

## ⚙️ Configuration Reference

| Variable                  | Default                                    | Description                            |
|---------------------------|--------------------------------------------|----------------------------------------|
| `DATABASE_URL`            | `postgres://...localhost.../jobscraper`    | PostgreSQL connection string           |
| `TELEGRAM_BOT_TOKEN`      | *(empty)*                                  | Your bot token from @BotFather         |
| `TELEGRAM_CHAT_ID`        | *(empty)*                                  | Target chat/group/channel ID           |
| `KEYWORDS`                | `frontend developer,backend developer,...` | Comma-separated search terms           |
| `LOCATION`                | `India`                                    | Target location filter                 |
| `MAX_PAGES`               | `3`                                        | Pages to scrape per keyword per source |
| `MIN_DELAY_SECONDS`       | `1`                                        | Min delay between requests             |
| `MAX_DELAY_SECONDS`       | `3`                                        | Max delay between requests             |
| `MAX_RETRIES`             | `3`                                        | Retry attempts on failure              |
| `CRON_SCHEDULE`           | `0 * * * *`                                | Cron expression (every hour)           |
| `NOTIFICATION_BATCH_SIZE` | `10`                                       | Jobs per Telegram message              |

---

## 📬 Notification Format

```
🚀 New Jobs Found! (Batch 1/2)
3 new listings
━━━━━━━━━━━━━━━━━━

*Senior Fullstack Developer*
🏢 Razorpay
📍 Bangalore, Karnataka, India
🌐 `LinkedIn`
🕐 2 hours ago
🏷 React · Node.js · TypeScript
🔗 Apply Here

*Backend Engineer – Go*
🏢 Zepto
📍 Mumbai, Maharashtra, India
🌐 `Naukri`
🕐 Posted today
🏷 Golang · PostgreSQL · Docker
🔗 Apply Here
...
```

---

## 🔍 Scraping Strategy Details

| Source    | Method       | Approach                                                    |
|-----------|--------------|-------------------------------------------------------------|
| LinkedIn  | Playwright   | Headless Chromium; JS-evaluated DOM; `f_TPR=r3600` filter   |
| Indeed    | Colly        | Static HTML scraping; `fromage=1` (last 24h) filter         |
| Naukri    | Colly        | `__NEXT_DATA__` JSON extraction; HTML fallback              |

**Incremental scraping** — each scraper stops as soon as it encounters a link already
in its in-memory seen-set for the current run. Combined with the DB `UNIQUE(link)`
constraint, this ensures no duplicate notifications.

---

## 🛡️ Error Handling

| Scenario                    | Behavior                                          |
|-----------------------------|---------------------------------------------------|
| Single scraper fails        | Logged + Telegram alert; other scrapers continue  |
| DB insert conflict          | Silently skipped (ON CONFLICT DO NOTHING)         |
| Network timeout             | Retried up to `MAX_RETRIES` times (exponential)   |
| Pipeline already running    | New tick skipped with a warning log               |
| Telegram API down           | Logged; jobs still saved to DB                    |
| Browser crash (Playwright)  | Error returned; scraper marked failed for run     |

---

## 🧪 Running Tests

```bash
# Unit tests
go test ./...

# With race detector
go test -race ./...

# With coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

---

## 🚀 Production Deployment Tips

1. **Use a secrets manager** (AWS Secrets Manager, Vault) instead of `.env` files
2. **Set `sslmode=require`** in `DATABASE_URL` for managed Postgres (RDS, Supabase)
3. **Tune `MAX_PAGES`** — start with 2 pages; increase only if needed
4. **Monitor with Prometheus** — expose `/metrics` if you add `prometheus/client_golang`
5. **Add a Liveness probe** — the cron ticker keeps the process alive; expose `GET /health`
6. **Persist Playwright cache** — mount `/root/.cache/ms-playwright` as a Docker volume

---

## 📌 Roadmap / Optional Enhancements

- [ ] HTTP `/api/jobs` endpoint with filters (source, keyword, date range)
- [ ] Prometheus metrics (jobs_scraped_total, scrape_duration_seconds)
- [ ] Email notifications via SendGrid as fallback
- [ ] Proxy rotation for IP-based rate limiting
- [ ] Redis cache for seen-links (faster than DB lookups at scale)
- [ ] Slack / Discord notifier adapters
- [ ] React dashboard to browse jobs

---

## ⚠️ Legal & Ethical Notes

- This scraper targets **public job listings** — the same pages any browser user sees
- Random delays (1–3s) and user-agent rotation are used to avoid hammering servers
- Respects `robots.txt` where Colly's default behavior applies
- Do **not** use this to bypass paywalls, authentication, or CAPTCHA systems
- Review each site's Terms of Service before using in a commercial context
