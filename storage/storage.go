package storage

import (
	"context"
	"database/sql"
	"errors"
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

func (s *Store) migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS emails (
			id BIGSERIAL PRIMARY KEY,
			to_addr TEXT NOT NULL,
			subject TEXT NOT NULL,
			body TEXT NOT NULL,
			status TEXT NOT NULL,
			error TEXT,
			created_at TIMESTAMPTZ NOT NULL,
			sent_at TIMESTAMPTZ
		);`,
		`CREATE TABLE IF NOT EXISTS templates (
			id BIGSERIAL PRIMARY KEY,
			tpl_key TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			description TEXT,
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS template_versions (
			id BIGSERIAL PRIMARY KEY,
			template_id BIGINT NOT NULL REFERENCES templates(id),
			version INT NOT NULL,
			locale TEXT NOT NULL DEFAULT 'es-GT',
			subject TEXT NOT NULL,
			body_html TEXT,
			body_text TEXT,
			is_active BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMPTZ NOT NULL,
			UNIQUE(template_id, version, locale)
		);`,
		// Seed plantilla "bienvenida" (si no existe)
		`INSERT INTO templates (tpl_key, name, description, created_at, updated_at)
		 SELECT 'bienvenida', 'Bienvenida', 'Plantilla de bienvenida por defecto', NOW(), NOW()
		 WHERE NOT EXISTS (SELECT 1 FROM templates WHERE tpl_key='bienvenida');`,
		`INSERT INTO template_versions (template_id, version, locale, subject, body_html, body_text, is_active, created_at)
		 SELECT t.id, 1, 'es-GT',
			 'Hola {{ .userName }}, ¡bienvenido!',
			 '<h1>¡Hola {{ .userName }}!</h1><p>Tu registro fue exitoso. Soporte: {{ .supportEmail }}</p>',
			 'Hola {{ .userName }}! Tu registro fue exitoso. Soporte: {{ .supportEmail }}',
			 TRUE,
			 NOW()
		 FROM templates t
		 WHERE t.tpl_key='bienvenida'
		   AND NOT EXISTS (
			 SELECT 1 FROM template_versions v
			 WHERE v.template_id = t.id AND v.version=1 AND v.locale='es-GT'
		   );`,
	}
	for _, q := range stmts {
		if _, err := s.DB.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

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
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO emails (to_addr, subject, body, status, created_at)
		 VALUES ($1, $2, $3, 'queued', NOW())`, to, subject, body)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

func (s *Store) MarkSent(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE emails SET status='sent', sent_at=NOW() WHERE id=$1`, id)
	return err
}

func (s *Store) MarkFailed(ctx context.Context, id int64, msg string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE emails SET status='failed', error=$1 WHERE id=$2`, msg, id)
	return err
}

type TemplateVersion struct {
	Subject  string
	BodyHTML sql.NullString
	BodyText sql.NullString
}

func (s *Store) GetActiveTemplateVersion(ctx context.Context, tplKey, locale string) (*TemplateVersion, error) {
	if locale == "" {
		locale = "es-GT"
	}
	const q = `
SELECT v.subject, v.body_html, v.body_text
FROM template_versions v
JOIN templates t ON t.id = v.template_id
WHERE t.tpl_key = $1 AND v.locale = $2 AND v.is_active = TRUE
LIMIT 1;
`
	row := s.DB.QueryRowContext(ctx, q, tplKey, locale)
	tv := &TemplateVersion{}
	if err := row.Scan(&tv.Subject, &tv.BodyHTML, &tv.BodyText); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			const q2 = `
SELECT v.subject, v.body_html, v.body_text
FROM template_versions v
JOIN templates t ON t.id = v.template_id
WHERE t.tpl_key = $1 AND v.is_active = TRUE
ORDER BY v.created_at DESC
LIMIT 1;`
			row2 := s.DB.QueryRowContext(ctx, q2, tplKey)
			if err2 := row2.Scan(&tv.Subject, &tv.BodyHTML, &tv.BodyText); err2 != nil {
				return nil, err
			}
			return tv, nil
		}
		return nil, err
	}
	return tv, nil
}
