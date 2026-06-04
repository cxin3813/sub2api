//go:build unit

package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type adminUsageBodyLogRepoStub struct {
	service.UsageLogRepository
	usage *service.UsageLog
}

func (r *adminUsageBodyLogRepoStub) GetByID(ctx context.Context, id int64) (*service.UsageLog, error) {
	if r.usage != nil && r.usage.ID == id {
		return r.usage, nil
	}
	return nil, service.ErrUsageLogNotFound
}

func (r *adminUsageBodyLogRepoStub) ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters usagestats.UsageLogFilters) ([]service.UsageLog, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

type adminGatewayBodyLogRepoStub struct {
	service.GatewayBodyLogRepository
	log *service.GatewayBodyLog
}

func (r *adminGatewayBodyLogRepoStub) GetByUsageLogID(ctx context.Context, usageLogID int64) (*service.GatewayBodyLog, error) {
	if r.log != nil && r.log.UsageLogID == usageLogID {
		return r.log, nil
	}
	return nil, service.ErrGatewayBodyLogNotFound
}

func newAdminUsageBodyLogRouter(usage *service.UsageLog, bodyLog *service.GatewayBodyLog) *gin.Engine {
	gin.SetMode(gin.TestMode)
	usageSvc := service.NewUsageService(&adminUsageBodyLogRepoStub{usage: usage}, nil, nil, nil)
	bodySvc := service.NewGatewayBodyLogService(&adminGatewayBodyLogRepoStub{log: bodyLog}, nil)
	handler := NewUsageHandler(usageSvc, nil, nil, nil, bodySvc)
	router := gin.New()
	router.GET("/admin/usage/:id/body-log", handler.GetBodyLog)
	return router
}

func TestAdminUsageGetBodyLogReturnsDetail(t *testing.T) {
	usage := &service.UsageLog{ID: 42, RequestID: "req_42", APIKeyID: 11}
	bodyLog := &service.GatewayBodyLog{
		UsageLogID:         42,
		RequestID:          "req_42",
		APIKeyID:           11,
		RequestBody:        []byte(`{"prompt":"hi"}`),
		ResponseBody:       []byte(`{"text":"ok"}`),
		RequestBodyBytes:   15,
		ResponseBodyBytes:  13,
		RequestContentType: "application/json",
		StatusCode:         200,
		StorageKind:        "db",
	}
	router := newAdminUsageBodyLogRouter(usage, bodyLog)

	req := httptest.NewRequest(http.MethodGet, "/admin/usage/42/body-log", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var payload struct {
		Data struct {
			RequestID    string `json:"request_id"`
			RequestBody  string `json:"request_body"`
			ResponseBody string `json:"response_body"`
			StatusCode   int    `json:"status_code"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Equal(t, "req_42", payload.Data.RequestID)
	require.Equal(t, `{"prompt":"hi"}`, payload.Data.RequestBody)
	require.Equal(t, `{"text":"ok"}`, payload.Data.ResponseBody)
	require.Equal(t, 200, payload.Data.StatusCode)
}

func TestAdminUsageGetBodyLogReturns404WhenNotCaptured(t *testing.T) {
	router := newAdminUsageBodyLogRouter(&service.UsageLog{ID: 42, RequestID: "req_42", APIKeyID: 11}, nil)

	req := httptest.NewRequest(http.MethodGet, "/admin/usage/42/body-log", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}
