package bodylog

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

func TestLegacySettingsAdapterPreservesCoreFieldsAndSupportsPartialUpdates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := testManager(t, &memoryIndex{})
	router := gin.New()
	admin := router.Group("/api/v1/admin")
	m.RegisterAdmin(admin)
	admin.GET("/settings", func(c *gin.Context) {
		response.Success(c, gin.H{"site_name": "original"})
	})
	admin.PUT("/settings", func(c *gin.Context) {
		var input map[string]any
		if err := c.ShouldBindBodyWith(&input, binding.JSON); err != nil {
			t.Fatal("compatibility adapter consumed the core request", err)
		}
		if input["site_name"] == "reject" {
			response.BadRequest(c, "core validation failed")
			return
		}
		response.Success(c, gin.H{"site_name": input["site_name"]})
	})
	for _, tc := range []struct {
		method, body string
		status       int
	}{
		{"GET", "", 200},
		{"PUT", `{"site_name":"updated","gateway_body_log_capture_request":false,"gateway_body_log_max_bytes":0}`, 200},
		{"PUT", `{"site_name":"reject","gateway_body_log_capture_request":true}`, 400},
		{"PUT", `{"gateway_body_log_max_bytes":-1}`, 400},
	} {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, "/api/v1/admin/settings", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, req)
		if recorder.Code != tc.status {
			t.Fatalf("%s returned %d: %s", tc.body, recorder.Code, recorder.Body.String())
		}
		if recorder.Code == 200 {
			var result struct {
				Data map[string]any `json:"data"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Data["site_name"] == nil || result.Data["gateway_body_log_enabled"] != true ||
				result.Data["gateway_body_log_max_bytes"] != float64(0) {
				t.Fatalf("core or legacy fields lost: %+v", result.Data)
			}
		}
	}
	if m.Config().Request || !m.Config().Response || !m.Config().Enabled {
		t.Fatal("a rejected or partial core update changed unrelated capture fields")
	}
}

func TestMetadataCredentialsAreRedactedWithoutChangingHeadersOrQuery(t *testing.T) {
	headers := http.Header{"Authorization": {"Bearer secret"}, "X-Oai-Attestation": {"proof"}, "X-Request-Id": {"trace"}}
	safe := SafeHeaders(headers)
	if safe.Get("Authorization") != "[redacted]" || safe.Get("X-Oai-Attestation") != "[redacted]" || headers.Get("Authorization") != "Bearer secret" {
		t.Fatal("credential metadata leaked or business headers changed")
	}
	request := httptest.NewRequest("GET", "/v1/models?key=secret&model=example&X-Amz-Signature=proof", nil)
	query := SafeQuery(request.URL.Query())
	if query.Get("key") != "[redacted]" || query.Get("X-Amz-Signature") != "[redacted]" ||
		query.Get("model") != "example" || request.URL.Query().Get("key") != "secret" {
		t.Fatal("credential query leaked or business URL changed")
	}
}
