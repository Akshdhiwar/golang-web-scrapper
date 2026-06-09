// db/postgres.go
package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
	"go.uber.org/zap"

	"github.com/yourname/job-scraper/models"
)

// DB wraps the sql.DB connection with helper methods
type DB struct {
	conn   *sql.DB
	logger *zap.Logger
}

// New creates a new DB connection with retries
func New(dsn string, logger *zap.Logger) (*DB, error) {
	var conn *sql.DB
	var err error

	// Retry connection up to 5 times (useful for Docker startup)
	for i := 0; i < 5; i++ {
		conn, err = sql.Open("postgres", dsn)
		if err == nil {
			if pingErr := conn.Ping(); pingErr == nil {
				break
			}
		}
		logger.Warn("Database connection failed, retrying...",
			zap.Int("attempt", i+1),
			zap.Error(err),
		)
		time.Sleep(2 * time.Second)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	conn.SetMaxOpenConns(10)
	conn.SetMaxIdleConns(5)
	conn.SetConnMaxLifetime(30 * time.Minute)

	return &DB{conn: conn, logger: logger}, nil
}

// Migrate creates tables if they don't exist
func (d *DB) Migrate() error {
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
	`
	_, err := d.conn.Exec(query)
	if err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}
	d.logger.Info("Database migration completed")
	return nil
}

// InsertJob inserts a job, returns (true, nil) if newly inserted, (false, nil) if duplicate
func (d *DB) InsertJob(job *models.Job) (bool, error) {
	query := `
	INSERT INTO jobs (title, company, location, link, source, posted_at)
	VALUES ($1, $2, $3, $4, $5, $6)
	ON CONFLICT (link) DO NOTHING
	RETURNING id
	`
	var id int
	err := d.conn.QueryRow(query,
		job.Title,
		job.Company,
		job.Location,
		job.Link,
		job.Source,
		job.PostedAt,
	).Scan(&id)

	if err == sql.ErrNoRows {
		// Duplicate — link already exists
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("insert job failed: %w", err)
	}

	job.ID = id
	return true, nil
}

// JobExists checks if a job link is already stored
func (d *DB) JobExists(link string) (bool, error) {
	var count int
	err := d.conn.QueryRow(`SELECT COUNT(1) FROM jobs WHERE link = $1`, link).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetRecentJobs returns jobs created in the last N hours
func (d *DB) GetRecentJobs(hours int) ([]*models.Job, error) {
	query := `
	SELECT id, title, company, location, link, source, posted_at, created_at
	FROM jobs
	WHERE created_at >= NOW() - INTERVAL '1 hour' * $1
	ORDER BY created_at DESC
	`
	rows, err := d.conn.Query(query, hours)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*models.Job
	for rows.Next() {
		j := &models.Job{}
		if err := rows.Scan(&j.ID, &j.Title, &j.Company, &j.Location,
			&j.Link, &j.Source, &j.PostedAt, &j.CreatedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// Close closes the database connection
func (d *DB) Close() error {
	return d.conn.Close()
}
