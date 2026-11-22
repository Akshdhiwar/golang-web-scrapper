# 🕵️ Job Scraper — Go Job Aggregator with Telegram Alerts

Runs every hour and sends new job postings to a Telegram chat, grouped into
**feeds**. Each feed has its own label and its own "already sent" list:

| Feed | What it finds | Sources |
|------|---------------|---------|
| 🚀 **Dev Jobs – India** | Frontend / Backend / Fullstack developer roles in Mumbai, Bangalore, Hyderabad, Delhi, Pune | LinkedIn, Indeed |
| 🐹 **Golang – Worldwide** | Go / Golang roles anywhere, on-site or remote | LinkedIn (Worldwide), Remote OK, Remotive, Arbeitnow, HN "Who is hiring?", company career pages |
| 💼 **Software Engineer – Mumbai** | Any software engineering role in Mumbai, Navi Mumbai, Thane, or remote-India | LinkedIn, Indeed, Remote OK, Remotive, company career pages |
| 🎨 **Frontend – Preferred Cities** | Frontend-only roles (React, Angular, Vue…) in the 5 preferred cities | LinkedIn, Indeed, company career pages |

> **Naukri** is supported in code but currently answers automated requests with
> "Access Denied" (bot protection), so it is not enabled in any feed.

Feeds live in [`profiles.yaml`](profiles.yaml) and career pages in
[`companies.yaml`](companies.yaml) — add or change either without touching Go code.

---

## 📐 Architecture Overview

```
                    GitHub Actions cron (hourly)  ─or─  built-in scheduler
                                      │
                        profiles.yaml (feeds) + companies.yaml
                                      │
          ┌───────────────────────────┴────────────────────────────┐
          ▼                                                        ▼
  SEARCH SOURCES (per keyword × location)               FEED SOURCES (once per run)
  LinkedIn · Indeed (shared headless Chromium)          Remote OK · Remotive · Arbeitnow
  Naukri (Colly)                                        HN Who's Hiring · Career pages
  identical queries across feeds run once;              (Greenhouse / Lever / Ashby APIs)
  worker pool of MAX_CONCURRENCY
          └───────────────────────────┬────────────────────────────┘
                                      ▼
                      ROUTE results to the feeds that asked for them
                                      ▼
                 FILTER per feed: title / location regexes + freshness
                                      ▼
            DEDUP: jobs UNIQUE(link) + profile_jobs (feed, job) → new per feed
                                      ▼
            TELEGRAM: one labelled message batch per feed + run summary
```

---

## 🗂️ Project Structure

```
├── cmd/main.go               # Entry point — wires everything together
├── profiles.yaml             # Job feeds: keywords, sources, filters
├── companies.yaml            # Career pages (Greenhouse / Lever / Ashby boards)
├── config/
│   ├── config.go             # Env-based settings
│   └── profiles.go           # Loads + validates profiles.yaml / companies.yaml
├── filter/filter.go          # Per-feed title / location / freshness filters
├── scrapers/
│   ├── scraper.go            # Scraper (search) and Feed (fetch-once) interfaces
│   ├── browser.go            # One shared headless Chromium for all browser scrapers
│   ├── browser_site.go       # Generic paginated browser scraping
│   ├── linkedin.go           # LinkedIn public job search (Playwright)
│   ├── indeed.go             # Indeed India (Playwright)
│   ├── naukri.go             # Naukri.com (Colly, JSON + HTML fallback)
│   └── feeds/                # Remote OK, Remotive, Arbeitnow, HN, career pages
├── scheduler/scheduler.go    # Pipeline: tasks → worker pool → filter → dedup → notify
├── db/postgres.go            # PostgreSQL connection, migrations, upsert, per-feed dedup
├── notifier/telegram.go      # Telegram messages (labelled, size-aware batching)
├── models/job.go             # Job struct + tag scoring
└── utils/helpers.go          # User agents, delays, retry, logger
```

---

## ⚡ Quick Start

### Option A — Run locally (no Docker, no database) — simplest

**Prerequisites:** Go 1.22+. That's it — Chromium is downloaded automatically
the first time a browser source (Indeed, or the LinkedIn fallback) needs it.

```bash
cp .env.example .env      # set TELEGRAM_BOT_TOKEN / TELEGRAM_CHAT_ID (leave DATABASE_URL empty)
make dry                  # one run, messages printed to the console instead of sent
                          #   (read-only: saves nothing, so a later real run still sends them)
make run                  # one run, sends to Telegram
make watch                # keep running on CRON_SCHEDULE (default: hourly)
```

With no `DATABASE_URL`, "already sent" state lives in `.data/seen.json`
(`STORE_FILE`). The **first run on an empty store only seeds it** — every job
currently listed is recorded but nothing is sent, so you don't get a flood. From
the second run on you get only new jobs. To send the first run too:
`NOTIFY_FIRST_RUN=true make run`. Delete `.data/seen.json` to start over.

### Option B — Docker Compose

```bash
cp .env.example .env      # set the Telegram values
docker compose up -d
docker compose logs -f scraper
```

Set `DATABASE_URL` in `.env` to use your Postgres; leave it empty and the file
store is kept in the `scraper_data` volume.

### Option C — GitHub Actions

`.github/workflows/scraper.yml` runs hourly with `RUN_ONCE=true` and Postgres
from the `DATABASE_URL` secret (required there — the run refuses to start
without it, since the file store would not persist between runs).

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

-- Which jobs each feed has already sent (per-feed dedup)
CREATE TABLE profile_jobs (
    profile    TEXT NOT NULL,                -- feed id from profiles.yaml
    job_id     INT  NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    created_at TIMESTAMP DEFAULT NOW(),
    PRIMARY KEY (profile, job_id)
);
```

### Useful Queries

```sql
-- Jobs found in last hour
SELECT * FROM jobs WHERE created_at >= NOW() - INTERVAL '1 hour' ORDER BY created_at DESC;

-- Jobs sent per feed today
SELECT profile, COUNT(*) FROM profile_jobs
WHERE created_at::date = CURRENT_DATE GROUP BY profile;

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

### Environment variables

| Variable                  | Default                                 | Description |
|---------------------------|-----------------------------------------|-------------|
| `DATABASE_URL`            | *(empty)*                               | PostgreSQL connection string; empty = local file store |
| `STORE_FILE`              | `.data/seen.json`                       | File store used when `DATABASE_URL` is empty |
| `NOTIFY_FIRST_RUN`        | `false`                                 | Send jobs on the first run against an empty store (default: seed only) |
| `TELEGRAM_BOT_TOKEN`      | *(empty)*                               | Bot token from @BotFather |
| `TELEGRAM_CHAT_ID`        | *(empty)*                               | Chat/group/channel that receives every feed |
| `PROFILES_FILE`           | `profiles.yaml`                         | Feed definitions |
| `COMPANIES_FILE`          | `companies.yaml`                        | Career pages for the `careers` source |
| `MAX_CONCURRENCY`         | `4`                                     | Search tasks (browser pages) running at once |
| `MAX_PAGES`               | `3`                                     | Default pages per query (a feed's `max_pages` wins) |
| `MIN_DELAY_SECONDS`       | `1`                                     | Min delay between result pages |
| `MAX_DELAY_SECONDS`       | `3`                                     | Max delay between result pages |
| `MAX_RETRIES`             | `3`                                     | Retry attempts on navigation failure |
| `CRON_SCHEDULE`           | `0 * * * *`                             | 5-field cron for the built-in scheduler |
| `RUN_ONCE`                | `false`                                 | Run one pipeline and exit (used by GitHub Actions) |
| `DRY_RUN`                 | `false`                                 | Print Telegram messages instead of sending |
| `KEYWORDS` / `LOCATIONS`  | dev roles / 5 cities                    | Only used if `profiles.yaml` is missing |

### Adding or changing a feed (`profiles.yaml`)

```yaml
- id: golang_world                 # stable id — used for dedup in the DB
  label: "🐹 Golang – Worldwide"   # Telegram header
  keywords: [golang, go developer] # queries for linkedin / indeed / naukri
  locations: [Worldwide]
  sources: [linkedin, remoteok, remotive, arbeitnow, hn, careers]
  title_include: ['(?i)\bgo(lang)?\b']   # must match one (empty = all)
  title_exclude: ['(?i)go[- ]to[- ]market']
  location_include: []              # empty = anywhere
  posted_within_hours: 24           # date filter sent to LinkedIn / Indeed
  feed_max_age_hours: 72            # max age for API / career-page jobs
  max_pages: 3
```

Filters are Go regular expressions and apply to **every** source. Indeed and
Naukri only search India; use `linkedin` plus the API/career sources for
global feeds. Changing an `id` makes it a brand-new feed (it will re-send).

### Adding a company career page (`companies.yaml`)

Find the board slug in the company's careers URL —
`boards.greenhouse.io/<slug>`, `jobs.lever.co/<slug>` or `jobs.ashbyhq.com/<slug>` —
and add `{ name: Acme, ats: greenhouse, slug: acme }`. Many large Indian
companies use other ATS systems (Workday, Darwinbox…) without public APIs; their
jobs still arrive via LinkedIn.

---

## 📬 Notification Format

Each run sends **one combined message**: a section per feed that has new jobs,
each title linking to the posting, jobs ordered by relevance then recency, and
the run stats as a footer:

```
🔔 3 new jobs · 2 feeds

🚀 Dev Jobs – India · 2

1. Senior Developer                       ← title is the link
🏢 ICE · 📍 Hyderabad, India
🕐 2h ago · LinkedIn

2. Backend Engineer (Go)
🏢 Razorpay · 📍 Bengaluru, India
🕐 today · Career page · #Golang #AWS

💼 Software Engineer – Mumbai · 1

1. SDE II
🏢 Zeta · 📍 Mumbai, India
🕐 5h ago · LinkedIn

⚡ 41s · 1277 scraped · careers, linkedin · next run ~38m
```

- Runs with nothing new send **nothing**.
- The message only splits ("· part 1/2", feed headers marked "(cont.)") when
  it would exceed Telegram's 4096-character limit.
- Source failures arrive as a single "⚠️ Some sources had trouble" message.
- A job that fits two feeds (e.g. a React role in Mumbai) appears in both sections.

---

## 🔍 Scraping Strategy Details

| Source        | Method | Notes |
|---------------|--------|-------|
| LinkedIn      | HTTP (guest search endpoint), Playwright fallback | No browser needed normally; falls back to Chromium on 429 / errors. `f_TPR` = feed's `posted_within_hours`; `location=Worldwide` works |
| Indeed India  | Playwright | `fromage` = days; often blocked (failures don't alert) |
| Naukri        | Colly | `__NEXT_DATA__` JSON with HTML fallback; `jobAge` = days. **Currently blocked** ("Access Denied"), not used by any feed |
| Remote OK     | JSON API | Latest ~100 remote jobs |
| Remotive      | JSON API | Software-dev category; fetched every 6 h (UTC 0/6/12/18) per their usage guidance |
| Arbeitnow     | JSON API | EU + remote jobs, newest first, up to 5 pages |
| HN Who's Hiring | Algolia API | Top-level comments of the latest monthly thread; first line = title |
| Career pages  | Greenhouse / Lever / Ashby APIs | Every company in `companies.yaml`; posting date filtered |

All browser scrapers share one headless Chromium (a fresh context per task,
images/fonts/CSS blocked) that is only installed and launched when first
needed. Identical queries requested by several feeds are scraped once and shared.

**Early paging stop:** every scraped link is remembered for 14 days
(`scraped_links` table / file store). Results are sorted newest first, so once
≥80% of a page was already seen in an earlier run, the remaining pages are
skipped — on hourly runs most queries need just one page.

---

## 🛡️ Error Handling

| Scenario                    | Behavior |
|-----------------------------|----------|
| A source fails              | Logged; one Telegram alert per source per run (not per task); Indeed alerts are muted |
| One career page fails       | Logged and skipped; the source only fails if every company fails |
| Duplicate job               | Stored once (`UNIQUE(link)`); notified once per feed (`profile_jobs`) |
| Network timeout             | Retried up to `MAX_RETRIES` times with exponential backoff |
| Pipeline already running    | New tick skipped (and GitHub Actions runs never overlap) |
| Telegram rate limit (429)   | Waits `retry_after` and retries |
| Telegram API down           | Logged; jobs still saved to DB |
| Browser crash               | Relaunched on the next task |

---

## 🧪 Running Tests

```bash
# Unit tests (filters, feed parsers, Telegram batching)
go test ./...

# Full run without sending anything to Telegram
RUN_ONCE=true DRY_RUN=true go run ./cmd/main.go

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
- Uses official public JSON APIs where they exist (Remote OK, Remotive, Arbeitnow, HN, Greenhouse, Lever, Ashby) and follows their usage notes (Remote OK links back to its listings; Remotive is polled 4× a day)
- Do **not** use this to bypass paywalls, authentication, or CAPTCHA systems
- Review each site's Terms of Service before using in a commercial context
