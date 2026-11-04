package bodylog

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

type legacySettings struct {
	Enabled  *bool  `json:"gateway_body_log_enabled"`
	Request  *bool  `json:"gateway_body_log_capture_request"`
	Response *bool  `json:"gateway_body_log_capture_response"`
	Limit    *int64 `json:"gateway_body_log_max_bytes"`
}

// legacySettingsCompatibility keeps existing automation usable while the
// extension's settings and implementation stay outside the core settings DTOs.
// This is limited to the existing non-streaming settings JSON endpoint.
func (m *Manager) legacySettingsCompatibility() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.FullPath() != "/api/v1/admin/settings" || (c.Request.Method != http.MethodGet && c.Request.Method != http.MethodPut) {
			c.Next()
			return
		}
		var update legacySettings
		cfg := m.Config()
		changed := false
		if c.Request.Method == http.MethodPut {
			if err := c.ShouldBindBodyWith(&update, binding.JSON); err != nil {
				response.BadRequest(c, "Invalid body log settings")
				c.Abort()
				return
			}
			for target, value := range map[*bool]*bool{&cfg.Enabled: update.Enabled, &cfg.Request: update.Request, &cfg.Response: update.Response} {
				if value != nil {
					*target, changed = *value, true
				}
			}
			if update.Limit != nil {
				cfg.Limit, changed = *update.Limit, true
			}
			if err := cfg.Validate(); err != nil {
				response.BadRequest(c, err.Error())
				c.Abort()
				return
			}
		}
		original := c.Writer
		buffer := &legacyJSONWriter{ResponseWriter: original, status: 200, size: -1}
		c.Writer = buffer
		defer func() { c.Writer = original }()
		c.Next()
		c.Writer = original
		if buffer.status >= 200 && buffer.status < 300 {
			if changed {
				if err := m.Configure(c.Request.Context(), cfg); err != nil {
					response.Error(c, 503, "Body log settings could not be saved")
					return
				}
			}
			var envelope map[string]json.RawMessage
			if json.Unmarshal(buffer.Bytes(), &envelope) == nil {
				var data map[string]any
				if json.Unmarshal(envelope["data"], &data) == nil && data != nil {
					current := m.Config()
					data["gateway_body_log_enabled"] = current.Enabled
					data["gateway_body_log_capture_request"] = current.Request
					data["gateway_body_log_capture_response"] = current.Response
					data["gateway_body_log_max_bytes"] = current.Limit
					envelope["data"], _ = json.Marshal(data)
					raw, err := json.Marshal(envelope)
					if err == nil {
						buffer.Buffer.Reset()
						_, _ = buffer.Buffer.Write(raw)
					}
				}
			}
		}
		original.Header().Del("Content-Length")
		original.WriteHeader(buffer.status)
		_, _ = original.Write(buffer.Bytes())
	}
}

type legacyJSONWriter struct {
	gin.ResponseWriter
	bytes.Buffer
	status int
	size   int
}

func (w *legacyJSONWriter) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	w.size = max(0, w.size) + n
	return n, err
}
func (w *legacyJSONWriter) WriteString(p string) (int, error) { return w.Write([]byte(p)) }
func (w *legacyJSONWriter) WriteHeader(status int)            { w.status = status }
func (w *legacyJSONWriter) WriteHeaderNow()                   { w.size = max(0, w.size) }
func (w *legacyJSONWriter) Status() int                       { return w.status }
func (w *legacyJSONWriter) Size() int                         { return w.size }
func (w *legacyJSONWriter) Written() bool                     { return w.size >= 0 }
