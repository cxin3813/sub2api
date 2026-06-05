package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	GatewayBodyLogDefaultMaxBytes = 256 * 1024
	GatewayBodyLogMaxBytesLimit   = 1024 * 1024
	GatewayBodyLogStorageDB       = "db"
)

var ErrGatewayBodyLogNotFound = infraerrors.NotFound("GATEWAY_BODY_LOG_NOT_FOUND", "gateway body log not found")

type GatewayBodyLog struct {
	ID                  int64
	UsageLogID          int64
	RequestID           string
	APIKeyID            int64
	UserID              int64
	AccountID           int64
	Platform            string
	Model               string
	RequestType         RequestType
	Stream              bool
	RequestMethod       string
	RequestPath         string
	InboundEndpoint     *string
	UpstreamEndpoint    *string
	ClientRequestID     string
	RequestContentType  string
	ResponseContentType string
	StatusCode          int
	RequestHeaderJSON   []byte
	ResponseHeaderJSON  []byte
	RequestBody         []byte
	ResponseBody        []byte
	RequestBodyBytes    int64
	ResponseBodyBytes   int64
	RequestBodySHA256   string
	ResponseBodySHA256  string
	RequestTruncated    bool
	ResponseTruncated   bool
	StorageKind         string
	RequestStoredAt     *time.Time
	ResponseStoredAt    *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type GatewayBodyLogRepository interface {
	Upsert(ctx context.Context, log *GatewayBodyLog) error
	GetByUsageLogID(ctx context.Context, usageLogID int64) (*GatewayBodyLog, error)
	DeleteBefore(ctx context.Context, before any) (int64, error)
}

type GatewayBodyLogSettings struct {
	Enabled         bool
	MaxBytes        int
	CaptureRequest  bool
	CaptureResponse bool
}

type GatewayBodyLogCaptureInput struct {
	UsageLog            *UsageLog
	RequestBody         []byte
	ResponseBody        []byte
	ResponseCapture     *GatewayBodyLogBodyCapture
	RequestContentType  string
	ResponseContentType string
	StatusCode          int
	RequestMethod       string
	RequestPath         string
	InboundEndpoint     *string
	UpstreamEndpoint    *string
	Platform            string
	Model               string
	RequestType         RequestType
	Stream              bool
	ClientRequestID     string
	RequestHeaderJSON   []byte
	ResponseHeaderJSON  []byte
}

type GatewayBodyLogBodyCapture struct {
	Body      []byte
	Bytes     int64
	SHA256    string
	Truncated bool
}

type GatewayBodyLogService struct {
	repo     GatewayBodyLogRepository
	settings *SettingService
}

func NewGatewayBodyLogService(repo GatewayBodyLogRepository, settings *SettingService) *GatewayBodyLogService {
	return &GatewayBodyLogService{repo: repo, settings: settings}
}

func (s *GatewayBodyLogService) Enabled(ctx context.Context) (bool, error) {
	if s == nil || s.repo == nil {
		return false, nil
	}
	settings := defaultGatewayBodyLogSettings()
	var err error
	if s.settings != nil {
		settings, err = s.settings.GetGatewayBodyLogSettings(ctx)
		if err != nil {
			return false, err
		}
	}
	return settings.Enabled, nil
}

func (s *GatewayBodyLogService) Capture(ctx context.Context, input GatewayBodyLogCaptureInput) error {
	if s == nil || s.repo == nil || input.UsageLog == nil || input.UsageLog.ID <= 0 {
		return nil
	}
	settings := defaultGatewayBodyLogSettings()
	var err error
	if s.settings != nil {
		settings, err = s.settings.GetGatewayBodyLogSettings(ctx)
		if err != nil {
			return err
		}
	}
	if !settings.Enabled {
		return nil
	}

	log := &GatewayBodyLog{
		UsageLogID:          input.UsageLog.ID,
		RequestID:           input.UsageLog.RequestID,
		APIKeyID:            input.UsageLog.APIKeyID,
		UserID:              input.UsageLog.UserID,
		AccountID:           input.UsageLog.AccountID,
		Platform:            input.Platform,
		Model:               input.Model,
		RequestType:         input.RequestType.Normalize(),
		Stream:              input.Stream,
		RequestMethod:       input.RequestMethod,
		RequestPath:         input.RequestPath,
		InboundEndpoint:     input.InboundEndpoint,
		UpstreamEndpoint:    input.UpstreamEndpoint,
		ClientRequestID:     input.ClientRequestID,
		RequestContentType:  input.RequestContentType,
		ResponseContentType: input.ResponseContentType,
		StatusCode:          input.StatusCode,
		RequestHeaderJSON:   cloneBytes(input.RequestHeaderJSON),
		ResponseHeaderJSON:  cloneBytes(input.ResponseHeaderJSON),
		StorageKind:         GatewayBodyLogStorageDB,
		RequestStoredAt:     nil,
		ResponseStoredAt:    nil,
	}
	if log.RequestType == RequestTypeUnknown {
		log.RequestType = input.UsageLog.EffectiveRequestType()
	}
	if log.Stream == false {
		log.Stream = input.UsageLog.Stream
	}
	if log.Model == "" {
		log.Model = input.UsageLog.Model
	}
	if log.InboundEndpoint == nil {
		log.InboundEndpoint = input.UsageLog.InboundEndpoint
	}
	if log.UpstreamEndpoint == nil {
		log.UpstreamEndpoint = input.UsageLog.UpstreamEndpoint
	}
	if settings.CaptureRequest {
		log.RequestBodyBytes = int64(len(input.RequestBody))
		log.RequestBodySHA256 = sha256Hex(input.RequestBody)
		log.RequestBody, log.RequestTruncated = truncateBody(input.RequestBody, settings.MaxBytes)
		now := time.Now()
		log.RequestStoredAt = &now
	}
	if settings.CaptureResponse {
		if input.ResponseCapture != nil {
			log.ResponseBodyBytes = input.ResponseCapture.Bytes
			log.ResponseBodySHA256 = input.ResponseCapture.SHA256
			log.ResponseBody, log.ResponseTruncated = truncateBody(input.ResponseCapture.Body, settings.MaxBytes)
			log.ResponseTruncated = log.ResponseTruncated || input.ResponseCapture.Truncated
		} else {
			log.ResponseBodyBytes = int64(len(input.ResponseBody))
			log.ResponseBodySHA256 = sha256Hex(input.ResponseBody)
			log.ResponseBody, log.ResponseTruncated = truncateBody(input.ResponseBody, settings.MaxBytes)
		}
		now := time.Now()
		log.ResponseStoredAt = &now
	}
	return s.repo.Upsert(ctx, log)
}

func (s *GatewayBodyLogService) GetByUsageLog(ctx context.Context, usageLog *UsageLog) (*GatewayBodyLog, error) {
	if s == nil || s.repo == nil || usageLog == nil || usageLog.ID <= 0 {
		return nil, ErrGatewayBodyLogNotFound
	}
	return s.repo.GetByUsageLogID(ctx, usageLog.ID)
}

func defaultGatewayBodyLogSettings() GatewayBodyLogSettings {
	return GatewayBodyLogSettings{
		Enabled:         false,
		MaxBytes:        GatewayBodyLogDefaultMaxBytes,
		CaptureRequest:  true,
		CaptureResponse: true,
	}
}

func clampGatewayBodyLogMaxBytes(value int) int {
	if value <= 0 {
		return GatewayBodyLogDefaultMaxBytes
	}
	if value > GatewayBodyLogMaxBytesLimit {
		return GatewayBodyLogMaxBytesLimit
	}
	return value
}

func truncateBody(body []byte, maxBytes int) ([]byte, bool) {
	maxBytes = clampGatewayBodyLogMaxBytes(maxBytes)
	if len(body) <= maxBytes {
		return cloneBytes(body), false
	}
	return cloneBytes(body[:maxBytes]), true
}

func cloneBytes(src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}

func sha256Hex(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func MarshalGatewayBodyLogHeaders(headers http.Header) []byte {
	if len(headers) == 0 {
		return nil
	}

	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	sanitized := make(map[string][]string, len(keys))
	for _, key := range keys {
		values := headers[key]
		if len(values) == 0 {
			continue
		}
		out := make([]string, 0, len(values))
		for _, value := range values {
			out = append(out, safeHeaderValueForLog(key, value))
		}
		normalizedKey := strings.ToLower(strings.TrimSpace(key))
		sanitized[normalizedKey] = append(sanitized[normalizedKey], out...)
	}

	raw, err := json.Marshal(sanitized)
	if err != nil {
		return nil
	}
	return raw
}
