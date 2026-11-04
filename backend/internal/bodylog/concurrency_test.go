package bodylog

import (
	"context"
	"sync"
	"testing"
	"time"
)

type blockedIndex struct {
	memoryIndex
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (i *blockedIndex) Put(ctx context.Context, r Record) error {
	i.once.Do(func() {
		close(i.entered)
		select {
		case <-i.release:
		case <-ctx.Done():
		}
	})
	return i.memoryIndex.Put(ctx, r)
}

func TestBlockedIndexDoesNotHoldTheCaptureFileLock(t *testing.T) {
	index := &blockedIndex{entered: make(chan struct{}), release: make(chan struct{})}
	m := testManager(t, index)
	s := m.Begin(nil, "gateway_http", "POST", "/v1/responses")
	defer close(index.release)
	select {
	case <-index.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("index worker did not start")
	}
	done := make(chan struct{})
	go func() {
		w := s.Body("client_response", "text/plain", "", nil)
		_, _ = w.Write([]byte("response stays independent of metadata persistence"))
		w.Finish(true)
		s.End(200, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("index persistence blocked gateway body capture or completion")
	}
}

func TestConcurrentEndAndUsageAssociationRemainDurable(t *testing.T) {
	m := testManager(t, nil)
	s := m.Begin(nil, "gateway_http", "POST", "/v1/messages")
	var workers sync.WaitGroup
	for n := range 12 {
		workers.Add(1)
		go func(id int64) { defer workers.Done(); s.AddUsage(id) }(int64(n + 1))
	}
	workers.Add(1)
	go func() { defer workers.Done(); s.End(200, nil) }()
	workers.Wait()
	m.syncRecord(s.record.ID)
	row, err := m.Get(context.Background(), s.record.ID)
	if err != nil || len(row.UsageIDs) != 12 || row.EndedAt == nil {
		t.Fatalf("concurrent completion lost usage links: %+v %v", row, err)
	}
}

func TestArchiveBurstDoesNotCrowdOutDistinctRequests(t *testing.T) {
	index := &blockedIndex{entered: make(chan struct{}), release: make(chan struct{})}
	m := testManager(t, index)
	m.Begin(nil, "gateway_http", "POST", "/v1/responses")
	var release sync.Once
	defer release.Do(func() { close(index.release) })
	<-index.entered
	ids := make([]string, 0, 80)
	for n := range 80 {
		s := m.Begin(nil, "gateway_http", "POST", "/v1/messages")
		w := s.Body("client_request", "text/plain", "", nil)
		_, _ = w.Write([]byte("complete burst request"))
		w.Finish(true)
		s.End(200, nil)
		s.AddUsage(int64(n + 1))
		ids = append(ids, s.record.ID)
	}
	// The worker is blocked in its first index write. Start/part/end/usage
	// updates must not fill the queue with duplicates and lose later requests.
	m.queueMu.Lock()
	pending := make(map[string]bool)
	for range len(m.queue) {
		id := <-m.queue
		pending[id] = true
		m.queue <- id
	}
	m.queueMu.Unlock()
	for _, id := range ids {
		if !pending[id] {
			t.Fatalf("burst request %s was deferred to the slow recovery scan", id)
		}
	}
	release.Do(func() { close(index.release) })
}
