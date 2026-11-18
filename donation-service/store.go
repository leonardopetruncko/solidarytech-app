package main

import (
	"context"
	"database/sql"
	"time"

	"github.com/XSAM/otelsql"
	_ "github.com/jackc/pgx/v5/stdlib"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore abre o pool instrumentado: cada query vira um span no trace
// (aparece no Service Map do Datadog como dependência "postgresql").
func NewPostgresStore(ctx context.Context, url string) (*PostgresStore, error) {
	db, err := otelsql.Open("pgx", url, otelsql.WithAttributes(semconv.DBSystemPostgreSQL))
	if err != nil {
		return nil, err
	}
	// Rightsizing do pool: 2 réplicas x 10 conexões cabem com folga no db.t3.micro.
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	_, _ = otelsql.RegisterDBStatsMetrics(db, otelsql.WithAttributes(semconv.DBSystemPostgreSQL))

	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(c); err != nil {
		return nil, err
	}
	return &PostgresStore{db: db}, nil
}

func (s *PostgresStore) Insert(ctx context.Context, d *Donation) error {
	return s.db.QueryRowContext(ctx,
		"INSERT INTO donations (ngo_id, amount, donor_name, status) VALUES ($1, $2, $3, $4) RETURNING id, created_at",
		d.NgoID, d.Amount, d.DonorName, d.Status,
	).Scan(&d.ID, &d.CreatedAt)
}

func (s *PostgresStore) List(ctx context.Context, limit int) ([]Donation, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, ngo_id, amount, donor_name, status, created_at FROM donations ORDER BY id DESC LIMIT $1", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Donation{}
	for rows.Next() {
		var d Donation
		if err := rows.Scan(&d.ID, &d.NgoID, &d.Amount, &d.DonorName, &d.Status, &d.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, d)
	}
	return list, rows.Err()
}

func (s *PostgresStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *PostgresStore) Close() {
	_ = s.db.Close()
}
