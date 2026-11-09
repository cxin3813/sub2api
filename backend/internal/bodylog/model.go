// Package bodylog observes gateway traffic without participating in routing,
// protocol conversion or billing.
package bodylog

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrNotFound = errors.New("body log not found")

type Config struct {
	Enabled       bool   `json:"enabled"`
	Request       bool   `json:"capture_request"`
	Response      bool   `json:"capture_response"`
	Limit         int64  `json:"capture_limit_bytes"`
	RetentionDays int    `json:"retention_days"`
	Budget        int64  `json:"storage_budget_bytes"`
	Capacity      string `json:"capacity_policy"`
}

func DefaultConfig() Config {
	return Config{Request: true, Response: true, RetentionDays: 7, Budget: 10 << 30, Capacity: "delete_oldest_completed"}
}

func (c Config) Validate() error {
	if c.RetentionDays < 1 || c.RetentionDays > 36500 {
		return errors.New("retention_days must be between 1 and 36500")
	}
	if c.Budget < 1<<20 || c.Limit < 0 {
		return errors.New("storage budget must be at least 1 MiB and capture limit cannot be negative")
	}
	if c.Capacity != "delete_oldest_completed" && c.Capacity != "pause_capture" {
		return errors.New("invalid capacity_policy")
	}
	return nil
}

type Part struct {
	Key    string `json:"key"`
	Bytes  int64  `json:"bytes"`
	Stored int64  `json:"stored_bytes"`
	Codec  string `json:"codec"`
	Sealed bool   `json:"sealed"`
}

type Body struct {
	ID          string      `json:"id"`
	Direction   string      `json:"direction"`
	ContentType string      `json:"content_type"`
	Encoding    string      `json:"content_encoding,omitempty"`
	Headers     http.Header `json:"headers,omitempty"`
	Bytes       int64       `json:"bytes"`
	Saved       int64       `json:"saved_bytes"`
	State       string      `json:"state"`
	Error       string      `json:"error,omitempty"`
	Parts       []Part      `json:"parts"`
}

type Record struct {
	Revision    int64          `json:"revision"`
	ID          string         `json:"id"`
	ParentID    string         `json:"parent_id,omitempty"`
	Kind        string         `json:"kind"`
	CreatedAt   time.Time      `json:"created_at"`
	EndedAt     *time.Time     `json:"ended_at,omitempty"`
	Method      string         `json:"method"`
	Path        string         `json:"path"`
	Route       string         `json:"route,omitempty"`
	Status      int            `json:"status"`
	UserID      int64          `json:"user_id,omitempty"`
	APIKeyID    int64          `json:"api_key_id,omitempty"`
	AccountID   int64          `json:"account_id,omitempty"`
	GroupID     *int64         `json:"group_id,omitempty"`
	UsageIDs    []int64        `json:"usage_log_ids,omitempty"`
	Model       string         `json:"model,omitempty"`
	Platform    string         `json:"platform,omitempty"`
	State       string         `json:"state"`
	Persistence string         `json:"persistence"`
	Error       string         `json:"error,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Bodies      []Body         `json:"bodies"`
}

type Filter struct {
	Before    time.Time
	BeforeID  string
	UserID    int64
	APIKeyID  int64
	Status    int
	ParentID  string
	UsageID   int64
	Limit     int
	AccountID int64
	Platform  string
	Kind      string
	RootsOnly bool
	Model     string
	RequestID string
	After     time.Time
	Until     time.Time
	Oldest    bool
}

type Index interface {
	LoadConfig(context.Context) (Config, error)
	SaveConfig(context.Context, Config) error
	Put(context.Context, Record) error
	Get(context.Context, string) (Record, error)
	List(context.Context, Filter) ([]Record, error)
	Prune(context.Context, time.Time) error
}

// BodyStore references are private identifiers, never public URLs.
type BodyStore interface {
	Open(context.Context, Part) (io.ReadCloser, error)
}

type scopeKey struct{}

func WithSession(ctx context.Context, s *Session) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, scopeKey{}, s)
}

func FromContext(ctx context.Context) *Session {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(scopeKey{}).(*Session)
	return s
}

func CopyContext(parent, target context.Context) context.Context {
	return WithSession(target, FromContext(parent))
}

func SafeHeaders(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, values := range h {
		switch strings.ToLower(k) {
		case "authorization", "proxy-authorization", "x-api-key", "api-key", "cookie", "set-cookie", "x-goog-api-key", "x-oai-attestation":
			out[k] = []string{"[redacted]"}
		default:
			out[k] = append([]string(nil), values...)
		}
	}
	return out
}

func SafeQuery(query url.Values) url.Values {
	out := make(url.Values, len(query))
	for key, values := range query {
		switch strings.ToLower(key) {
		case "key", "api_key", "api-key", "apikey", "token", "access_token", "refresh_token", "signature", "x-amz-signature", "x-amz-credential", "x-amz-security-token":
			out[key] = []string{"[redacted]"}
		default:
			out[key] = append([]string(nil), values...)
		}
	}
	return out
}
