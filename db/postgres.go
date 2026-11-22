// db/postgres.go
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lib/pq"
	"go.uber.org/zap"
)

// DB wraps the sql.DB connection with helper methods
type DB struct {
	conn     *sql.DB
	logger   *zap.Logger
	fresh    bool
	readOnly bool // dry run: MarkNew rolls back, RecordScraped is skipped
}

// New creates a new DB connection with retries
func New(dsn string, logger *zap.Logger) (*DB, error) {
	var conn *sql.DB
	var err error

	conn, err = sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid database URL: %w", err)
	}

	// Retry the connection up to 5 times (useful for Docker startup)
	for i := range 5 {
		if err = conn.Ping(); err == nil {
			break
		}
		logger.Warn("Database connection failed, retrying...",
			zap.Int("attempt", i+1),
			zap.Error(err),
		)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	conn.SetMaxOpenConns(10)
	conn.SetMaxIdleConns(5)
	conn.SetConnMaxLifetime(30 * time.Minute)

	return &DB{conn: conn, logger: logger}, nil
}

// Migrate creates tables if they don't exist. When the per-profile table is
// created for the first time, every job already stored is marked as sent for
// each profile in profileIDs, so upgrading doesn't re-send old jobs.
func (d *DB) Migrate(profileIDs []string) error {
	var hadProfileTable bool
	if err := d.conn.QueryRow(`SELECT to_regclass('public.profile_jobs') IS NOT NULL`).Scan(&hadProfileTable); err != nil {
		return fmt.Errorf("migration check failed: %w", err)
	}

	query := `
	CREATE TABLE IF NOT EXISTS jobs (
		id         SERIAL PRIMARY KEY,
		title      TEXT NOT NULL,
		company    TEXT NOT NULL,
		location   TEXT,
		link       TEXT UNIQUE NOT NULL,
		source     TEXT NOT NULL,
		posted_at  TEXT,
		created_at TIMESTAMP DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_jobs_source ON jobs(source);
	CREATE INDEX IF NOT EXISTS idx_jobs_created_at ON jobs(created_at);
	CREATE INDEX IF NOT EXISTS idx_jobs_link ON jobs(link);

	-- One row per (feed, job) that has been notified: per-feed deduplication
	CREATE TABLE IF NOT EXISTS profile_jobs (
		profile    TEXT NOT NULL,
		job_id     INT  NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
		created_at TIMESTAMP DEFAULT NOW(),
		PRIMARY KEY (profile, job_id)
	);

	-- Every link scraped in recent runs (matched or not): lets scrapers stop
	-- paging once they reach results an earlier run already saw.
	CREATE TABLE IF NOT EXISTS scraped_links (
		link      TEXT PRIMARY KEY,
		last_seen TIMESTAMP NOT NULL DEFAULT NOW()
	);
	`
	if _, err := d.conn.Exec(query); err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}

	if !hadProfileTable && len(profileIDs) > 0 {
		res, err := d.conn.Exec(`
			INSERT INTO profile_jobs (profile, job_id)
			SELECT p, j.id FROM jobs j CROSS JOIN unnest($1::text[]) AS p
			ON CONFLICT DO NOTHING`, pq.Array(profileIDs))
		if err != nil {
			return fmt.Errorf("profile backfill failed: %w", err)
		}
		n, _ := res.RowsAffected()
		d.logger.Info("Backfilled existing jobs into profiles", zap.Int64("rows", n))
	}

	if err := d.conn.QueryRow(`SELECT NOT EXISTS (SELECT 1 FROM jobs)`).Scan(&d.fresh); err != nil {
		return fmt.Errorf("migration check failed: %w", err)
	}
	d.logger.Info("Database migration completed")
	return nil
}

// MarkNew stores the matched jobs and records each (profile, job) pair in
// two bulk statements, returning the pairs that were not recorded before.
func (d *DB) MarkNew(ctx context.Context, marks []Mark) (map[Key]bool, error) {
	isNew := map[Key]bool{}
	if len(marks) == 0 {
		d.fresh = false
		return isNew, nil
	}

	// One row per distinct link: ON CONFLICT DO UPDATE may touch a row once.
	var titles, companies, locations, links, sources, posted []string
	byLink := map[string]bool{}
	for _, m := range marks {
		j := m.Job
		if j == nil || j.Link == "" || byLink[j.Link] {
			continue
		}
		byLink[j.Link] = true
		titles = append(titles, j.Title)
		companies = append(companies, j.Company)
		locations = append(locations, j.Location)
		links = append(links, j.Link)
		sources = append(sources, j.Source)
		posted = append(posted, j.PostedAt)
	}

	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// The no-op DO UPDATE makes RETURNING yield ids for existing rows too.
	rows, err := tx.QueryContext(ctx, `
		INSERT INTO jobs (title, company, location, link, source, posted_at)
		SELECT * FROM unnest($1::text[], $2::text[], $3::text[], $4::text[], $5::text[], $6::text[])
		ON CONFLICT (link) DO UPDATE SET link = EXCLUDED.link
		RETURNING id, link`,
		pq.Array(titles), pq.Array(companies), pq.Array(locations),
		pq.Array(links), pq.Array(sources), pq.Array(posted))
	if err != nil {
		return nil, fmt.Errorf("upsert jobs: %w", err)
	}
	idByLink := make(map[string]int, len(links))
	linkByID := make(map[int]string, len(links))
	for rows.Next() {
		var id int
		var link string
		if err := rows.Scan(&id, &link); err != nil {
			rows.Close()
			return nil, err
		}
		idByLink[link], linkByID[id] = id, link
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var profiles []string
	var jobIDs []int64
	for _, m := range marks {
		if m.Job == nil {
			continue
		}
		if id, ok := idByLink[m.Job.Link]; ok {
			m.Job.ID = id
			profiles = append(profiles, m.Profile)
			jobIDs = append(jobIDs, int64(id))
		}
	}
	rows, err = tx.QueryContext(ctx, `
		INSERT INTO profile_jobs (profile, job_id)
		SELECT * FROM unnest($1::text[], $2::int[])
		ON CONFLICT DO NOTHING
		RETURNING profile, job_id`, pq.Array(profiles), pq.Array(jobIDs))
	if err != nil {
		return nil, fmt.Errorf("mark profile jobs: %w", err)
	}
	for rows.Next() {
		var profile string
		var id int
		if err := rows.Scan(&profile, &id); err != nil {
			rows.Close()
			return nil, err
		}
		isNew[Key{profile, linkByID[id]}] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// In a dry run the deferred Rollback undoes both inserts; the RETURNING
	// rows above still told us which jobs would have been new.
	if !d.readOnly {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit: %w", err)
		}
	}
	d.fresh = false
	return isNew, nil
}

// SeenLinks reports which links were scraped in an earlier run.
func (d *DB) SeenLinks(ctx context.Context, links []string) (map[string]bool, error) {
	seen := map[string]bool{}
	if len(links) == 0 {
		return seen, nil
	}
	rows, err := d.conn.QueryContext(ctx,
		`SELECT link FROM scraped_links WHERE link = ANY($1)`, pq.Array(links))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		seen[l] = true
	}
	return seen, rows.Err()
}

// RecordScraped remembers this run's scraped links and prunes old ones.
func (d *DB) RecordScraped(ctx context.Context, links []string) error {
	if d.readOnly {
		return nil
	}
	links = uniqueStrings(links)
	if len(links) > 0 {
		if _, err := d.conn.ExecContext(ctx, `
			INSERT INTO scraped_links (link, last_seen)
			SELECT unnest($1::text[]), NOW()
			ON CONFLICT (link) DO UPDATE SET last_seen = NOW()`, pq.Array(links)); err != nil {
			return fmt.Errorf("record scraped links: %w", err)
		}
	}
	_, err := d.conn.ExecContext(ctx,
		`DELETE FROM scraped_links WHERE last_seen < NOW() - INTERVAL '14 days'`)
	return err
}

// Fresh is true when the jobs table was empty at startup (until MarkNew).
func (d *DB) Fresh() bool { return d.fresh }

// Close closes the database connection
func (d *DB) Close() error {
	return d.conn.Close()
}
