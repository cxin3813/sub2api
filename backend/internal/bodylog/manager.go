package bodylog

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
)

const (
	bufferBytes = 64 << 10
	partBytes   = 8 << 20
)

type Manager struct {
	root        string
	index       Index
	config      atomic.Pointer[Config]
	used        atomic.Int64
	revision    atomic.Int64
	needed      atomic.Int64
	failures    atomic.Int64
	buffers     atomic.Int64
	mu          sync.Mutex
	active      map[string]*Session
	readers     map[string]int
	deleting    map[string]bool
	parseSlots  chan struct{}
	lockMu      sync.Mutex
	fileLocks   map[string]*recordLock
	ticketMu    sync.Mutex
	tickets     map[string]downloadTicket
	queue       chan string
	queueMu     sync.Mutex
	queued      map[string]bool
	fallback    chan Record
	stop        chan struct{}
	done        chan struct{}
	closeOnce   sync.Once
	lastError   atomic.Value
	floorBytes  int64
	maintenance sync.Mutex
}

func NewManager(root string, index Index, cfg Config) (*Manager, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(root) {
		return nil, errors.New("body log directory must be absolute")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	// Refuse to adopt arbitrary non-empty directories.
	marker := filepath.Join(root, ".bodylog-v2")
	if _, err := os.Stat(marker); errors.Is(err, os.ErrNotExist) {
		entries, readErr := os.ReadDir(root)
		if readErr != nil {
			return nil, readErr
		}
		if len(entries) != 0 {
			return nil, errors.New("body log directory is not an owned empty store")
		}
		if err := os.WriteFile(marker, []byte("bodylog-v2\n"), 0600); err != nil {
			return nil, err
		}
	}
	m := &Manager{root: root, index: index, active: map[string]*Session{}, readers: map[string]int{}, deleting: map[string]bool{}, parseSlots: make(chan struct{}, 2), queue: make(chan string, 256), fallback: make(chan Record, 256), stop: make(chan struct{}), done: make(chan struct{})}
	m.floorBytes = 1 << 30
	m.revision.Store(time.Now().UnixNano())
	m.config.Store(&cfg)
	// Inventory our own store, not arbitrary siblings or Docker volumes.
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			st, statErr := d.Info()
			if statErr != nil {
				return statErr
			}
			m.used.Add(st.Size())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	go m.run()
	return m, nil
}

func (m *Manager) Config() Config { return *m.config.Load() }

func (m *Manager) Configure(ctx context.Context, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if m.index == nil {
		return errors.New("body log configuration store unavailable")
	}
	if err := m.index.SaveConfig(ctx, c); err != nil {
		return err
	}
	m.config.Store(&c)
	m.enqueue("")
	return nil
}

func (m *Manager) Stats() map[string]any {
	last, _ := m.lastError.Load().(string)
	return map[string]any{"used_bytes": m.used.Load(), "active_buffer_bytes": m.buffers.Load(), "failure_count": m.failures.Load(), "last_error": last, "queued_records": len(m.queue)}
}

func (m *Manager) failure(err error) {
	if err == nil {
		return
	}
	m.failures.Add(1)
	m.lastError.Store(err.Error())
	slog.Warn("body log persistence failed", "error", err)
}

func (m *Manager) reserve(n int64) bool {
	for {
		old := m.used.Load()
		if n < 0 || old+n > m.Config().Budget {
			m.needed.Store(max(m.needed.Load(), n))
			m.enqueue("")
			return false
		}
		if m.used.CompareAndSwap(old, old+n) {
			return true
		}
	}
}

func (m *Manager) reserveBody(n int64) bool {
	var disk syscall.Statfs_t
	if err := syscall.Statfs(m.root, &disk); err != nil || uint64(disk.Bavail)*uint64(disk.Bsize) < uint64(m.floorBytes)+uint64(n) {
		return false
	}
	// Leave working space for an atomic manifest/tombstone replacement.
	headroom := min(int64(1<<20), m.Config().Budget/16)
	if !m.reserve(n + headroom) {
		return false
	}
	m.used.Add(-headroom)
	return true
}

func validID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u.String() == id
}

// Locks exist only while in use. A slow download holds its own record rather
// than blocking unrelated captures that happen to hash to the same bucket.
type recordLock struct {
	rw      sync.RWMutex
	manager *Manager
	id      string
	refs    int
}

func (m *Manager) fileLock(id string) *recordLock {
	m.lockMu.Lock()
	defer m.lockMu.Unlock()
	if m.fileLocks == nil {
		m.fileLocks = map[string]*recordLock{}
	}
	lock := m.fileLocks[id]
	if lock == nil {
		lock = &recordLock{manager: m, id: id}
		m.fileLocks[id] = lock
	}
	lock.refs++
	return lock
}

func (l *recordLock) release() {
	l.manager.lockMu.Lock()
	defer l.manager.lockMu.Unlock()
	l.refs--
	if l.refs == 0 {
		delete(l.manager.fileLocks, l.id)
	}
}

func (l *recordLock) Lock()    { l.rw.Lock() }
func (l *recordLock) RLock()   { l.rw.RLock() }
func (l *recordLock) Unlock()  { l.rw.Unlock(); l.release() }
func (l *recordLock) RUnlock() { l.rw.RUnlock(); l.release() }
func (l *recordLock) TryLock() bool {
	if l.rw.TryLock() {
		return true
	}
	l.release()
	return false
}

func (m *Manager) recordPath(id string) (string, error) {
	if !validID(id) {
		return "", ErrNotFound
	}
	return filepath.Join(m.root, id), nil
}

func (m *Manager) partPath(key string) (string, error) {
	pieces := strings.Split(key, "/")
	if len(pieces) != 2 || !validID(pieces[0]) || strings.ContainsAny(pieces[1], `/\`) {
		return "", ErrNotFound
	}
	base := strings.Split(pieces[1], ".")
	if len(base) != 3 || !validID(base[0]) || (base[2] != "raw" && base[2] != "gz") {
		return "", ErrNotFound
	}
	for _, c := range base[1] {
		if c < '0' || c > '9' {
			return "", ErrNotFound
		}
	}
	if base[1] == "" {
		return "", ErrNotFound
	}
	return filepath.Join(m.root, key), nil
}

func (m *Manager) manifest(r Record) error {
	return m.writeManifest(r, false)
}

func (m *Manager) writeManifest(r Record, reclaim bool) error {
	r.Revision = m.revision.Add(1)
	dir, err := m.recordPath(r.ID)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	target := filepath.Join(dir, "manifest.json")
	var previous int64
	if st, err := os.Stat(target); err == nil {
		previous = st.Size()
	}
	if !m.reserve(int64(len(raw))) {
		if !reclaim || previous == 0 || r.State != "expired" {
			return errors.New("body log capacity exhausted")
		}
		// A reduced budget can already be below current usage. Permit the
		// cleanup's single replacement manifest so it can reclaim that usage.
		// It replaces an existing manifest; body writes never bypass budget.
		m.used.Add(int64(len(raw)))
	}
	temp := filepath.Join(dir, "manifest.next")
	f, err := os.OpenFile(temp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err == nil {
		_, err = f.Write(raw)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err == nil {
		err = os.Rename(temp, target)
	}
	if err != nil {
		_ = os.Remove(temp)
		m.used.Add(-int64(len(raw)))
		return err
	}
	m.used.Add(-previous)
	if dirFile, openErr := os.Open(dir); openErr == nil {
		err = dirFile.Sync()
		_ = dirFile.Close()
	}
	return err
}

func (m *Manager) load(id string) (Record, error) {
	var r Record
	dir, err := m.recordPath(id)
	if err != nil {
		return r, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(raw, &r); err != nil || r.ID != id {
		return r, ErrNotFound
	}
	return r, nil
}

func (m *Manager) enqueue(id string) {
	m.queueMu.Lock()
	defer m.queueMu.Unlock()
	if m.queued[id] {
		return
	}
	select {
	case m.queue <- id:
		if m.queued == nil {
			m.queued = make(map[string]bool)
		}
		m.queued[id] = true
	default:
		// Manifests remain on disk; the bounded recovery scan retries them.
	}
}

func (m *Manager) syncQueued(id string) {
	// An update that arrives during archival may enqueue one more pass. Until
	// then, repeated writes to the same manifest occupy only one queue slot.
	m.queueMu.Lock()
	delete(m.queued, id)
	m.queueMu.Unlock()
	m.syncRecord(id)
}

func (m *Manager) enqueueFallback(r Record, err error) {
	r.Revision = m.revision.Add(1)
	r.Persistence, r.Error = "error", err.Error()
	select {
	case m.fallback <- r:
	default:
		m.failure(errors.New("body log metadata fallback queue exhausted"))
	}
}

func (m *Manager) writeFallback(r Record) {
	if m.index != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		m.failure(m.index.Put(ctx, r))
		cancel()
	}
}

func (m *Manager) run() {
	defer close(m.done)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var directory *os.File
	defer func() {
		if directory != nil {
			_ = directory.Close()
		}
	}()
	for {
		select {
		case <-m.stop:
			// Retrying failed capacity/index work can enqueue more work. Drain
			// only the existing bounded batch so shutdown cannot loop forever.
			pending := len(m.queue) + len(m.fallback)
			for range pending {
				select {
				case id := <-m.queue:
					m.syncQueued(id)
				case r := <-m.fallback:
					m.writeFallback(r)
				default:
					return
				}
			}
			return
		case id := <-m.queue:
			m.syncQueued(id)
		case r := <-m.fallback:
			m.writeFallback(r)
		case <-ticker.C:
			if directory == nil {
				directory, _ = os.Open(m.root)
			}
			if directory != nil {
				names, err := directory.Readdirnames(32)
				for _, id := range names {
					if validID(id) {
						m.syncRecord(id)
					}
				}
				if err != nil {
					_ = directory.Close()
					directory = nil
				}
			}
			m.cleanup()
		}
	}
}

func (m *Manager) syncRecord(id string) {
	if id == "" {
		m.cleanup()
		return
	}
	// A viewer must not lose a raw part while it is being replaced by gzip.
	lock := m.fileLock(id)
	if !lock.TryLock() {
		return
	}
	held := true
	defer func() {
		if held {
			lock.Unlock()
		}
	}()
	m.mu.Lock()
	s := m.active[id]
	m.mu.Unlock()
	var r Record
	var err error
	if s != nil {
		r = s.Snapshot()
	} else {
		m.recoverTemporaryFiles(id)
		r, err = m.load(id)
		if err != nil {
			// A full filesystem may have prevented the initial manifest while
			// the bounded metadata fallback succeeded. Restore it when space
			// returns, using only this store's discovered UUID directories.
			if m.index == nil {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			r, err = m.index.Get(ctx, id)
			cancel()
			if err != nil {
				return
			}
		}
		allCompressed := r.EndedAt != nil
		for _, b := range r.Bodies {
			for _, part := range b.Parts {
				allCompressed = allCompressed && part.Codec == "gzip"
			}
		}
		dir, _ := m.recordPath(id)
		indexed, indexErr := os.ReadFile(filepath.Join(dir, "indexed"))
		indexRevision, _ := strconv.ParseInt(string(indexed), 10, 64)
		if allCompressed && r.State != "expired" && indexErr == nil && indexRevision == r.Revision {
			return
		}
	}
	if r.State == "expired" {
		if m.index != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err := m.index.Put(ctx, r)
			cancel()
			if err != nil {
				m.failure(err)
				return
			}
			m.removeBodies(r)
		}
		return
	}
	// A process restart has no live reader/writer for this orphaned session.
	if s == nil && r.EndedAt == nil {
		now := time.Now().UTC()
		r.EndedAt, r.State = &now, "interrupted"
		for i := range r.Bodies {
			if r.Bodies[i].State == "open" {
				r.Bodies[i].State = "partial"
			}
			for j := range r.Bodies[i].Parts {
				part := &r.Bodies[i].Parts[j]
				path, pathErr := m.partPath(part.Key)
				if pathErr == nil {
					if st, statErr := os.Stat(path); statErr == nil && part.Codec == "identity" {
						part.Bytes, part.Stored = st.Size(), st.Size()
					}
				}
				part.Sealed = true
			}
			var saved int64
			for _, part := range r.Bodies[i].Parts {
				saved += part.Bytes
			}
			r.Bodies[i].Saved = saved
			if r.Bodies[i].Bytes < saved {
				r.Bodies[i].Bytes = saved
			}
		}
	}
	changed := false
	for bi := range r.Bodies {
		for pi := range r.Bodies[bi].Parts {
			p := &r.Bodies[bi].Parts[pi]
			if !p.Sealed || p.Codec != "identity" {
				continue
			}
			next, compErr := m.compress(*p)
			if compErr != nil {
				m.failure(compErr)
				continue
			}
			*p = next
			changed = true
		}
	}
	if s != nil && changed {
		s.replaceParts(r)
		r = s.Snapshot()
	}
	r.Persistence = "durable_local"
	for _, b := range r.Bodies {
		for _, p := range b.Parts {
			if !p.Sealed {
				r.Persistence = "buffered"
			}
		}
	}
	if err := m.manifest(r); err != nil {
		m.failure(err)
		// Capacity or filesystem failure must still leave a searchable request
		// when the metadata store is available.
		m.enqueueFallback(r, err)
		return
	}
	r, err = m.load(r.ID)
	if err != nil {
		m.failure(err)
		return
	}
	for _, b := range r.Bodies {
		for _, p := range b.Parts {
			if p.Codec != "gzip" {
				continue
			}
			oldPath, pathErr := m.partPath(strings.TrimSuffix(p.Key, ".gz") + ".raw")
			if pathErr == nil {
				if st, statErr := os.Stat(oldPath); statErr == nil {
					if removeErr := os.Remove(oldPath); removeErr == nil {
						m.used.Add(-st.Size())
					}
				}
			}
		}
	}
	if m.index != nil {
		// Database availability must not hold the file lock needed by capture
		// writes or request completion. Acknowledge only this exact revision.
		lock.Unlock()
		held = false
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err = m.index.Put(ctx, r)
		cancel()
		m.failure(err)
		if err == nil {
			lock = m.fileLock(r.ID)
			lock.Lock()
			held = true
			current, loadErr := m.load(r.ID)
			if loadErr != nil || current.Revision != r.Revision {
				m.enqueue(r.ID)
				return
			}
			dir, _ := m.recordPath(r.ID)
			marker := filepath.Join(dir, "indexed")
			ack := []byte(strconv.FormatInt(r.Revision, 10))
			var oldSize int64
			if st, statErr := os.Stat(marker); statErr == nil {
				oldSize = st.Size()
			}
			if m.reserve(int64(len(ack))) {
				if writeErr := os.WriteFile(marker, ack, 0600); writeErr == nil {
					m.used.Add(-oldSize)
				} else {
					m.used.Add(-int64(len(ack)))
					m.failure(writeErr)
				}
			}
		}
	}
}

func (m *Manager) recoverTemporaryFiles(id string) {
	dir, err := m.recordPath(id)
	if err != nil {
		return
	}
	temp := filepath.Join(dir, "manifest.next")
	if st, err := os.Stat(temp); err == nil {
		main := filepath.Join(dir, "manifest.json")
		if _, mainErr := os.Stat(main); errors.Is(mainErr, os.ErrNotExist) {
			raw, readErr := os.ReadFile(temp)
			var candidate Record
			if readErr == nil && json.Unmarshal(raw, &candidate) == nil && candidate.ID == id {
				if renameErr := os.Rename(temp, main); renameErr != nil {
					m.failure(renameErr)
				}
			}
		} else if mainErr == nil && os.Remove(temp) == nil {
			m.used.Add(-st.Size())
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".gz.next") {
			continue
		}
		key := strings.TrimSuffix(entry.Name(), ".next")
		compressed, err := m.partPath(id + "/" + key)
		if err != nil {
			continue
		}
		raw := strings.TrimSuffix(compressed, ".gz") + ".raw"
		// Compression has not committed. The original is the recovery source.
		if _, err := os.Stat(raw); err == nil {
			if st, err := entry.Info(); err == nil && os.Remove(compressed+".next") == nil {
				m.used.Add(-st.Size())
			}
		}
	}
}

func (m *Manager) compress(p Part) (Part, error) {
	path, err := m.partPath(p.Key)
	if err != nil {
		return p, err
	}
	in, err := os.Open(path)
	if err != nil {
		return p, err
	}
	defer func() { _ = in.Close() }()
	// Budget temporary output too; a failed attempt leaves the original intact.
	reserved := p.Bytes + (64 << 10)
	if !m.reserve(reserved) {
		return p, errors.New("body log has no compression headroom")
	}
	nextPath := strings.TrimSuffix(path, ".raw") + ".gz"
	if st, statErr := os.Stat(nextPath); statErr == nil {
		m.used.Add(-reserved)
		p.Key = strings.TrimSuffix(p.Key, ".raw") + ".gz"
		p.Stored, p.Codec = st.Size(), "gzip"
		return p, nil
	}
	out, err := os.OpenFile(nextPath+".next", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		m.used.Add(-reserved)
		return p, err
	}
	z, _ := gzip.NewWriterLevel(out, gzip.BestSpeed)
	_, copyErr := io.Copy(z, in)
	closeErr := z.Close()
	syncErr := out.Sync()
	fileErr := out.Close()
	for _, e := range []error{copyErr, closeErr, syncErr, fileErr} {
		if err == nil {
			err = e
		}
	}
	if err == nil {
		err = os.Rename(nextPath+".next", nextPath)
	}
	if err != nil {
		_ = os.Remove(nextPath + ".next")
		m.used.Add(-reserved)
		return p, err
	}
	st, err := os.Stat(nextPath)
	if err != nil {
		m.used.Add(-reserved)
		return p, err
	}
	m.used.Add(st.Size() - reserved)
	// The manifest is switched before unlinking by replaceParts/manifest.
	p.Key = strings.TrimSuffix(p.Key, ".raw") + ".gz"
	p.Stored, p.Codec = st.Size(), "gzip"
	return p, nil
}

func (m *Manager) cleanup() {
	if m.index == nil {
		return
	}
	m.maintenance.Lock()
	defer m.maintenance.Unlock()
	cfg := m.Config()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Fetch the oldest completed records through the time index.
	candidates, err := m.index.List(ctx, Filter{Oldest: true, Limit: 32})
	if err != nil {
		m.failure(err)
		return
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].CreatedAt.Before(candidates[j].CreatedAt) })
	for _, r := range candidates {
		if r.EndedAt == nil || r.State == "expired" {
			continue
		}
		expired := time.Since(r.CreatedAt) > time.Duration(cfg.RetentionDays)*24*time.Hour
		pressure := cfg.Capacity == "delete_oldest_completed" && m.used.Load()+m.needed.Load() > cfg.Budget
		if !expired && !pressure {
			break
		}
		lock := m.fileLock(r.ID)
		if !lock.TryLock() {
			continue
		}
		m.mu.Lock()
		active := m.active[r.ID] != nil || m.readers[r.ID] > 0
		if !active {
			m.deleting[r.ID] = true
		}
		m.mu.Unlock()
		if active {
			lock.Unlock()
			continue
		}
		local, err := m.load(r.ID)
		if err != nil || local.EndedAt == nil || local.Persistence != "durable_local" {
			m.mu.Lock()
			delete(m.deleting, r.ID)
			m.mu.Unlock()
			lock.Unlock()
			continue
		}
		dir, _ := m.recordPath(r.ID)
		ack, ackErr := os.ReadFile(filepath.Join(dir, "indexed"))
		indexRevision, _ := strconv.ParseInt(string(ack), 10, 64)
		if ackErr != nil || indexRevision != local.Revision {
			m.enqueue(r.ID)
			m.mu.Lock()
			delete(m.deleting, r.ID)
			m.mu.Unlock()
			lock.Unlock()
			continue
		}
		reason := "capacity"
		if expired {
			reason = "retention"
		}
		// Persist a tombstone before deleting any body. A restart resumes cleanup.
		local.State, local.Error = "expired", reason
		local.Persistence = "durable_local"
		for i := range local.Bodies {
			local.Bodies[i].State = "unavailable"
			local.Bodies[i].Error = "cleaned_by_" + reason
		}
		if err := m.writeManifest(local, true); err != nil {
			m.failure(err)
			m.mu.Lock()
			delete(m.deleting, r.ID)
			m.mu.Unlock()
			lock.Unlock()
			continue
		}
		if err := m.index.Put(ctx, local); err != nil {
			m.failure(err)
			m.mu.Lock()
			delete(m.deleting, r.ID)
			m.mu.Unlock()
			lock.Unlock()
			continue
		}
		m.removeBodies(local)
		m.mu.Lock()
		delete(m.deleting, r.ID)
		m.mu.Unlock()
		lock.Unlock()
	}
	if m.used.Load()+m.needed.Load() <= cfg.Budget {
		m.needed.Store(0)
	}
	m.failure(m.index.Prune(ctx, time.Now().Add(-time.Duration(cfg.RetentionDays+1)*24*time.Hour)))
}

func (m *Manager) removeBodies(r Record) {
	dir, err := m.recordPath(r.ID)
	if err != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == "manifest.json" {
			continue
		}
		if e.Name() == "indexed" {
			if st, statErr := e.Info(); statErr == nil && os.Remove(filepath.Join(dir, e.Name())) == nil {
				m.used.Add(-st.Size())
			}
			continue
		}
		path, err := m.partPath(r.ID + "/" + e.Name())
		if err != nil {
			continue
		}
		st, err := e.Info()
		if err == nil {
			if err = os.Remove(path); err == nil {
				m.used.Add(-st.Size())
			} else {
				m.failure(err)
			}
		}
	}
	// Once the index tombstone is durable and payloads are gone, retain the
	// reason in PostgreSQL without accumulating manifests on the file budget.
	remaining, err := os.ReadDir(dir)
	if err == nil && len(remaining) == 1 && remaining[0].Name() == "manifest.json" {
		st, statErr := remaining[0].Info()
		if statErr == nil && os.Remove(filepath.Join(dir, "manifest.json")) == nil {
			m.used.Add(-st.Size())
			_ = os.Remove(dir)
		}
	}
}

func (m *Manager) Close() {
	m.closeOnce.Do(func() { close(m.stop) })
	<-m.done
}

type gzipFile struct {
	*gzip.Reader
	file *os.File
}

func (g *gzipFile) Close() error {
	zerr := g.Reader.Close()
	ferr := g.file.Close()
	if zerr != nil {
		return zerr
	}
	return ferr
}

func (m *Manager) Open(_ context.Context, p Part) (io.ReadCloser, error) {
	path, err := m.partPath(p.Key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if p.Codec != "gzip" {
		return f, nil
	}
	z, err := gzip.NewReader(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &gzipFile{Reader: z, file: f}, nil
}

func (m *Manager) Get(ctx context.Context, id string) (Record, error) {
	if !validID(id) {
		return Record{}, ErrNotFound
	}
	m.mu.Lock()
	s := m.active[id]
	m.mu.Unlock()
	if s != nil {
		return s.Snapshot(), nil
	}
	var r Record
	var err error
	if m.index == nil {
		r, err = m.load(id)
	} else {
		r, err = m.index.Get(ctx, id)
	}
	if m.retentionExpired(r) {
		r.State, r.Error = "expired", "retention"
		for i := range r.Bodies {
			r.Bodies[i].State, r.Bodies[i].Error = "unavailable", "cleaned_by_retention"
		}
	}
	return r, err
}

func (m *Manager) retentionExpired(r Record) bool {
	return r.EndedAt != nil && time.Since(r.CreatedAt) > time.Duration(m.Config().RetentionDays)*24*time.Hour
}

func (m *Manager) WriteBody(ctx context.Context, r Record, bodyID string, w io.Writer, offset, limit int64) (int64, error) {
	if r.State == "expired" || m.retentionExpired(r) {
		return 0, ErrNotFound
	}
	// Flush before taking the file lock: a writer can hold its sink mutex while
	// persisting a newly opened part. This order avoids a lock inversion.
	m.mu.Lock()
	live := m.active[r.ID]
	m.mu.Unlock()
	if live != nil {
		live.mu.Lock()
		sinks := append([]*Sink(nil), live.sinks...)
		live.mu.Unlock()
		for _, sink := range sinks {
			sink.mu.Lock()
			if sink.writer != nil {
				if err := sink.writer.Flush(); err != nil {
					sink.fail(err)
				}
			}
			sink.mu.Unlock()
		}
	}
	lock := m.fileLock(r.ID)
	lock.RLock()
	defer lock.RUnlock()
	m.mu.Lock()
	if m.deleting[r.ID] {
		m.mu.Unlock()
		return 0, ErrNotFound
	}
	m.readers[r.ID]++
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.readers[r.ID]--
		if m.readers[r.ID] == 0 {
			delete(m.readers, r.ID)
		}
		m.mu.Unlock()
	}()
	current, err := m.load(r.ID)
	if err != nil || current.State == "expired" || m.retentionExpired(current) {
		return 0, ErrNotFound
	}
	m.mu.Lock()
	active := m.active[r.ID]
	m.mu.Unlock()
	if active != nil {
		r = active.Snapshot()
	} else {
		r = current
	}
	for _, b := range r.Bodies {
		if b.ID != bodyID {
			continue
		}
		var n int64
		for _, p := range b.Parts {
			if err := ctx.Err(); err != nil {
				return n, err
			}
			if offset >= p.Bytes {
				offset -= p.Bytes
				continue
			}
			reader, err := m.Open(ctx, p)
			if err != nil {
				return n, err
			}
			if offset > 0 {
				_, err = io.CopyN(io.Discard, reader, offset)
			}
			offset = 0
			if err == nil {
				var written int64
				if limit > 0 {
					written, err = io.CopyN(w, reader, limit-n)
					if errors.Is(err, io.EOF) {
						err = nil
					}
				} else {
					written, err = io.Copy(w, reader)
				}
				n += written
			}
			_ = reader.Close()
			if err != nil || (limit > 0 && n >= limit) {
				return n, err
			}
		}
		return n, nil
	}
	return 0, fmt.Errorf("%w: body", ErrNotFound)
}
