package bodylog

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAuthenticatedRawPagingAndOneUseDownloadPreserveCompleteBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := testManager(t, nil)
	s := m.Begin(nil, "gateway_http", "POST", "/v1/responses")
	sink := s.Body("client_response", "text/plain", "", nil)
	content := []byte(strings.Repeat("完整正文", 100000))
	_, _ = sink.Write(content)
	sink.Finish(true)
	s.End(200, nil)
	m.syncRecord(s.record.ID)
	row, err := m.Get(t.Context(), s.record.ID)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	v1 := router.Group("/api/v1")
	m.RegisterDownload(v1)
	admin := v1.Group("/admin")
	admin.Use(func(c *gin.Context) {
		if c.GetHeader("X-Test-Admin") != "true" {
			c.AbortWithStatus(401)
			return
		}
		c.Next()
	})
	m.RegisterAdmin(admin)
	body := row.Bodies[0].ID
	base := "/api/v1/admin/body-logs/" + row.ID
	perform := func(method, path string, authenticated bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if authenticated {
			req.Header.Set("X-Test-Admin", "true")
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		return recorder
	}
	if result := perform("GET", base+"/bodies/"+body, false); result.Code != 401 {
		t.Fatal("body access bypassed the admin group")
	}
	page := perform("GET", base+"/bodies/"+body+"?limit=5", true)
	var result struct {
		Data struct {
			Content string `json:"content"`
			Next    int    `json:"next_offset"`
			More    bool   `json:"has_more"`
		} `json:"data"`
	}
	if err := json.Unmarshal(page.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if page.Code != 200 || result.Data.Content != "完" || result.Data.Next != 3 || !result.Data.More {
		t.Fatalf("raw paging split UTF-8 or hid the remaining body: %s", page.Body.String())
	}
	issued := perform("POST", base+"/download/"+body, true)
	var ticket struct {
		Data struct {
			Path string `json:"path"`
		} `json:"data"`
	}
	if err := json.Unmarshal(issued.Body.Bytes(), &ticket); err != nil || ticket.Data.Path == "" {
		t.Fatalf("download ticket unavailable: %s %v", issued.Body.String(), err)
	}
	download := perform("GET", "/api/v1"+ticket.Data.Path, false)
	if download.Code != 200 || !bytes.Equal(download.Body.Bytes(), content) || download.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("download truncated or changed the complete archived body")
	}
	if reused := perform(http.MethodGet, "/api/v1"+ticket.Data.Path, false); reused.Code != 404 {
		t.Fatal("download capability could be reused")
	}
}
