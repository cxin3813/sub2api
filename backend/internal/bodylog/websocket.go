package bodylog

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/url"
	"sync"
	"time"

	coderws "github.com/coder/websocket"
)

func StartChild(ctx context.Context, kind, method, path string, accountID int64) (context.Context, *Session) {
	parent := FromContext(ctx)
	if parent == nil {
		return ctx, nil
	}
	host := ""
	if u, err := url.Parse(path); err == nil && u.Host != "" {
		host, path = u.Host, u.EscapedPath()
	}
	s := parent.manager.Begin(parent, kind, method, path)
	if s == nil {
		return ctx, nil
	}
	s.Update(func(r *Record) {
		r.AccountID = accountID
		if host != "" {
			r.Metadata["host"] = host
		}
	})
	return WithSession(ctx, s), s
}

func WriteWS(ctx context.Context, conn *coderws.Conn, kind coderws.MessageType, payload []byte) error {
	err := conn.Write(ctx, kind, payload)
	if err == nil {
		ObserveMessage(ctx, "client_response", int(kind), payload)
	}
	return err
}

// Message payloads are stored losslessly with length/type/time framing. This
// avoids concatenating JSON documents and avoids base64-expanding binary data.
type MessageWriter struct {
	mu   sync.Mutex
	sink *Sink
}

func (s *Session) MessageWriter(direction string) *MessageWriter {
	if s == nil {
		return nil
	}
	w := s.Body(direction, "application/x-bodylog-ws", "", nil)
	if w == nil {
		return nil
	}
	_, _ = w.Write([]byte("BWS1"))
	return &MessageWriter{sink: w}
}

func (w *MessageWriter) Write(kind int, payload []byte) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	var header [13]byte
	binary.BigEndian.PutUint32(header[:4], uint32(len(payload)))
	header[4] = byte(kind)
	binary.BigEndian.PutUint64(header[5:], uint64(time.Now().UnixNano()))
	_, _ = w.sink.Write(header[:])
	_, _ = w.sink.Write(payload)
}

func (w *MessageWriter) Finish() {
	if w != nil {
		w.sink.Finish(true)
	}
}

// ObserveMessage obtains a scope-bound stream, never binding a pooled socket
// permanently to its first borrower.
func ObserveMessage(ctx context.Context, direction string, kind int, payload []byte) {
	s := FromContext(ctx)
	if s == nil {
		return
	}
	s.Update(func(r *Record) {
		key := direction + "_messages"
		count, _ := r.Metadata[key].(int)
		r.Metadata[key] = count + 1
	})
	s.messageMu.Lock()
	if s.messages == nil {
		s.messages = map[string]*MessageWriter{}
	}
	w, ok := s.messages[direction]
	if ok && w != nil && kind == 1 && len(payload) <= parseBudget {
		// Start a fresh directional body at an explicit protocol turn
		// boundary. Limits apply to each turn; file parts continue rolling.
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(payload, &event) == nil &&
			(event.Type == "response.create" || event.Type == "response.created" || event.Type == "message_start") {
			w.Finish()
			w = s.MessageWriter(direction)
			s.messages[direction] = w
		}
	}
	if !ok {
		w = s.MessageWriter(direction)
		s.messages[direction] = w
	}
	s.messageMu.Unlock()
	w.Write(kind, payload)
}

func ObserveJSON(ctx context.Context, direction string, value any) {
	if FromContext(ctx) == nil {
		return
	}
	if raw, err := json.Marshal(value); err == nil {
		ObserveMessage(ctx, direction, 1, raw)
	}
}
