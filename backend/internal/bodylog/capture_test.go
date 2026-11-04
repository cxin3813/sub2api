package bodylog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type memoryIndex struct {
	mu     sync.Mutex
	rows   map[string]Record
	config Config
	fail   bool
}

func (i *memoryIndex) LoadConfig(context.Context) (Config, error)   { return i.config, nil }
func (i *memoryIndex) SaveConfig(_ context.Context, c Config) error { i.config = c; return nil }
func (i *memoryIndex) Put(_ context.Context, r Record) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.fail {
		return errors.New("index offline")
	}
	if i.rows == nil {
		i.rows = map[string]Record{}
	}
	raw, _ := json.Marshal(r)
	var copy Record
	_ = json.Unmarshal(raw, &copy)
	i.rows[r.ID] = copy
	return nil
}
func (i *memoryIndex) Get(_ context.Context, id string) (Record, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	r, ok := i.rows[id]
	if !ok {
		return r, ErrNotFound
	}
	return r, nil
}
func (i *memoryIndex) List(_ context.Context, f Filter) ([]Record, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	var out []Record
	for _, r := range i.rows {
		if f.Oldest && (r.EndedAt == nil || r.State == "expired") {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt.Before(out[b].CreatedAt) })
	return out, nil
}
func (i *memoryIndex) Prune(context.Context, time.Time) error { return nil }

func testManager(t *testing.T, index Index) *Manager {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Enabled = true
	m, err := NewManager(t.TempDir(), index, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m
}

func TestLargeBodySurvivesCompressionAndLateUsageLink(t *testing.T) {
	m := testManager(t, nil)
	s := m.Begin(nil, "gateway_http", "POST", "/v1/messages")
	w := s.Body("client_request", "application/json", "", nil)
	payload := bytes.Repeat([]byte("long context with tool results\n"), 350000)
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	w.Finish(true)
	s.End(200, nil)
	m.Close() // Drain asynchronous archival before inspecting the stored codec.
	m.syncRecord(s.record.ID)
	// Billing completion may arrive after the raw file has been archived.
	s.AddUsage(42)
	m.syncRecord(s.record.ID)
	r, err := m.Get(context.Background(), s.record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Bodies[0].State != "complete" || len(r.Bodies[0].Parts) < 2 {
		t.Fatalf("wrong large-body state: %+v", r.Bodies[0])
	}
	var restored bytes.Buffer
	if _, err := m.WriteBody(context.Background(), r, r.Bodies[0].ID, &restored, 0, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, restored.Bytes()) {
		t.Fatal("body changed or truncated")
	}
	if len(r.UsageIDs) != 1 || r.UsageIDs[0] != 42 {
		t.Fatal("late usage association lost")
	}
	for _, p := range r.Bodies[0].Parts {
		if p.Codec != "gzip" {
			t.Fatalf("part was not compressed: %+v", p)
		}
	}
}

func TestReaderDoesNotConsumeAheadAndClosesOnce(t *testing.T) {
	m := testManager(t, nil)
	s := m.Begin(nil, "upstream_http", "POST", "/messages")
	w := s.Body("upstream_response", "text/plain", "", nil)
	origin := &countedReader{Reader: bytes.NewReader([]byte("abcdef"))}
	reader := ObserveReader(origin, w, nil)
	buf := make([]byte, 2)
	n, err := reader.Read(buf)
	if n != 2 || err != nil || string(buf) != "ab" || origin.Len() != 4 {
		t.Fatal("observer changed the read contract")
	}
	_ = reader.Close()
	_ = reader.Close()
	s.End(200, nil)
	if origin.closed != 1 {
		t.Fatalf("closed %d times", origin.closed)
	}
	if b := s.Snapshot().Bodies[0]; b.State != "partial" || b.Saved != 2 {
		t.Fatalf("wrong partial state: %+v", b)
	}
}

type countedReader struct {
	*bytes.Reader
	closed int
}

func (r *countedReader) Close() error { r.closed++; return nil }

func TestGatewayCapturesRejectedAndSuccessfulRoutesWithoutUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := testManager(t, nil)
	r := gin.New()
	routes := NewGatewayRoutes()
	r.Use(m.Gateway(routes, nil))
	r.Use(gin.Recovery())
	before := r.Routes()
	r.POST("/v1/rejected", func(c *gin.Context) { c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized"}) })
	r.POST("/v1/messages", func(c *gin.Context) {
		_, _ = io.Copy(io.Discard, c.Request.Body)
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.WriteString("data: {\"text\":\"ok\"}\n\n")
		c.Writer.Flush()
	})
	r.GET("/v1/panic", func(c *gin.Context) { panic("provider failed") })
	routes.Register(before, r.Routes())
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/v1/rejected", 401}, {"/v1/messages", 200},
	} {
		resp := httptest.NewRecorder()
		req := httptest.NewRequest("POST", tc.path, bytes.NewBufferString(`{"input":"hello"}`))
		r.ServeHTTP(resp, req)
		if resp.Code != tc.status {
			t.Fatalf("%s returned %d", tc.path, resp.Code)
		}
	}
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, httptest.NewRequest("GET", "/v1/panic", nil))
	if resp.Code != 500 {
		t.Fatalf("panic status: %d", resp.Code)
	}
	entries, err := os.ReadDir(m.root)
	if err != nil {
		t.Fatal(err)
	}
	var records []Record
	for _, e := range entries {
		if validID(e.Name()) {
			row, err := m.load(e.Name())
			if err != nil {
				t.Fatal(err)
			}
			records = append(records, row)
		}
	}
	if len(records) != 3 {
		t.Fatalf("captured %d routes", len(records))
	}
	for _, row := range records {
		if row.Path == "/v1/rejected" && row.Bodies[0].State != "not_read" {
			t.Fatal("unread body reported as empty")
		}
		if row.Path == "/v1/panic" && row.Status != 500 {
			t.Fatal("panic recovery happened outside capture")
		}
		if len(row.UsageIDs) != 0 {
			t.Fatal("capture unexpectedly requires a usage log")
		}
	}
}

func TestCleanupProtectsActiveAndPausePolicy(t *testing.T) {
	index := &memoryIndex{}
	m := testManager(t, index)
	m.Close() // Drive archive/index/cleanup passes explicitly in this policy test.
	old := m.Begin(nil, "gateway_http", "POST", "/v1/messages")
	old.Update(func(r *Record) { r.CreatedAt = time.Now().Add(-8 * 24 * time.Hour) })
	w := old.Body("client_request", "text/plain", "", nil)
	_, _ = w.Write([]byte("retained until complete"))
	old.persist()
	m.syncRecord(old.record.ID)
	m.cleanup()
	if _, err := m.load(old.record.ID); err != nil {
		t.Fatal("active record was removed")
	}
	w.Finish(true)
	old.End(200, nil)
	m.syncRecord(old.record.ID)
	m.cleanup()
	r, err := m.Get(context.Background(), old.record.ID)
	if err != nil || r.State != "expired" || r.Error != "retention" {
		t.Fatalf("expiry missing: %+v %v", r, err)
	}
	current := m.Begin(nil, "gateway_http", "POST", "/v1/messages")
	cw := current.Body("client_request", "text/plain", "", nil)
	_, _ = cw.Write([]byte("keep me"))
	cw.Finish(true)
	current.End(200, nil)
	m.syncRecord(current.record.ID)
	cfg := m.Config()
	cfg.Capacity = "pause_capture"
	if err := m.Configure(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	m.needed.Store(cfg.Budget)
	m.cleanup()
	r, err = m.Get(context.Background(), current.record.ID)
	if err != nil || r.State == "expired" {
		t.Fatal("pause_capture deleted an unexpired record")
	}
}

func TestLoggingFailureDoesNotChangeHTTPBody(t *testing.T) {
	m := testManager(t, nil)
	s := m.Begin(nil, "gateway_http", "POST", "/responses")
	cfg := m.Config()
	cfg.Budget = 1 << 20
	m.config.Store(&cfg)
	req := httptest.NewRequest("POST", "http://provider.test/responses", bytes.NewReader(bytes.Repeat([]byte("a"), 2<<20)))
	req = req.WithContext(WithSession(req.Context(), s))
	resp, err := ObserveHTTP(req, 1, func(r *http.Request) (*http.Response, error) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil || len(body) != 2<<20 {
			t.Fatal("logging failure changed upstream request")
		}
		_ = r.Body.Close()
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewBufferString("original response"))}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	s.End(200, nil)
	if err != nil || string(reply) != "original response" {
		t.Fatal("logging failure changed upstream response")
	}
}

func TestNoBodyAndDisabledObservationPreserveHTTPContract(t *testing.T) {
	m := testManager(t, nil)
	s := m.Begin(nil, "gateway_http", "GET", "/v1/models")
	req := httptest.NewRequest("GET", "http://provider.test/v1/models", nil)
	req.Body = http.NoBody
	req = req.WithContext(WithSession(req.Context(), s))
	resp, err := ObserveHTTP(req, 7, func(observed *http.Request) (*http.Response, error) {
		if observed.Body != http.NoBody || observed.ContentLength != req.ContentLength {
			t.Fatal("empty request became an unknown-length request body")
		}
		return &http.Response{StatusCode: 204, Header: http.Header{}, Body: http.NoBody}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	s.End(204, nil)
	original := io.NopCloser(bytes.NewBufferString("not copied"))
	if ObserveReader(original, nil, nil) != original {
		t.Fatal("disabled request capture added a reader wrapper")
	}
}

func TestActiveBufferedBodyCanBeViewed(t *testing.T) {
	m := testManager(t, nil)
	s := m.Begin(nil, "gateway_http", "POST", "/v1/responses")
	w := s.Body("client_response", "text/event-stream", "", nil)
	_, _ = w.Write([]byte("data: still running\n\n"))
	r := s.Snapshot()
	var out bytes.Buffer
	if _, err := m.WriteBody(context.Background(), r, r.Bodies[0].ID, &out, 0, 0); err != nil {
		t.Fatal(err)
	}
	if out.String() != "data: still running\n\n" || s.Snapshot().Bodies[0].State != "open" {
		t.Fatal("viewing an active buffer lost content or completed the response")
	}
	s.End(200, nil)
}

func TestUpstreamResponseCanFinishBeforeTheRequestReader(t *testing.T) {
	m := testManager(t, nil)
	parent := m.Begin(nil, "gateway_http", "POST", "/v1/audio/transcriptions")
	req := httptest.NewRequest("POST", "http://provider.test/upload", bytes.NewBufferString("still uploading"))
	req = req.WithContext(WithSession(req.Context(), parent))
	var sent *http.Request
	var exchange *Session
	resp, err := ObserveHTTP(req, 7, func(observed *http.Request) (*http.Response, error) {
		sent, exchange = observed, FromContext(observed.Context())
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: http.NoBody}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	parent.End(200, nil)
	if exchange.Snapshot().EndedAt != nil {
		t.Fatal("response completion prematurely closed an active upload")
	}
	_, _ = io.Copy(io.Discard, sent.Body)
	_ = sent.Body.Close()
	row := exchange.Snapshot()
	if row.EndedAt == nil || row.Bodies[0].State != "complete" || row.Bodies[0].Saved != int64(len("still uploading")) {
		t.Fatalf("late upload bytes were lost: %+v", row)
	}
}

func TestRestartRecoversOnlyBytesThatReachedDisk(t *testing.T) {
	m := testManager(t, nil)
	m.Close()
	s := m.Begin(nil, "gateway_http", "POST", "/v1/responses")
	w := s.Body("client_request", "text/plain", "", nil)
	_, _ = w.Write(bytes.Repeat([]byte("x"), bufferBytes))
	_, _ = w.Write(bytes.Repeat([]byte("x"), 37))
	s.persist() // Manifest can describe an unflushed tail.
	w.mu.Lock()
	if err := w.file.Close(); err != nil {
		t.Fatal(err)
	}
	w.file, w.writer = nil, nil // Simulate process loss without a graceful flush.
	w.mu.Unlock()
	part := s.Snapshot().Bodies[0].Parts[0]
	partPath, _ := m.partPath(part.Key)
	temporary := strings.TrimSuffix(partPath, ".raw") + ".gz.next"
	if err := os.WriteFile(temporary, []byte("interrupted compression"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := m.Config()
	recovered, err := NewManager(m.root, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	recovered.syncRecord(s.record.ID)
	if _, err := os.Stat(temporary); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("interrupted compression kept consuming the file budget")
	}
	r, err := recovered.Get(context.Background(), s.record.ID)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	_, err = recovered.WriteBody(context.Background(), r, r.Bodies[0].ID, &out, 0, 0)
	if err != nil || r.State != "interrupted" || r.Bodies[0].State != "partial" || r.Bodies[0].Saved != int64(out.Len()) || r.Bodies[0].Saved >= r.Bodies[0].Bytes {
		t.Fatalf("recovery misrepresented the captured bytes: %+v actual=%d err=%v", r, out.Len(), err)
	}
}

func TestBudgetFailureLeavesSearchableMetadataAndShutdownFinishes(t *testing.T) {
	index := &memoryIndex{}
	m := testManager(t, index)
	cfg := m.Config()
	cfg.Budget = 1 << 20
	m.config.Store(&cfg)
	m.used.Store(cfg.Budget)
	s := m.Begin(nil, "gateway_http", "POST", "/v1/messages")
	s.End(401, nil)
	m.Close()
	row, err := index.Get(context.Background(), s.record.ID)
	if err != nil || row.Status != 401 || row.Persistence != "error" {
		t.Fatalf("failed capture disappeared from the index: %+v %v", row, err)
	}
	// The retry queue must not prevent a bounded shutdown.
	cfg.Budget = 10 << 30
	dir := t.TempDir()
	live, err := NewManager(dir, index, cfg)
	if err != nil {
		t.Fatal(err)
	}
	live.used.Store(cfg.Budget)
	capture := live.Begin(nil, "gateway_http", "POST", "/v1/messages")
	capture.End(400, nil)
	done := make(chan struct{})
	go func() { live.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown was trapped by retries")
	}
}

func TestCleanupDoesNotDeleteAnUnindexedManifestRevision(t *testing.T) {
	index := &memoryIndex{}
	m := testManager(t, index)
	m.Close()
	s := m.Begin(nil, "gateway_http", "POST", "/v1/messages")
	s.Update(func(r *Record) { r.CreatedAt = time.Now().Add(-8 * 24 * time.Hour) })
	w := s.Body("client_request", "text/plain", "", nil)
	_, _ = w.Write([]byte("unindexed usage update"))
	w.Finish(true)
	s.End(200, nil)
	m.syncRecord(s.record.ID)
	s.AddUsage(123)
	m.cleanup()
	dir, _ := m.recordPath(s.record.ID)
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatal("cleanup removed an unacknowledged manifest")
	}
	m.syncRecord(s.record.ID)
	m.cleanup()
	row, _ := index.Get(context.Background(), s.record.ID)
	if row.State != "expired" {
		t.Fatal("cleanup did not resume after the index was acknowledged")
	}
}

func TestRetentionDeniesReadsBeforePhysicalCleanup(t *testing.T) {
	m := testManager(t, nil)
	m.Close()
	s := m.Begin(nil, "gateway_http", "POST", "/v1/messages")
	s.Update(func(r *Record) { r.CreatedAt = time.Now().Add(-8 * 24 * time.Hour) })
	w := s.Body("client_response", "text/plain", "", nil)
	_, _ = w.Write([]byte("expired content"))
	w.Finish(true)
	s.End(200, nil)
	local, err := m.load(s.record.ID)
	if err != nil {
		t.Fatal(err)
	}
	row, err := m.Get(context.Background(), s.record.ID)
	if err != nil || row.State != "expired" {
		t.Fatal("expired body remained readable while cleanup was pending")
	}
	if _, err := m.WriteBody(context.Background(), local, local.Bodies[0].ID, io.Discard, 0, 0); !errors.Is(err, ErrNotFound) {
		t.Fatal("stale metadata allowed a read after retention expiry")
	}
}

func TestReducingBudgetBelowCurrentUsageStillCleansCompletedLogs(t *testing.T) {
	index := &memoryIndex{}
	m := testManager(t, index)
	m.Close()
	s := m.Begin(nil, "gateway_http", "POST", "/v1/messages")
	w := s.Body("client_request", "application/octet-stream", "", nil)
	payload := make([]byte, 2<<20)
	_, _ = rand.New(rand.NewSource(1)).Read(payload)
	_, _ = w.Write(payload)
	w.Finish(true)
	s.End(200, nil)
	m.syncRecord(s.record.ID)
	cfg := m.Config()
	cfg.Budget = 1 << 20
	if m.used.Load() <= cfg.Budget {
		t.Fatal("fixture did not exceed the new budget")
	}
	if err := m.Configure(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	m.cleanup()
	row, err := index.Get(context.Background(), s.record.ID)
	if err != nil || row.State != "expired" || row.Error != "capacity" || m.used.Load() > cfg.Budget {
		t.Fatalf("reduced budget prevented its own cleanup: state=%s reason=%s used=%d err=%v", row.State, row.Error, m.used.Load(), err)
	}
}
