package bodylog

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Session struct {
	mu        sync.Mutex
	manager   *Manager
	record    Record
	config    Config
	sinks     []*Sink
	ended     bool
	finalized bool
	messageMu sync.Mutex
	messages  map[string]*MessageWriter
}

type Sink struct {
	mu      sync.Mutex
	session *Session
	id      string
	index   int
	file    *os.File
	writer  *bufio.Writer
	inPart  int64
	closed  bool
	touched bool
}

func (m *Manager) Begin(parent *Session, kind, method, path string) *Session {
	cfg := m.Config()
	if !cfg.Enabled {
		return nil
	}
	id := uuid.NewString()
	dir, _ := m.recordPath(id)
	if err := os.Mkdir(dir, 0700); err != nil {
		m.failure(err)
		return nil
	}
	r := Record{ID: id, Kind: kind, CreatedAt: time.Now().UTC(), Method: method, Path: path,
		State: "open", Persistence: "buffered", Metadata: map[string]any{}, Bodies: []Body{}}
	if parent != nil {
		pr := parent.Snapshot()
		r.ParentID, r.UserID, r.APIKeyID, r.GroupID = pr.ID, pr.UserID, pr.APIKeyID, pr.GroupID
		r.Platform = pr.Platform
	}
	s := &Session{manager: m, record: r, config: cfg}
	m.mu.Lock()
	m.active[id] = s
	m.mu.Unlock()
	lock := m.fileLock(id)
	lock.Lock()
	if err := m.manifest(r); err != nil {
		m.failure(err)
	}
	lock.Unlock()
	m.enqueue(id)
	return s
}

func (s *Session) Snapshot() Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	// The bounded manifest contains references and metadata, never body bytes.
	raw, _ := json.Marshal(s.record)
	var out Record
	_ = json.Unmarshal(raw, &out)
	return out
}

func (s *Session) Update(fn func(*Record)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	fn(&s.record)
	s.mu.Unlock()
}

func (s *Session) AddUsage(id int64) { s.LinkUsage(id, nil) }

// LinkUsage persists late metadata together with its usage association.
func (s *Session) LinkUsage(id int64, enrich func(*Record)) {
	if s == nil || id <= 0 {
		return
	}
	lock := s.manager.fileLock(s.record.ID)
	lock.Lock()
	defer lock.Unlock()
	s.mu.Lock()
	ended := s.finalized
	s.mu.Unlock()
	add := func(r *Record) {
		if enrich != nil {
			if r.Metadata == nil {
				r.Metadata = map[string]any{}
			}
			enrich(r)
		}
		for _, existing := range r.UsageIDs {
			if existing == id {
				return
			}
		}
		r.UsageIDs = append(r.UsageIDs, id)
	}
	if ended {
		r, err := s.manager.load(s.record.ID)
		if err != nil && s.manager.index != nil {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			r, err = s.manager.index.Get(ctx, s.record.ID)
			cancel()
		}
		if err == nil {
			add(&r)
			if err := s.manager.manifest(r); err != nil {
				s.manager.failure(err)
				s.manager.enqueueFallback(r, err)
			}
			s.manager.enqueue(r.ID)
		}
	} else {
		s.Update(add)
		s.persistLocked()
	}
}

func (s *Session) persist() {
	if s == nil {
		return
	}
	lock := s.manager.fileLock(s.record.ID)
	lock.Lock()
	defer lock.Unlock()
	s.persistLocked()
}

func (s *Session) persistLocked() {
	r := s.Snapshot()
	if err := s.manager.manifest(r); err != nil {
		s.manager.failure(err)
		s.manager.enqueueFallback(r, err)
	}
	s.manager.enqueue(s.record.ID)
}

func (s *Session) Body(direction, contentType, encoding string, headers http.Header) *Sink {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return nil
	}
	isRequest := direction == "client_request" || direction == "upstream_request"
	enabled := s.config.Response
	if isRequest {
		enabled = s.config.Request
	}
	if !enabled {
		return nil
	}
	b := Body{ID: uuid.NewString(), Direction: direction, ContentType: contentType, Encoding: encoding, Headers: SafeHeaders(headers), State: "open", Parts: []Part{}}
	s.record.Bodies = append(s.record.Bodies, b)
	w := &Sink{session: s, id: b.ID, index: len(s.record.Bodies) - 1}
	s.sinks = append(s.sinks, w)
	return w
}

func (s *Session) End(status int, err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	sinks := append([]*Sink(nil), s.sinks...)
	s.mu.Unlock()
	s.messageMu.Lock()
	for _, w := range s.messages {
		w.Finish()
	}
	s.messageMu.Unlock()
	for _, sink := range sinks {
		sink.Finish(false)
	}
	lock := s.manager.fileLock(s.record.ID)
	lock.Lock()
	defer lock.Unlock()
	now := time.Now().UTC()
	s.Update(func(r *Record) {
		r.Status, r.EndedAt, r.State = status, &now, "closed"
		if status == 101 {
			r.ConnectionState = "closed"
			if firstError, ok := r.Metadata["ws_first_message_error"].(string); ok && firstError != "" {
				r.ConnectionState, r.InteractionState = "first_message_timeout", "not_started"
			} else if count, ok := r.Metadata["client_request_messages"].(int); ok && count > 0 {
				r.InteractionState = "completed"
			} else {
				r.InteractionState = "not_started"
			}
		} else {
			r.ConnectionState, r.InteractionState = "not_applicable", "completed"
		}
		if err != nil {
			r.Error, r.State = err.Error(), "interrupted"
		}
	})
	s.persistLocked()
	// Release retained per-part buffers after completion. Detached usage work
	// only needs the session identifier and current manifest.
	s.mu.Lock()
	s.sinks, s.finalized = nil, true
	s.mu.Unlock()
	s.manager.mu.Lock()
	delete(s.manager.active, s.record.ID)
	s.manager.mu.Unlock()
}

func (s *Session) replaceParts(snapshot Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.record.Bodies {
		for j := range s.record.Bodies[i].Parts {
			current := &s.record.Bodies[i].Parts[j]
			for _, b := range snapshot.Bodies {
				if b.ID != s.record.Bodies[i].ID {
					continue
				}
				for _, p := range b.Parts {
					if current.Key == p.Key || (current.Sealed && strings.TrimSuffix(current.Key, ".raw") == strings.TrimSuffix(p.Key, ".gz")) {
						if p.Sealed && p.Codec == "gzip" {
							*current = p
						}
					}
				}
			}
		}
	}
}

func (w *Sink) body(fn func(*Body)) {
	w.session.mu.Lock()
	fn(&w.session.record.Bodies[w.index])
	w.session.mu.Unlock()
}

func (w *Sink) openPart() error {
	m := w.session.manager
	// Capture buffers are bounded across all concurrent directions, not only
	// within each request. Failure is visible without changing business I/O.
	if m.buffers.Add(bufferBytes) > 64<<20 {
		m.buffers.Add(-bufferBytes)
		return fmt.Errorf("body log active buffer budget exhausted")
	}
	opened := false
	defer func() {
		if !opened {
			m.buffers.Add(-bufferBytes)
		}
	}()
	n := 0
	w.body(func(b *Body) { n = len(b.Parts) })
	key := fmt.Sprintf("%s/%s.%06d.raw", w.session.record.ID, w.id, n)
	path, err := w.session.manager.partPath(key)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	w.file, w.writer, w.inPart = f, bufio.NewWriterSize(f, bufferBytes), 0
	opened = true
	w.body(func(b *Body) { b.Parts = append(b.Parts, Part{Key: key, Codec: "identity"}) })
	w.session.persist()
	return nil
}

// Write only reports errors in the log's own state. It must not turn an
// observation failure into a business request/response read error.
func (w *Sink) Write(p []byte) (int, error) {
	if w == nil {
		return len(p), nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return len(p), nil
	}
	w.touched = true
	total := len(p)
	var saved int64
	var failed bool
	w.body(func(b *Body) {
		b.Bytes += int64(total)
		saved, failed = b.Saved, b.State == "unavailable" || b.State == "truncated"
	})
	if failed {
		return total, nil
	}
	if limit := w.session.config.Limit; limit > 0 && saved+int64(len(p)) > limit {
		p = p[:max(0, limit-saved)]
		w.body(func(b *Body) { b.State, b.Error = "truncated", "configured_capture_limit" })
	}
	for len(p) > 0 {
		if w.file == nil {
			if err := w.openPart(); err != nil {
				w.fail(err)
				return total, nil
			}
		}
		n := min(int64(len(p)), int64(partBytes)-w.inPart)
		if !w.session.manager.reserveBody(n) {
			w.fail(fmt.Errorf("body log storage budget exhausted"))
			return total, nil
		}
		written, err := w.writer.Write(p[:n])
		w.session.manager.used.Add(int64(written) - n)
		w.inPart += int64(written)
		w.body(func(b *Body) {
			b.Saved += int64(written)
			part := &b.Parts[len(b.Parts)-1]
			part.Bytes, part.Stored = w.inPart, w.inPart
		})
		p = p[written:]
		if err != nil {
			w.fail(err)
			return total, nil
		}
		if w.inPart >= partBytes {
			if err := w.seal(); err != nil {
				w.fail(err)
				return total, nil
			}
			w.session.persist()
		}
	}
	return total, nil
}

func (w *Sink) fail(err error) {
	w.body(func(b *Body) { b.State, b.Error = "unavailable", err.Error() })
	w.session.manager.failure(err)
}

func (w *Sink) seal() error {
	if w.file == nil {
		return nil
	}
	flushErr := w.writer.Flush()
	syncErr := w.file.Sync()
	closeErr := w.file.Close()
	w.file, w.writer = nil, nil
	w.session.manager.buffers.Add(-bufferBytes)
	if flushErr != nil {
		return flushErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	w.body(func(b *Body) { b.Parts[len(b.Parts)-1].Sealed = true })
	return nil
}

func (w *Sink) Finish(complete bool) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.closed = true
	if err := w.seal(); err != nil {
		w.fail(err)
	}
	w.body(func(b *Body) {
		if b.State == "open" {
			switch {
			case complete && b.Bytes == 0:
				b.State = "empty"
			case complete:
				b.State = "complete"
			case !w.touched:
				b.State = "not_read"
			default:
				b.State = "partial"
			}
		}
	})
}

type observedReader struct {
	io.ReadCloser
	sink     *Sink
	onClose  func()
	once     sync.Once
	done     sync.Once
	closeErr error
}

func ObserveReader(body io.ReadCloser, sink *Sink, onClose func()) io.ReadCloser {
	if body == nil || body == http.NoBody {
		sink.Finish(true)
		if onClose != nil {
			onClose()
		}
		return body
	}
	if sink == nil && onClose == nil {
		return body
	}
	return &observedReader{ReadCloser: body, sink: sink, onClose: onClose}
}

func (r *observedReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 {
		_, _ = r.sink.Write(p[:n])
	}
	if err == io.EOF {
		r.sink.Finish(true)
	} else if err != nil {
		r.sink.Finish(false)
	}
	if err != nil && r.onClose != nil {
		r.done.Do(r.onClose)
	}
	return n, err
}

func (r *observedReader) Close() error {
	r.once.Do(func() {
		r.closeErr = r.ReadCloser.Close()
		r.sink.Finish(false)
		if r.onClose != nil {
			r.done.Do(r.onClose)
		}
	})
	return r.closeErr
}
