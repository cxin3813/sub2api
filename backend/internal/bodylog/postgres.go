package bodylog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type PostgresIndex struct{ DB *sql.DB }

// The extension owns its schema and never rewrites the core migration ledger.
func (p *PostgresIndex) Init(ctx context.Context) error {
	_, err := p.DB.ExecContext(ctx, `
CREATE SCHEMA IF NOT EXISTS bodylog;
CREATE TABLE IF NOT EXISTS bodylog.config (
 id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id), value JSONB NOT NULL
);
CREATE TABLE IF NOT EXISTS bodylog.records (
 id UUID PRIMARY KEY, parent_id UUID, created_at TIMESTAMPTZ NOT NULL,
 ended_at TIMESTAMPTZ, user_id BIGINT, api_key_id BIGINT, status INTEGER NOT NULL,
 value JSONB NOT NULL
);
CREATE TABLE IF NOT EXISTS bodylog.task_links (
 kind TEXT NOT NULL, task_id TEXT NOT NULL, parent_id UUID NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), PRIMARY KEY(kind,task_id)
);
CREATE INDEX IF NOT EXISTS bodylog_records_created ON bodylog.records(created_at DESC, id);
CREATE INDEX IF NOT EXISTS bodylog_records_parent ON bodylog.records(parent_id);
CREATE INDEX IF NOT EXISTS bodylog_records_user ON bodylog.records(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS bodylog_records_key ON bodylog.records(api_key_id, created_at DESC);
CREATE INDEX IF NOT EXISTS bodylog_records_usage ON bodylog.records USING GIN ((value -> 'usage_log_ids'));
`)
	return err
}

func (p *PostgresIndex) LoadConfig(ctx context.Context) (Config, error) {
	c := DefaultConfig()
	var raw []byte
	err := p.DB.QueryRowContext(ctx, `SELECT value FROM bodylog.config WHERE id=TRUE`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		// Preserve the legacy switch, but do not inherit its BYTEA size ceiling.
		rows, legacyErr := p.DB.QueryContext(ctx, `SELECT key,value FROM settings WHERE key IN
('gateway_body_log_enabled','gateway_body_log_capture_request','gateway_body_log_capture_response')`)
		if legacyErr == nil {
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var key, value string
				if rows.Scan(&key, &value) == nil {
					switch key {
					case "gateway_body_log_enabled":
						c.Enabled = value == "true"
					case "gateway_body_log_capture_request":
						c.Request = value == "true"
					case "gateway_body_log_capture_response":
						c.Response = value == "true"
					}
				}
			}
		}
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(raw, &c); err != nil {
		return c, err
	}
	return c, c.Validate()
}

func (p *PostgresIndex) SaveConfig(ctx context.Context, c Config) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = p.DB.ExecContext(ctx, `INSERT INTO bodylog.config(id,value) VALUES(TRUE,$1::jsonb) ON CONFLICT(id) DO UPDATE SET value=EXCLUDED.value`, string(raw))
	return err
}

func (p *PostgresIndex) Put(ctx context.Context, r Record) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	var parent any
	if r.ParentID != "" {
		parent = r.ParentID
	}
	_, err = p.DB.ExecContext(ctx, `INSERT INTO bodylog.records(id,parent_id,created_at,ended_at,user_id,api_key_id,status,value)
VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb) ON CONFLICT(id) DO UPDATE SET
 ended_at=EXCLUDED.ended_at,user_id=EXCLUDED.user_id,api_key_id=EXCLUDED.api_key_id,status=EXCLUDED.status,value=EXCLUDED.value
 WHERE COALESCE((bodylog.records.value->>'revision')::bigint,0) <= COALESCE((EXCLUDED.value->>'revision')::bigint,0)`,
		r.ID, parent, r.CreatedAt, r.EndedAt, r.UserID, r.APIKeyID, r.Status, string(raw))
	return err
}

func (p *PostgresIndex) Get(ctx context.Context, id string) (Record, error) {
	var raw []byte
	var r Record
	err := p.DB.QueryRowContext(ctx, `SELECT value FROM bodylog.records WHERE id=$1`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(raw, &r)
	return r, err
}

func (p *PostgresIndex) List(ctx context.Context, f Filter) ([]Record, error) {
	query := `SELECT value FROM bodylog.records WHERE TRUE`
	args := []any{}
	add := func(clause string, value any) {
		args = append(args, value)
		query += " AND " + strings.ReplaceAll(clause, "?", "$"+strconv.Itoa(len(args)))
	}
	if f.RootsOnly {
		query += " AND parent_id IS NULL"
	}
	if f.AccountID > 0 {
		add("(value->>'account_id')::bigint = ?", f.AccountID)
	}
	if f.Platform != "" {
		add("value->>'platform' = ?", f.Platform)
	}
	if f.Kind != "" {
		add("value->>'kind' = ?", f.Kind)
	}
	if f.Model != "" {
		add("value->>'model' = ?", f.Model)
	}
	if f.RequestID != "" {
		add("value#>>'{metadata,client_request_id}' = ?", f.RequestID)
	}
	if !f.After.IsZero() {
		add("created_at >= ?", f.After)
	}
	if !f.Until.IsZero() {
		add("created_at <= ?", f.Until)
	}
	if !f.Before.IsZero() {
		if f.BeforeID != "" {
			args = append(args, f.Before, f.BeforeID)
			query += fmt.Sprintf(" AND (created_at,id) < ($%d,$%d::uuid)", len(args)-1, len(args))
		} else {
			add("created_at < ?", f.Before)
		}
	}
	if f.UserID > 0 {
		add("user_id = ?", f.UserID)
	}
	if f.APIKeyID > 0 {
		add("api_key_id = ?", f.APIKeyID)
	}
	if f.Status > 0 {
		add("status = ?", f.Status)
	}
	if f.ParentID != "" {
		add("parent_id = ?::uuid", f.ParentID)
	}
	if f.UsageID > 0 {
		add("(value->'usage_log_ids') @> ?::jsonb", fmt.Sprintf("[%d]", f.UsageID))
	}
	if f.Oldest {
		query += " AND ended_at IS NOT NULL AND value->>'state' != 'expired'"
	}
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	args = append(args, limit)
	order := "DESC"
	if f.Oldest {
		order = "ASC"
	}
	query += fmt.Sprintf(" ORDER BY created_at %s,id %s LIMIT $%d", order, order, len(args))
	rows, err := p.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]Record, 0)
	for rows.Next() {
		var raw []byte
		var r Record
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p *PostgresIndex) Prune(ctx context.Context, before time.Time) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM bodylog.records WHERE created_at < $1 AND value->>'state'='expired'`, before)
	if err == nil {
		_, err = p.DB.ExecContext(ctx, `DELETE FROM bodylog.task_links links WHERE created_at < $1
AND NOT EXISTS (SELECT 1 FROM bodylog.records records WHERE records.id=links.parent_id)`, before)
	}
	return err
}

func (p *PostgresIndex) BindTask(ctx context.Context, kind, id, parent string) error {
	_, err := p.DB.ExecContext(ctx, `INSERT INTO bodylog.task_links(kind,task_id,parent_id) VALUES($1,$2,$3)
ON CONFLICT(kind,task_id) DO NOTHING`, kind, id, parent)
	return err
}

func (p *PostgresIndex) TaskParent(ctx context.Context, kind, id string) (string, error) {
	var parent string
	err := p.DB.QueryRowContext(ctx, `SELECT parent_id FROM bodylog.task_links WHERE kind=$1 AND task_id=$2`, kind, id).Scan(&parent)
	return parent, err
}
