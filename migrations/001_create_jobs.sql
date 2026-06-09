-- migrations/001_create_jobs.sql
-- Run this manually or let the app auto-migrate via db.Migrate()

CREATE TABLE IF NOT EXISTS jobs (
    id         SERIAL PRIMARY KEY,
    title      TEXT        NOT NULL,
    company    TEXT        NOT NULL,
    location   TEXT,
    link       TEXT        UNIQUE NOT NULL,
    source     TEXT        NOT NULL,
    posted_at  TEXT,
    created_at TIMESTAMP   DEFAULT NOW()
);

-- Performance indexes
CREATE INDEX IF NOT EXISTS idx_jobs_source     ON jobs(source);
CREATE INDEX IF NOT EXISTS idx_jobs_created_at ON jobs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_jobs_link       ON jobs(link);

-- Optional: full-text search on title
CREATE INDEX IF NOT EXISTS idx_jobs_title_fts
    ON jobs USING gin(to_tsvector('english', title));

COMMENT ON TABLE  jobs           IS 'Scraped job postings from LinkedIn, Indeed, and Naukri';
COMMENT ON COLUMN jobs.link      IS 'Canonical job URL — UNIQUE constraint enforces deduplication';
COMMENT ON COLUMN jobs.source    IS 'Scraper source: linkedin | indeed | naukri';
COMMENT ON COLUMN jobs.posted_at IS 'Human-readable posted time as reported by the source (e.g. "2 hours ago")';
