package storage

import (
	"context"
	"database/sql"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type Store struct{ DB *sql.DB }

func Open(dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}

	s := &Store{DB: db}
	if err := s.migrate(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// ==========================================================
// MIGRACIÓN INICIAL
// ==========================================================
func (s *Store) migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS emails (
			id BIGSERIAL PRIMARY KEY,
			to_addr TEXT NOT NULL,
			subject TEXT NOT NULL,
			body TEXT NOT NULL,
			status TEXT NOT NULL,
			error TEXT,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			sent_at TIMESTAMPTZ
		);`,
		`CREATE TABLE IF NOT EXISTS templates (
			id BIGSERIAL PRIMARY KEY,
			name TEXT NOT NULL,
			subject TEXT NOT NULL,
			body TEXT NOT NULL,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW()
		);`,
	}
	for _, q := range stmts {
		if _, err := s.DB.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// ==========================================================
// EMAILS CRUD
// ==========================================================
type Email struct {
	ID        int64
	To        string
	Subject   string
	Body      string
	Status    string
	Error     sql.NullString
	CreatedAt time.Time
	SentAt    sql.NullTime
}

func (s *Store) InsertQueued(ctx context.Context, to, subject, body string) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx,
		`INSERT INTO emails (to_addr, subject, body, status)
		 VALUES ($1,$2,$3,'queued') RETURNING id`, to, subject, body).Scan(&id)
	return id, err
}

func (s *Store) MarkSent(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE emails SET status='sent', sent_at=NOW() WHERE id=$1`, id)
	return err
}

func (s *Store) MarkFailed(ctx context.Context, id int64, msg string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE emails SET status='failed', error=$1 WHERE id=$2`, msg, id)
	return err
}

func (s *Store) ListEmails(ctx context.Context) ([]Email, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, to_addr, subject, body, status, error, created_at, sent_at
		 FROM emails ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Email
	for rows.Next() {
		var e Email
		if err := rows.Scan(&e.ID, &e.To, &e.Subject, &e.Body, &e.Status, &e.Error, &e.CreatedAt, &e.SentAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func (s *Store) DeleteEmail(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM emails WHERE id=$1`, id)
	return err
}

// ==========================================================
// PLANTILLAS CRUD
// ==========================================================
type Template struct {
	ID        int64
	Name      string
	Subject   string
	Body      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (s *Store) ListTemplates(ctx context.Context) ([]Template, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, subject, body, created_at, updated_at FROM templates ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Template
	for rows.Next() {
		var t Template
		if err := rows.Scan(&t.ID, &t.Name, &t.Subject, &t.Body, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, nil
}

func (s *Store) InsertTemplate(ctx context.Context, name, subject, body string) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `
		INSERT INTO templates (name, subject, body, created_at, updated_at)
		VALUES ($1, $2, $3, now(), now())
		RETURNING id
	`, name, subject, body).Scan(&id)
	return id, err
}

func (s *Store) UpdateTemplate(ctx context.Context, id int64, name, subject, body string) error {
	_, err := s.DB.ExecContext(ctx, `
		UPDATE templates
		SET name=$1, subject=$2, body=$3, updated_at=now()
		WHERE id=$4
	`, name, subject, body, id)
	return err
}

func (s *Store) DeleteTemplate(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM templates WHERE id=$1`, id)
	return err
}
