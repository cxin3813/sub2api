//go:build integration

package bodylog

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

// This test requires an explicitly supplied disposable database. It never
// reads application configuration or connects to a running deployment.
func TestPostgresArchiveSettingsLegacyAndTaskRecovery(t *testing.T) {
	dsn := os.Getenv("BODYLOG_TEST_DSN")
	if dsn == "" {
		t.Skip("set BODYLOG_TEST_DSN to an isolated disposable PostgreSQL database")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	index := &PostgresIndex{DB: db}
	if err := index.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM bodylog.config WHERE id=TRUE`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT);
CREATE TABLE IF NOT EXISTS usage_logs(id BIGINT PRIMARY KEY,request_id TEXT,api_key_id BIGINT);
INSERT INTO settings(key,value) VALUES('gateway_body_log_enabled','true'),
('gateway_body_log_capture_request','false'),('gateway_body_log_capture_response','true') ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`); err != nil {
		t.Fatal(err)
	}
	cfg, err := index.LoadConfig(ctx)
	if err != nil || !cfg.Enabled || cfg.Request || !cfg.Response || cfg.Limit != 0 {
		t.Fatalf("legacy switches not preserved: %+v %v", cfg, err)
	}
	cfg.Request = true
	cfg.RetentionDays = 3
	cfg.Budget = 15 << 20
	if err := index.SaveConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	read, err := index.LoadConfig(ctx)
	if err != nil || read.RetentionDays != 3 || read.Budget != 15<<20 {
		t.Fatalf("configuration not persisted: %+v %v", read, err)
	}
	for _, name := range []string{"145_gateway_body_logs.sql", "146_gateway_body_logs_usage_log_id.sql"} {
		raw, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO usage_logs(id,request_id,api_key_id) VALUES(42,'legacy',1) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO gateway_body_logs(usage_log_id,request_id,api_key_id,request_body,response_body) VALUES(42,'legacy',1,$1,$2)
ON CONFLICT(usage_log_id) DO NOTHING`, []byte(`{"input":"old"}`), []byte("legacy reply")); err != nil {
		t.Fatal(err)
	}
	legacy, err := index.Legacy(ctx, 42)
	if err != nil || legacy["response_body"] != "legacy reply" {
		t.Fatalf("legacy body unavailable: %+v %v", legacy, err)
	}
	m, err := NewManager(t.TempDir(), index, cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := m.Begin(nil, "gateway_http", "POST", "/v1/responses")
	body := s.Body("client_request", "application/json", "", nil)
	_, _ = body.Write([]byte(`{"input":"saved through PostgreSQL index"}`))
	body.Finish(true)
	taskID := uuid.NewString()
	s.BindTask(ctx, "batch_image", taskID)
	s.End(200, nil)
	m.Close()
	s.AddUsage(42)
	m.syncRecord(s.record.ID)
	row, err := index.Get(ctx, s.record.ID)
	if err != nil || len(row.Bodies) != 1 || len(row.UsageIDs) != 1 {
		local, localErr := m.load(s.record.ID)
		t.Logf("session=%+v local=%+v localErr=%v", s.Snapshot(), local, localErr)
		t.Fatalf("index not restored: %+v %v", row, err)
	}
	var restored bytes.Buffer
	if _, err := m.WriteBody(ctx, row, row.Bodies[0].ID, &restored, 0, 0); err != nil {
		t.Fatal(err)
	}
	if restored.String() != `{"input":"saved through PostgreSQL index"}` {
		t.Fatal("index references wrong body")
	}
	list, err := index.List(ctx, Filter{UsageID: 42})
	if err != nil || len(list) < 1 {
		t.Fatalf("usage association query failed: %v", err)
	}
	scoped, task := m.ResumeTask(ctx, "batch_image", taskID)
	if FromContext(scoped) != task || task.Snapshot().ParentID != s.record.ID {
		t.Fatal("task parent was not recovered")
	}
	task.End(0, nil)
	m.syncRecord(task.record.ID)
	// A composite time/id cursor must not lose requests sharing a timestamp.
	stamp := time.Now().UTC().Truncate(time.Second)
	pageParent := uuid.NewString()
	a := Record{ID: uuid.NewString(), ParentID: pageParent, CreatedAt: stamp, State: "closed", Bodies: []Body{}}
	b := Record{ID: uuid.NewString(), ParentID: pageParent, CreatedAt: stamp, State: "closed", Bodies: []Body{}}
	if err := index.Put(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := index.Put(ctx, b); err != nil {
		t.Fatal(err)
	}
	if a.ID < b.ID {
		a, b = b, a
	}
	first, err := index.List(ctx, Filter{ParentID: pageParent, Limit: 1})
	if err != nil || len(first) != 1 || first[0].ID != a.ID {
		t.Fatalf("first page must use the same descending ID order as its cursor: %+v %v", first, err)
	}
	page, err := index.List(ctx, Filter{ParentID: pageParent, Before: stamp, BeforeID: a.ID})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range page {
		found = found || r.ID == b.ID
	}
	if !found {
		t.Fatal("pagination lost a record with the same timestamp")
	}
}
