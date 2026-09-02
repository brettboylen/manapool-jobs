package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

type knownPosting struct {
	Slug   string
	Hiring bool
}

func postgresURL() string {
	if u := os.Getenv("DATABASE_URL"); u != "" {
		return u
	}
	host := envStr("POSTGRES_HOST", "postgres-rw.postgres.svc.cluster.local")
	port := envStr("POSTGRES_PORT", "5432")
	db := envStr("POSTGRES_DATABASE", "app")
	user := envStr("POSTGRES_USER", "postgres")
	pass := os.Getenv("POSTGRES_PASSWORD")
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s", user, pass, host, port, db)
}

func newStore(ctx context.Context, conn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(conn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 2
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "30s"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS mtg.manapool_job_postings (
			slug             TEXT PRIMARY KEY,
			url              TEXT NOT NULL,
			title            TEXT NOT NULL DEFAULT '',
			department       TEXT NOT NULL DEFAULT '',
			location         TEXT NOT NULL DEFAULT '',
			hiring           BOOLEAN NOT NULL DEFAULT false,
			compensation     TEXT NOT NULL DEFAULT '',
			how_to_apply     TEXT NOT NULL DEFAULT '',
			summary          TEXT NOT NULL DEFAULT '',
			content_hash     TEXT NOT NULL DEFAULT '',
			first_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
			last_seen_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
			last_notified_at TIMESTAMPTZ,
			raw              JSONB NOT NULL DEFAULT '{}'::jsonb
		)`)
	return err
}

func (s *Store) known(ctx context.Context) (map[string]knownPosting, error) {
	rows, err := s.pool.Query(ctx, `SELECT slug, hiring FROM mtg.manapool_job_postings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]knownPosting{}
	for rows.Next() {
		var k knownPosting
		if err := rows.Scan(&k.Slug, &k.Hiring); err != nil {
			return nil, err
		}
		out[k.Slug] = k
	}
	return out, rows.Err()
}

func (s *Store) upsert(ctx context.Context, listing Listing, parsed JobParse, notified bool) error {
	raw, _ := json.Marshal(parsed)
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	var notifiedAt any
	if notified {
		notifiedAt = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mtg.manapool_job_postings (
			slug, url, title, department, location, hiring,
			compensation, how_to_apply, summary, content_hash,
			last_seen_at, last_notified_at, raw
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10, now(), $11, $12::jsonb
		)
		ON CONFLICT (slug) DO UPDATE SET
			url = EXCLUDED.url,
			title = EXCLUDED.title,
			department = EXCLUDED.department,
			location = EXCLUDED.location,
			hiring = EXCLUDED.hiring,
			compensation = CASE WHEN EXCLUDED.compensation <> '' THEN EXCLUDED.compensation ELSE mtg.manapool_job_postings.compensation END,
			how_to_apply = CASE WHEN EXCLUDED.how_to_apply <> '' THEN EXCLUDED.how_to_apply ELSE mtg.manapool_job_postings.how_to_apply END,
			summary = CASE WHEN EXCLUDED.summary <> '' THEN EXCLUDED.summary ELSE mtg.manapool_job_postings.summary END,
			content_hash = EXCLUDED.content_hash,
			last_seen_at = now(),
			last_notified_at = COALESCE(EXCLUDED.last_notified_at, mtg.manapool_job_postings.last_notified_at),
			raw = CASE WHEN EXCLUDED.raw <> '{}'::jsonb THEN EXCLUDED.raw ELSE mtg.manapool_job_postings.raw END`,
		listing.Slug, listing.URL, listing.Title, listing.Department, listing.Location, listing.Hiring,
		parsed.Compensation, parsed.HowToApply, parsed.Summary, listing.Hash,
		notifiedAt, raw)
	return err
}

func (s *Store) touch(ctx context.Context, listing Listing) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE mtg.manapool_job_postings SET
			title = $2,
			department = $3,
			location = $4,
			hiring = $5,
			content_hash = $6,
			last_seen_at = now()
		WHERE slug = $1`,
		listing.Slug, listing.Title, listing.Department, listing.Location, listing.Hiring, listing.Hash)
	return err
}

