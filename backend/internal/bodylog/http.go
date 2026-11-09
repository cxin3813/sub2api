package bodylog

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/gin-gonic/gin"
)

type exchangeKey struct{}

// ObserveHTTP is shared by the standard upstream, plugin and dedicated-client
// adapters. Requests without a gateway scope pass through unchanged.
func ObserveHTTP(req *http.Request, accountID int64, next func(*http.Request) (*http.Response, error)) (*http.Response, error) {
	parent := FromContext(req.Context())
	observed, _ := req.Context().Value(exchangeKey{}).(bool)
	if parent == nil || observed {
		return next(req)
	}
	s := parent.manager.Begin(parent, "upstream_http", req.Method, req.URL.EscapedPath())
	if s == nil {
		return next(req)
	}
	s.Update(func(r *Record) {
		r.AccountID = accountID
		r.Metadata["host"] = req.URL.Host
		if req.URL.RawQuery != "" {
			r.Metadata["query"] = SafeQuery(req.URL.Query())
		}
	})
	scope := context.WithValue(WithSession(req.Context(), s), exchangeKey{}, true)
	copyReq := req.Clone(scope)
	requestSink := s.Body("upstream_request", req.Header.Get("Content-Type"), req.Header.Get("Content-Encoding"), req.Header)
	var lifecycle sync.Mutex
	requestDone := req.Body == nil || req.Body == http.NoBody
	responseDone := false
	status := 0
	finish := func(request bool) {
		lifecycle.Lock()
		if request {
			requestDone = true
		} else {
			responseDone = true
		}
		ended, code := requestDone && responseDone, status
		lifecycle.Unlock()
		if ended {
			s.End(code, nil)
		}
	}
	if requestDone {
		requestSink.Finish(true)
	} else {
		copyReq.Body = ObserveReader(req.Body, requestSink, func() { finish(true) })
	}
	resp, err := next(copyReq)
	if err != nil {
		s.End(0, err)
		return resp, err
	}
	if resp == nil {
		s.End(0, nil)
		return resp, nil
	}
	s.Update(func(r *Record) {
		r.Status = resp.StatusCode
		r.Metadata["upstream_request_id"] = resp.Header.Get("x-request-id")
	})
	lifecycle.Lock()
	status = resp.StatusCode
	lifecycle.Unlock()
	responseSink := s.Body("upstream_response", resp.Header.Get("Content-Type"), resp.Header.Get("Content-Encoding"), resp.Header)
	resp.Body = ObserveReader(resp.Body, responseSink, func() { finish(false) })
	return resp, nil
}

type Transport struct{ Base http.RoundTripper }

func ObserveClient(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	copy := *client
	copy.Transport = Transport{Base: client.Transport}
	return &copy
}

func (t Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return ObserveHTTP(req, 0, base.RoundTrip)
}

type GatewayRoutes struct{ routes map[string]bool }

type RouterOptions struct {
	Manager *Manager
	Routes  *GatewayRoutes
}

func NewGatewayRoutes() *GatewayRoutes { return &GatewayRoutes{routes: map[string]bool{}} }

func (g *GatewayRoutes) Register(before, after gin.RoutesInfo) {
	existing := map[string]bool{}
	for _, r := range before {
		existing[r.Method+" "+r.Path] = true
	}
	for _, r := range after {
		key := r.Method + " " + r.Path
		if !existing[key] {
			g.routes[key] = true
		}
	}
}

func (m *Manager) Gateway(g *GatewayRoutes, enrich func(*gin.Context, *Session)) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !g.routes[c.Request.Method+" "+c.FullPath()] || !m.Config().Enabled {
			c.Next()
			return
		}
		s := m.Begin(nil, "gateway_http", c.Request.Method, c.Request.URL.EscapedPath())
		if s == nil {
			c.Next()
			return
		}
		s.Update(func(r *Record) {
			r.Route = c.FullPath()
			r.Metadata["client_request_id"] = c.GetHeader("x-request-id")
			r.Metadata["user_agent"] = c.GetHeader("User-Agent")
			if c.Request.URL.RawQuery != "" {
				r.Metadata["query"] = SafeQuery(c.Request.URL.Query())
			}
		})
		original := c.Writer
		requestSink := s.Body("client_request", c.GetHeader("Content-Type"), c.GetHeader("Content-Encoding"), c.Request.Header)
		responseSink := s.Body("client_response", "", "", nil)
		c.Request = c.Request.WithContext(WithSession(c.Request.Context(), s))
		if c.Request.Body == nil || c.Request.Body == http.NoBody {
			requestSink.Finish(true)
		} else {
			c.Request.Body = ObserveReader(c.Request.Body, requestSink, nil)
		}
		w := &captureWriter{ResponseWriter: original, sink: responseSink}
		c.Writer = w
		defer func() {
			if enrich != nil {
				enrich(c, s)
			}
			if responseSink != nil {
				responseSink.body(func(b *Body) {
					b.Headers = SafeHeaders(original.Header())
					b.ContentType, b.Encoding = original.Header().Get("Content-Type"), original.Header().Get("Content-Encoding")
				})
				responseSink.Finish(c.Request.Context().Err() == nil)
			}
			s.End(original.Status(), c.Request.Context().Err())
			if c.Writer == w {
				c.Writer = original
			}
		}()
		c.Next()
	}
}

type captureWriter struct {
	gin.ResponseWriter
	sink *Sink
}

// ObserveSynthetic observes an existing background Gin invocation. The caller
// owns its lifecycle and invokes finish after the final write.
func ObserveSynthetic(c *gin.Context, s *Session) func() {
	if s == nil {
		return func() {}
	}
	original := c.Writer
	input := s.Body("client_request", c.GetHeader("Content-Type"), c.GetHeader("Content-Encoding"), c.Request.Header)
	if preread, ok := c.Request.Body.(*pkghttputil.PrereadBody); ok {
		// The synthetic task replays an already-consumed ingress snapshot. Keep
		// the concrete preread body so the upstream zero-copy/budget path survives.
		_, _ = input.Write(preread.Bytes())
		input.Finish(true)
	} else {
		c.Request.Body = ObserveReader(c.Request.Body, input, nil)
	}
	output := s.Body("client_response", "", "", nil)
	c.Writer = &captureWriter{ResponseWriter: original, sink: output}
	return func() {
		if output != nil {
			output.body(func(b *Body) {
				b.ContentType = original.Header().Get("Content-Type")
				b.Headers = SafeHeaders(original.Header())
			})
			output.Finish(c.Request.Context().Err() == nil)
		}
		s.End(original.Status(), c.Request.Context().Err())
	}
}

func (w *captureWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *captureWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if n > 0 {
		_, _ = w.sink.Write(p[:n])
	}
	return n, err
}

func (w *captureWriter) WriteString(p string) (int, error) {
	n, err := w.ResponseWriter.WriteString(p)
	if n > 0 {
		_, _ = io.WriteString(w.sink, p[:n])
	}
	return n, err
}

func (w *captureWriter) ReadFrom(r io.Reader) (int64, error) {
	// Do not let an embedded ReaderFrom bypass observation.
	return io.Copy(struct{ io.Writer }{w}, r)
}

func (w *captureWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.ResponseWriter.Hijack()
}

// DetachedScope copies only the log scope, not cancellation or mutable Gin state.
func DetachedScope(ctx context.Context) context.Context {
	return CopyContext(ctx, context.Background())
}

func (s *Session) Duration() time.Duration {
	r := s.Snapshot()
	end := time.Now()
	if r.EndedAt != nil {
		end = *r.EndedAt
	}
	return end.Sub(r.CreatedAt)
}
