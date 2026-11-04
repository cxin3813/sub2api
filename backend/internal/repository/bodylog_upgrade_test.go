package repository

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/bodylog"
	_ "github.com/lib/pq"
)

// Opt-in acceptance against an empty disposable database. The source directory
// must contain the old fork's migration files, including the two BYTEA files.
// No application configuration or deployment connection is consulted.
func TestBodyLogUpgradeFromLegacyFork(t *testing.T) {
	dsn, source := os.Getenv("BODYLOG_UPGRADE_TEST_DSN"), os.Getenv("BODYLOG_UPGRADE_MIGRATIONS_DIR")
	if dsn == "" || source == "" {
		t.Skip("set BODYLOG_UPGRADE_TEST_DSN and BODYLOG_UPGRADE_MIGRATIONS_DIR for isolated upgrade acceptance")
	}
	for _, name := range []string{"145_gateway_body_logs.sql", "146_gateway_body_logs_usage_log_id.sql"} {
		if _, err := os.Stat(filepath.Join(source, name)); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var tables int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname='public'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatal("upgrade acceptance requires an empty disposable database")
	}
	if err := applyMigrationsFS(ctx, db, os.DirFS(source)); err != nil {
		t.Fatal("initialize legacy schema:", err)
	}
	var userID, keyID, accountID, usageID int64
	for _, step := range []struct {
		query string
		args  []any
		id    *int64
	}{
		{`INSERT INTO users(email,password_hash) VALUES('upgrade@example.invalid','unused-fixture') RETURNING id`, nil, &userID},
		{`INSERT INTO api_keys(user_id,key,name) VALUES($1,'bodylog-upgrade-synthetic','upgrade') RETURNING id`, []any{&userID}, &keyID},
		{`INSERT INTO accounts(name,platform,type) VALUES('upgrade','openai','apikey') RETURNING id`, nil, &accountID},
		{`INSERT INTO usage_logs(user_id,api_key_id,account_id,request_id,model) VALUES($1,$2,$3,'upgrade-legacy','fixture-model') RETURNING id`, []any{&userID, &keyID, &accountID}, &usageID},
	} {
		if err := db.QueryRowContext(ctx, step.query, step.args...).Scan(step.id); err != nil {
			t.Fatal(err)
		}
	}
	request, reply := []byte(`{"input":"legacy upgrade"}`), []byte("legacy upgrade reply")
	if _, err := db.ExecContext(ctx, `INSERT INTO gateway_body_logs(usage_log_id,request_id,api_key_id,request_body,response_body)
VALUES($1,'upgrade-legacy',$2,$3,$4)`, usageID, keyID, request, reply); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES
('gateway_body_log_enabled','true'),('gateway_body_log_capture_request','false'),
('gateway_body_log_capture_response','true'),('gateway_body_log_max_bytes','262144')
ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := ApplyMigrations(ctx, db); err != nil {
		t.Fatal("upgrade core schema:", err)
	}
	index := &bodylog.PostgresIndex{DB: db}
	if err := index.Init(ctx); err != nil {
		t.Fatal(err)
	}
	cfg, err := index.LoadConfig(ctx)
	if err != nil || !cfg.Enabled || cfg.Request || !cfg.Response || cfg.Limit != 0 {
		t.Fatalf("legacy capture switches or complete-capture default changed: %+v %v", cfg, err)
	}
	legacy, err := index.Legacy(ctx, usageID)
	if err != nil || legacy["request_body"] != string(request) || legacy["response_body"] != string(reply) {
		t.Fatalf("legacy bytes changed during upgrade: %+v %v", legacy, err)
	}
	if err := index.SaveConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	manager, err := bodylog.NewManager(t.TempDir(), index, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	session := manager.Begin(nil, "gateway_http", "POST", "/v1/responses")
	writer := session.Body("client_response", "application/json", "", nil)
	payload := []byte(`{"output":"new file body after upgrade"}`)
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	writer.Finish(true)
	session.AddUsage(usageID)
	session.End(200, nil)
	manager.Close()
	rows, err := index.List(ctx, bodylog.Filter{UsageID: usageID, Limit: 10})
	if err != nil || len(rows) != 1 || len(rows[0].Bodies) != 1 {
		t.Fatalf("new archive missing after upgrade: %+v %v", rows, err)
	}
	var restored bytes.Buffer
	if _, err := manager.WriteBody(ctx, rows[0], rows[0].Bodies[0].ID, &restored, 0, 0); err != nil || !bytes.Equal(restored.Bytes(), payload) {
		t.Fatalf("new archive bytes changed: %q %v", restored.Bytes(), err)
	}
	if err := ApplyMigrations(ctx, db); err != nil {
		t.Fatal("repeated startup:", err)
	}
	if err := index.Init(ctx); err != nil {
		t.Fatal("repeated module initialization:", err)
	}
	var after int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	t.Logf("legacy migrations=%d upgraded migrations=%d; legacy BYTEA, imported switches, new file archive and repeated startup passed", before, after)
}
