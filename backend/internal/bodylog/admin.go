package bodylog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type downloadTicket struct {
	Record string
	Body   string
	Until  time.Time
}

// RegisterDownload serves one-use, short-lived capabilities issued only through
// the authenticated admin API. Native browser downloads stream to disk.
func (m *Manager) RegisterDownload(v1 *gin.RouterGroup) {
	v1.GET("/body-log-download/:token", func(c *gin.Context) {
		m.ticketMu.Lock()
		ticket, ok := m.tickets[c.Param("token")]
		delete(m.tickets, c.Param("token"))
		m.ticketMu.Unlock()
		if !ok || time.Now().After(ticket.Until) {
			bodyLogAPIError(c, ErrNotFound)
			return
		}
		r, err := m.Get(c.Request.Context(), ticket.Record)
		if err != nil || r.State == "expired" {
			bodyLogAPIError(c, ErrNotFound)
			return
		}
		c.Header("Content-Type", "application/octet-stream")
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.bin"`, r.ID, ticket.Body))
		c.Header("Cache-Control", "no-store")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("X-Content-Type-Options", "nosniff")
		_, err = m.WriteBody(c.Request.Context(), r, ticket.Body, c.Writer, 0, 0)
		m.failure(err)
	})
}

// RegisterAdmin is called only inside the existing authenticated, audited
// administrator route group.
func (m *Manager) RegisterAdmin(admin *gin.RouterGroup) {
	admin.Use(m.legacySettingsCompatibility())
	g := admin.Group("/body-logs")
	g.GET("/config", func(c *gin.Context) {
		response.Success(c, gin.H{"config": m.Config(), "stats": m.Stats()})
	})
	g.PUT("/config", func(c *gin.Context) {
		var cfg Config
		if err := c.ShouldBindJSON(&cfg); err != nil {
			response.BadRequest(c, "Invalid body log configuration")
			return
		}
		if err := cfg.Validate(); err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		if err := m.Configure(c.Request.Context(), cfg); err != nil {
			response.Error(c, 503, "Body log settings could not be saved")
			return
		}
		response.Success(c, cfg)
	})
	g.GET("", func(c *gin.Context) {
		f := Filter{Limit: 50}
		f.UserID, _ = strconv.ParseInt(c.Query("user_id"), 10, 64)
		f.APIKeyID, _ = strconv.ParseInt(c.Query("api_key_id"), 10, 64)
		f.UsageID, _ = strconv.ParseInt(c.Query("usage_log_id"), 10, 64)
		f.Status, _ = strconv.Atoi(c.Query("status"))
		f.Limit, _ = strconv.Atoi(c.Query("limit"))
		f.Before, _ = time.Parse(time.RFC3339Nano, c.Query("before"))
		f.BeforeID = c.Query("before_id")
		f.ParentID = c.Query("parent_id")
		if f.ParentID != "" && !validID(f.ParentID) {
			response.BadRequest(c, "Invalid parent identifier")
			return
		}
		if f.BeforeID != "" && !validID(f.BeforeID) {
			response.BadRequest(c, "Invalid pagination cursor")
			return
		}
		if m.index == nil {
			response.Error(c, 503, "Body log index unavailable")
			return
		}
		rows, err := m.index.List(c.Request.Context(), f)
		if err != nil {
			response.Error(c, 503, "Body log index unavailable")
			return
		}
		response.Success(c, gin.H{"items": rows})
	})
	g.GET("/:id", func(c *gin.Context) {
		r, err := m.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			bodyLogAPIError(c, err)
			return
		}
		response.Success(c, r)
	})
	g.POST("/:id/download/:body", func(c *gin.Context) {
		r, err := m.Get(c.Request.Context(), c.Param("id"))
		if err != nil || r.State == "expired" {
			bodyLogAPIError(c, ErrNotFound)
			return
		}
		found := false
		for _, b := range r.Bodies {
			found = found || b.ID == c.Param("body")
		}
		if !found {
			bodyLogAPIError(c, ErrNotFound)
			return
		}
		m.ticketMu.Lock()
		defer m.ticketMu.Unlock()
		if m.tickets == nil {
			m.tickets = map[string]downloadTicket{}
		}
		for key, ticket := range m.tickets {
			if time.Now().After(ticket.Until) {
				delete(m.tickets, key)
			}
		}
		if len(m.tickets) >= 128 {
			response.Error(c, 429, "Too many pending body downloads")
			return
		}
		token := uuid.NewString()
		m.tickets[token] = downloadTicket{Record: r.ID, Body: c.Param("body"), Until: time.Now().Add(time.Minute)}
		response.Success(c, gin.H{"path": "/body-log-download/" + token})
	})
	g.GET("/:id/bodies/:body", func(c *gin.Context) {
		r, err := m.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			bodyLogAPIError(c, err)
			return
		}
		if r.State == "expired" {
			response.Error(c, http.StatusGone, "Body log content has been cleaned by its retention policy")
			return
		}
		bodyID := c.Param("body")
		var body *Body
		for i := range r.Bodies {
			if r.Bodies[i].ID == bodyID {
				body = &r.Bodies[i]
			}
		}
		if body == nil {
			bodyLogAPIError(c, ErrNotFound)
			return
		}
		if c.Query("download") == "1" {
			c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.bin"`, r.ID, bodyID))
			c.Header("X-Content-Type-Options", "nosniff")
			c.Header("Cache-Control", "no-store")
			c.Header("Content-Type", "application/octet-stream")
			_, err := m.WriteBody(c.Request.Context(), r, bodyID, c.Writer, 0, 0)
			if err != nil {
				m.failure(err)
			}
			return
		}
		offset, _ := strconv.ParseInt(c.Query("offset"), 10, 64)
		if offset < 0 {
			response.BadRequest(c, "Invalid offset")
			return
		}
		limit, _ := strconv.ParseInt(c.Query("limit"), 10, 64)
		if limit <= 0 || limit > 256<<10 {
			limit = 64 << 10
		}
		var out bytes.Buffer
		n, err := m.WriteBody(c.Request.Context(), r, bodyID, &out, offset, limit)
		if err != nil {
			bodyLogAPIError(c, err)
			return
		}
		raw := out.Bytes()
		// Keep a split trailing UTF-8 rune for the next page.
		if n+offset < body.Saved {
			for len(raw) > 0 && !utf8.Valid(raw) && len(out.Bytes())-len(raw) < utf8.UTFMax {
				raw = raw[:len(raw)-1]
			}
		}
		next := offset + int64(len(raw))
		response.Success(c, gin.H{"content": string(raw), "offset": offset, "next_offset": next,
			"has_more": next < body.Saved, "saved_bytes": body.Saved, "valid_utf8": utf8.Valid(raw)})
	})
	g.GET("/:id/parsed/:body", func(c *gin.Context) {
		r, err := m.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			bodyLogAPIError(c, err)
			return
		}
		cursor, _ := strconv.Atoi(c.Query("cursor"))
		result, err := m.Parse(c.Request.Context(), r, c.Param("body"), max(0, cursor), 50)
		if err != nil {
			bodyLogAPIError(c, err)
			return
		}
		response.Success(c, result)
	})
	admin.GET("/usage/:id/body-log", func(c *gin.Context) {
		pg, ok := m.index.(*PostgresIndex)
		if !ok {
			bodyLogAPIError(c, ErrNotFound)
			return
		}
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid usage id")
			return
		}
		detail, err := pg.Legacy(c.Request.Context(), id)
		if err != nil {
			bodyLogAPIError(c, err)
			return
		}
		response.Success(c, detail)
	})
}

func bodyLogAPIError(c *gin.Context, err error) {
	if errors.Is(err, ErrNotFound) {
		response.Error(c, 404, "Body log not found")
	} else {
		response.Error(c, 503, "Body log content could not be read")
	}
}

// Legacy reads the original BYTEA table without modifying old rows or migrations.
func (p *PostgresIndex) Legacy(ctx context.Context, usageID int64) (map[string]any, error) {
	var raw []byte
	var request, reply []byte
	err := p.DB.QueryRowContext(ctx, `SELECT to_jsonb(g) - 'request_body' - 'response_body',
 request_body,response_body FROM gateway_body_logs g WHERE usage_log_id=$1`, usageID).Scan(&raw, &request, &reply)
	if err != nil {
		return nil, ErrNotFound
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	result["request_body"], result["response_body"] = string(request), string(reply)
	// The original API exposes the enum as a string, while the legacy table
	// stores its numeric representation.
	if n, ok := result["request_type"].(float64); ok {
		names := map[int]string{1: "sync", 2: "stream", 3: "ws_v2", 4: "cyber", 5: "live"}
		result["request_type"] = names[int(n)]
		if result["request_type"] == "" {
			result["request_type"] = "unknown"
		}
	}
	return result, nil
}
