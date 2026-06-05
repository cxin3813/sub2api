//go:build unit

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type gatewayBodyLogRepoStub struct {
	upserted     *GatewayBodyLog
	byUsageLogID map[int64]*GatewayBodyLog
}

func (r *gatewayBodyLogRepoStub) Upsert(ctx context.Context, log *GatewayBodyLog) error {
	cp := *log
	r.upserted = &cp
	if r.byUsageLogID == nil {
		r.byUsageLogID = map[int64]*GatewayBodyLog{}
	}
	r.byUsageLogID[log.UsageLogID] = &cp
	return nil
}

func (r *gatewayBodyLogRepoStub) GetByUsageLogID(ctx context.Context, usageLogID int64) (*GatewayBodyLog, error) {
	if r.byUsageLogID != nil {
		if log := r.byUsageLogID[usageLogID]; log != nil {
			return log, nil
		}
	}
	return nil, ErrGatewayBodyLogNotFound
}

func (r *gatewayBodyLogRepoStub) DeleteBefore(ctx context.Context, before any) (int64, error) {
	return 0, nil
}

func TestGatewayBodyLogServiceCapture_DefaultDisabledSkipsWrite(t *testing.T) {
	repo := &gatewayBodyLogRepoStub{}
	settings := NewSettingService(&settingAntigravityUARepoStub{values: map[string]string{}}, &config.Config{})
	svc := NewGatewayBodyLogService(repo, settings)

	err := svc.Capture(context.Background(), GatewayBodyLogCaptureInput{
		UsageLog:      &UsageLog{ID: 1, RequestID: "req_1", APIKeyID: 7},
		RequestBody:   []byte(`{"hello":"request"}`),
		ResponseBody:  []byte(`{"hello":"response"}`),
		StatusCode:    200,
		RequestMethod: "POST",
		RequestPath:   "/v1/messages",
	})

	require.NoError(t, err)
	require.Nil(t, repo.upserted)
}

func TestGatewayBodyLogServiceCapture_TruncatesBodiesAndHashesOriginal(t *testing.T) {
	repo := &gatewayBodyLogRepoStub{}
	settings := NewSettingService(&settingAntigravityUARepoStub{values: map[string]string{
		SettingKeyGatewayBodyLogEnabled:         "true",
		SettingKeyGatewayBodyLogMaxBytes:        "5",
		SettingKeyGatewayBodyLogCaptureRequest:  "true",
		SettingKeyGatewayBodyLogCaptureResponse: "true",
	}}, &config.Config{})
	svc := NewGatewayBodyLogService(repo, settings)

	err := svc.Capture(context.Background(), GatewayBodyLogCaptureInput{
		UsageLog:            &UsageLog{ID: 22, RequestID: "req_2", APIKeyID: 9, UserID: 3, AccountID: 4},
		RequestBody:         []byte("request-body"),
		ResponseBody:        []byte("response-body"),
		RequestContentType:  "application/json",
		ResponseContentType: "text/event-stream",
		StatusCode:          200,
		RequestMethod:       "POST",
		RequestPath:         "/v1/chat/completions",
		InboundEndpoint:     gatewayBodyLogPtrString("/v1/chat/completions"),
		UpstreamEndpoint:    gatewayBodyLogPtrString("/v1/responses"),
		Platform:            "openai",
		Model:               "gpt-5.1",
		RequestType:         RequestTypeStream,
		Stream:              true,
		ClientRequestID:     "client-req",
		RequestHeaderJSON:   []byte(`{"x-test":["a"]}`),
		ResponseHeaderJSON:  []byte(`{"content-type":["text/event-stream"]}`),
	})

	require.NoError(t, err)
	require.NotNil(t, repo.upserted)
	require.Equal(t, int64(22), repo.upserted.UsageLogID)
	require.Equal(t, "req_2", repo.upserted.RequestID)
	require.Equal(t, int64(9), repo.upserted.APIKeyID)
	require.Equal(t, []byte("reque"), repo.upserted.RequestBody)
	require.Equal(t, []byte("respo"), repo.upserted.ResponseBody)
	require.True(t, repo.upserted.RequestTruncated)
	require.True(t, repo.upserted.ResponseTruncated)
	require.Equal(t, int64(len("request-body")), repo.upserted.RequestBodyBytes)
	require.Equal(t, int64(len("response-body")), repo.upserted.ResponseBodyBytes)
	require.NotEmpty(t, repo.upserted.RequestBodySHA256)
	require.NotEmpty(t, repo.upserted.ResponseBodySHA256)
	require.Equal(t, "text/event-stream", repo.upserted.ResponseContentType)
}

func TestGatewayBodyLogServiceCapture_UsesExplicitResponseCaptureMetadata(t *testing.T) {
	repo := &gatewayBodyLogRepoStub{}
	settings := NewSettingService(&settingAntigravityUARepoStub{values: map[string]string{
		SettingKeyGatewayBodyLogEnabled:         "true",
		SettingKeyGatewayBodyLogMaxBytes:        "5",
		SettingKeyGatewayBodyLogCaptureRequest:  "true",
		SettingKeyGatewayBodyLogCaptureResponse: "true",
	}}, &config.Config{})
	svc := NewGatewayBodyLogService(repo, settings)
	fullStream := []byte("data: first\n\ndata: second\n\n")
	sum := sha256.Sum256(fullStream)

	err := svc.Capture(context.Background(), GatewayBodyLogCaptureInput{
		UsageLog: &UsageLog{ID: 25, RequestID: "req_stream_capture", APIKeyID: 9},
		ResponseCapture: &GatewayBodyLogBodyCapture{
			Body:      []byte("data: first"),
			Bytes:     int64(len(fullStream)),
			SHA256:    hex.EncodeToString(sum[:]),
			Truncated: true,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, repo.upserted)
	require.Equal(t, []byte("data:"), repo.upserted.ResponseBody)
	require.Equal(t, int64(len(fullStream)), repo.upserted.ResponseBodyBytes)
	require.Equal(t, hex.EncodeToString(sum[:]), repo.upserted.ResponseBodySHA256)
	require.True(t, repo.upserted.ResponseTruncated)
	require.NotNil(t, repo.upserted.ResponseStoredAt)
}

func TestGatewayBodyLogServiceCapture_DisabledRequestClearsAllDerivedFields(t *testing.T) {
	repo := &gatewayBodyLogRepoStub{}
	settings := NewSettingService(&settingAntigravityUARepoStub{values: map[string]string{
		SettingKeyGatewayBodyLogEnabled:         "true",
		SettingKeyGatewayBodyLogMaxBytes:        "5",
		SettingKeyGatewayBodyLogCaptureRequest:  "false",
		SettingKeyGatewayBodyLogCaptureResponse: "true",
	}}, &config.Config{})
	svc := NewGatewayBodyLogService(repo, settings)

	err := svc.Capture(context.Background(), GatewayBodyLogCaptureInput{
		UsageLog:     &UsageLog{ID: 23, RequestID: "req_disabled_request", APIKeyID: 9},
		RequestBody:  []byte("request-body"),
		ResponseBody: []byte("response-body"),
	})

	require.NoError(t, err)
	require.NotNil(t, repo.upserted)
	require.Nil(t, repo.upserted.RequestBody)
	require.Zero(t, repo.upserted.RequestBodyBytes)
	require.Empty(t, repo.upserted.RequestBodySHA256)
	require.False(t, repo.upserted.RequestTruncated)
	require.Nil(t, repo.upserted.RequestStoredAt)
	require.Equal(t, []byte("respo"), repo.upserted.ResponseBody)
	require.Equal(t, int64(len("response-body")), repo.upserted.ResponseBodyBytes)
	require.NotEmpty(t, repo.upserted.ResponseBodySHA256)
	require.True(t, repo.upserted.ResponseTruncated)
	require.NotNil(t, repo.upserted.ResponseStoredAt)
}

func TestGatewayBodyLogServiceCapture_DisabledResponseClearsAllDerivedFields(t *testing.T) {
	repo := &gatewayBodyLogRepoStub{}
	settings := NewSettingService(&settingAntigravityUARepoStub{values: map[string]string{
		SettingKeyGatewayBodyLogEnabled:         "true",
		SettingKeyGatewayBodyLogMaxBytes:        "5",
		SettingKeyGatewayBodyLogCaptureRequest:  "true",
		SettingKeyGatewayBodyLogCaptureResponse: "false",
	}}, &config.Config{})
	svc := NewGatewayBodyLogService(repo, settings)

	err := svc.Capture(context.Background(), GatewayBodyLogCaptureInput{
		UsageLog:     &UsageLog{ID: 24, RequestID: "req_disabled_response", APIKeyID: 9},
		RequestBody:  []byte("request-body"),
		ResponseBody: []byte("response-body"),
	})

	require.NoError(t, err)
	require.NotNil(t, repo.upserted)
	require.Equal(t, []byte("reque"), repo.upserted.RequestBody)
	require.Equal(t, int64(len("request-body")), repo.upserted.RequestBodyBytes)
	require.NotEmpty(t, repo.upserted.RequestBodySHA256)
	require.True(t, repo.upserted.RequestTruncated)
	require.NotNil(t, repo.upserted.RequestStoredAt)
	require.Nil(t, repo.upserted.ResponseBody)
	require.Zero(t, repo.upserted.ResponseBodyBytes)
	require.Empty(t, repo.upserted.ResponseBodySHA256)
	require.False(t, repo.upserted.ResponseTruncated)
	require.Nil(t, repo.upserted.ResponseStoredAt)
}

func TestGatewayBodyLogServiceGetByUsageLogRequiresUsageID(t *testing.T) {
	repo := &gatewayBodyLogRepoStub{
		byUsageLogID: map[int64]*GatewayBodyLog{
			42: {UsageLogID: 42, RequestID: "req_42", APIKeyID: 11},
		},
	}
	svc := NewGatewayBodyLogService(repo, nil)

	got, err := svc.GetByUsageLog(context.Background(), &UsageLog{ID: 42, RequestID: "ignored", APIKeyID: 11})

	require.NoError(t, err)
	require.Equal(t, int64(42), got.UsageLogID)
}

func TestGatewayBodyLogServiceGetByUsageLogRejectsMissingUsageID(t *testing.T) {
	svc := NewGatewayBodyLogService(&gatewayBodyLogRepoStub{}, nil)

	_, err := svc.GetByUsageLog(context.Background(), &UsageLog{APIKeyID: 1})

	require.ErrorIs(t, err, ErrGatewayBodyLogNotFound)
}

func gatewayBodyLogPtrString(v string) *string { return &v }
