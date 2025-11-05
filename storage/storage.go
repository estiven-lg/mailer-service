package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
			to_addr   TEXT NOT NULL,
			subject   TEXT NOT NULL,
			body      TEXT NOT NULL,
			status    TEXT NOT NULL,
			error     TEXT,
			created_at TIMESTAMPTZ NOT NULL,
			sent_at    TIMESTAMPTZ
		);`,
		`CREATE TABLE IF NOT EXISTS templates (
			id BIGSERIAL PRIMARY KEY,
			tpl_key    TEXT NOT NULL UNIQUE,
			name       TEXT NOT NULL,
			description TEXT,
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS template_versions (
			id BIGSERIAL PRIMARY KEY,
			template_id BIGINT NOT NULL REFERENCES templates(id),
			version  INT   NOT NULL,
			locale   TEXT  NOT NULL DEFAULT 'es-GT',
			subject  TEXT  NOT NULL,
			body_html TEXT,
			body_text TEXT,
			is_active BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMPTZ NOT NULL,
			UNIQUE(template_id, version, locale)
		);`,
		// Seed plantilla "bienvenida"
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

// === Envío normal (queued -> sent/failed) ===
func (s *Store) InsertQueued(ctx context.Context, to, subject, body string) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx,
		`INSERT INTO emails (to_addr, subject, body, status, created_at)
		 VALUES ($1, $2, $3, 'queued', NOW())
		 RETURNING id`,
		to, subject, body,
	).Scan(&id)
	if err != nil {
		return 0, err
	}
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

// === Borradores (draft) ===
func (s *Store) InsertDraft(ctx context.Context, to, subject, body string) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx,
		`INSERT INTO emails (to_addr, subject, body, status, created_at)
		 VALUES ($1,$2,$3,'draft', NOW())
		 RETURNING id`, to, subject, body).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// Actualiza solo si sigue en estado 'draft'
func (s *Store) UpdateDraft(ctx context.Context, id int64, to, subject, body string) (bool, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE emails
		    SET to_addr=$1, subject=$2, body=$3
		  WHERE id=$4 AND status='draft'`,
		to, subject, body, id)
	if err != nil {
		return false, err
	}
	aff, _ := res.RowsAffected()
	return aff == 1, nil
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

// =========================
// CRUD adicional (Emails y Templates)
// =========================

type EmailFilter struct {
	Status        string
	Limit, Offset int
}

func (s *Store) ListEmails(ctx context.Context, f EmailFilter) ([]Email, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}

	args := []any{}
	q := `SELECT id, to_addr, subject, body, status, error, created_at, sent_at FROM emails`
	if f.Status != "" {
		q += " WHERE status = $1"
		args = append(args, f.Status)
	}
	q += " ORDER BY created_at DESC"

	limitPos := len(args) + 1
	offsetPos := len(args) + 2
	q += fmt.Sprintf(" LIMIT $%d OFFSET $%d", limitPos, offsetPos)
	args = append(args, f.Limit, f.Offset)

	rows, err := s.DB.QueryContext(ctx, q, args...)
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
	return out, rows.Err()
}

func (s *Store) GetEmailByID(ctx context.Context, id int64) (*Email, error) {
	const q = `SELECT id, to_addr, subject, body, status, error, created_at, sent_at
	           FROM emails WHERE id=$1`
	var e Email
	if err := s.DB.QueryRowContext(ctx, q, id).
		Scan(&e.ID, &e.To, &e.Subject, &e.Body, &e.Status, &e.Error, &e.CreatedAt, &e.SentAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &e, nil
}

func (s *Store) DeleteEmail(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM emails WHERE id=$1`, id)
	return err
}

// -------- Templates --------

type Template struct {
	ID          int64
	Key         string
	Name        string
	Description sql.NullString
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (s *Store) ListTemplates(ctx context.Context) ([]Template, error) {
	const q = `SELECT id, tpl_key, name, description, created_at, updated_at
	           FROM templates ORDER BY tpl_key`
	rows, err := s.DB.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Template
	for rows.Next() {
		var t Template
		if err := rows.Scan(&t.ID, &t.Key, &t.Name, &t.Description, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type TemplateVersionDTO struct {
	TemplateID int64
	Version    int
	Locale     string
	Subject    string
	BodyHTML   sql.NullString
	BodyText   sql.NullString
	IsActive   bool
	CreatedAt  time.Time
}

func (s *Store) ListTemplateVersions(ctx context.Context, tplKey string) ([]TemplateVersionDTO, error) {
	const q = `SELECT v.template_id, v.version, v.locale, v.subject, v.body_html, v.body_text, v.is_active, v.created_at
	             FROM template_versions v
	             JOIN templates t ON t.id = v.template_id
	            WHERE t.tpl_key = $1
	         ORDER BY v.created_at DESC, v.version DESC`
	rows, err := s.DB.QueryContext(ctx, q, tplKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TemplateVersionDTO
	for rows.Next() {
		var tv TemplateVersionDTO
		if err := rows.Scan(&tv.TemplateID, &tv.Version, &tv.Locale, &tv.Subject, &tv.BodyHTML, &tv.BodyText, &tv.IsActive, &tv.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, tv)
	}
	return out, rows.Err()
}

func (s *Store) EnsureTemplate(ctx context.Context, tplKey, name, desc string) (int64, error) {
	const q = `INSERT INTO templates (tpl_key, name, description, created_at, updated_at)
	           VALUES ($1,$2,$3,NOW(),NOW())
	           ON CONFLICT (tpl_key) DO UPDATE SET updated_at=EXCLUDED.updated_at
	           RETURNING id`
	var id int64
	if err := s.DB.QueryRowContext(ctx, q, tplKey, name, desc).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) CreateTemplateVersion(ctx context.Context, tplKey, locale, subject string, bodyHTML, bodyText sql.NullString, isActive bool) error {
	var tplID int64
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM templates WHERE tpl_key=$1`, tplKey).Scan(&tplID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			var err2 error
			tplID, err2 = s.EnsureTemplate(ctx, tplKey, tplKey, "")
			if err2 != nil {
				return err2
			}
		} else {
			return err
		}
	}

	var nextVer int
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM template_versions WHERE template_id=$1`, tplID).Scan(&nextVer); err != nil {
		return err
	}
	if nextVer == 0 {
		nextVer = 1
	}

	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO template_versions (template_id, version, locale, subject, body_html, body_text, is_active, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,NOW())`,
		tplID, nextVer, locale, subject, bodyHTML, bodyText, isActive,
	)
	return err
}

func (s *Store) ActivateTemplateVersion(ctx context.Context, tplKey string, version int, locale string) error {
	const q1 = `UPDATE template_versions v SET is_active=FALSE
	              FROM templates t
	             WHERE v.template_id=t.id AND t.tpl_key=$1 AND v.locale=$2`
	if _, err := s.DB.ExecContext(ctx, q1, tplKey, locale); err != nil {
		return err
	}

	const q2 = `UPDATE template_versions v SET is_active=TRUE
	              FROM templates t
	             WHERE v.template_id=t.id AND t.tpl_key=$1 AND v.locale=$2 AND v.version=$3`
	_, err := s.DB.ExecContext(ctx, q2, tplKey, locale, version)
	return err
}
