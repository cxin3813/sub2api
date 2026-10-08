//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestForwardSystemOneRecordUsageCapturesBodyLog(t *testing.T) {
	requestBody := []byte(`{"model":"jev-latest","state":"sample","questions":{"q":{"type":"noul"}}}`)
	responseBody := []byte(`{"answers":{"q":{"type":"noul","answer":"ok"}},"usage":{}}`)
	account := &Account{ID: 7, Platform: PlatformTypeSafe, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "http://typesafe.test", "api_key": "ts-secret"}}
	upstream := &systemOneHTTPUpstream{do: func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"req-systemone-body-log"}},
			Body:       io.NopCloser(strings.NewReader(string(responseBody))),
		}, nil
	}}
	result, err := newSystemOneTestService(upstream).ForwardSystemOne(context.Background(), newSystemOneTestContext(), account, requestBody)
	require.NoError(t, err)

	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	bodyLogRepo := &recordUsageGatewayBodyLogRepoStub{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
	svc.SetGatewayBodyLogService(newEnabledGatewayBodyLogServiceForTest(bodyLogRepo))
	err = svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result:             &result.ForwardResult,
		APIKey:             &APIKey{ID: 501, Quota: 100, Group: &Group{RateMultiplier: 1}},
		User:               &User{ID: 601},
		Account:            account,
		InboundEndpoint:    "/v1/systemone",
		UpstreamEndpoint:   "/v1/systemone",
		RequestMethod:      http.MethodPost,
		RequestPath:        "/v1/systemone",
		RequestContentType: "application/json",
		APIKeyService:      &openAIRecordUsageAPIKeyQuotaStub{},
	})
	require.NoError(t, err)
	require.Equal(t, 1, bodyLogRepo.calls)
	require.Equal(t, usageRepo.lastLog.ID, bodyLogRepo.lastLog.UsageLogID)
	require.Equal(t, PlatformTypeSafe, bodyLogRepo.lastLog.Platform)
	require.Equal(t, "/v1/systemone", bodyLogRepo.lastLog.RequestPath)
	require.Equal(t, requestBody, bodyLogRepo.lastLog.RequestBody)
	require.Equal(t, responseBody, bodyLogRepo.lastLog.ResponseBody)
	require.Equal(t, http.StatusOK, bodyLogRepo.lastLog.StatusCode)
	require.Equal(t, "application/json", bodyLogRepo.lastLog.ResponseContentType)
	require.JSONEq(t, `{"content-type":["application/json"],"x-request-id":["req-systemone-body-log"]}`, string(bodyLogRepo.lastLog.ResponseHeaderJSON))
}
