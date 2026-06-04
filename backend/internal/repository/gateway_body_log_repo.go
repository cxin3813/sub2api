package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type gatewayBodyLogRepository struct {
	sql sqlExecutor
}

func NewGatewayBodyLogRepository(_ *dbent.Client, sqlDB *sql.DB) service.GatewayBodyLogRepository {
	return &gatewayBodyLogRepository{sql: sqlDB}
}

func (r *gatewayBodyLogRepository) Upsert(ctx context.Context, log *service.GatewayBodyLog) error {
	if r == nil || r.sql == nil || log == nil {
		return nil
	}

	sqlq := r.sql
	if tx := dbent.TxFromContext(ctx); tx != nil {
		sqlq = tx
	}

	createdAt := log.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	requestType := int16(log.RequestType.Normalize())

	query := `
		INSERT INTO gateway_body_logs (
			usage_log_id,
			request_id,
			api_key_id,
			user_id,
			account_id,
			platform,
			model,
			request_type,
			stream,
			request_method,
			request_path,
			inbound_endpoint,
			upstream_endpoint,
			client_request_id,
			request_content_type,
			response_content_type,
			status_code,
			request_headers,
			response_headers,
			request_body,
			response_body,
			request_body_bytes,
			response_body_bytes,
			request_body_sha256,
			response_body_sha256,
			request_truncated,
			response_truncated,
			storage_kind,
			request_stored_at,
			response_stored_at,
			created_at,
			updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
			$11, $12, $13, $14, $15, $16, $17, $18, $19, $20,
			$21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, NOW()
		)
		ON CONFLICT (usage_log_id) DO UPDATE SET
			request_id = EXCLUDED.request_id,
			api_key_id = EXCLUDED.api_key_id,
			user_id = EXCLUDED.user_id,
			account_id = EXCLUDED.account_id,
			platform = EXCLUDED.platform,
			model = EXCLUDED.model,
			request_type = EXCLUDED.request_type,
			stream = EXCLUDED.stream,
			request_method = EXCLUDED.request_method,
			request_path = EXCLUDED.request_path,
			inbound_endpoint = EXCLUDED.inbound_endpoint,
			upstream_endpoint = EXCLUDED.upstream_endpoint,
			client_request_id = EXCLUDED.client_request_id,
			request_content_type = EXCLUDED.request_content_type,
			response_content_type = EXCLUDED.response_content_type,
			status_code = EXCLUDED.status_code,
			request_headers = EXCLUDED.request_headers,
			response_headers = EXCLUDED.response_headers,
			request_body = EXCLUDED.request_body,
			response_body = EXCLUDED.response_body,
			request_body_bytes = EXCLUDED.request_body_bytes,
			response_body_bytes = EXCLUDED.response_body_bytes,
			request_body_sha256 = EXCLUDED.request_body_sha256,
			response_body_sha256 = EXCLUDED.response_body_sha256,
			request_truncated = EXCLUDED.request_truncated,
			response_truncated = EXCLUDED.response_truncated,
			storage_kind = EXCLUDED.storage_kind,
			request_stored_at = EXCLUDED.request_stored_at,
			response_stored_at = EXCLUDED.response_stored_at,
			updated_at = NOW()
		RETURNING id, created_at, updated_at
	`

	return scanSingleRow(ctx, sqlq, query, []any{
		log.UsageLogID,
		log.RequestID,
		log.APIKeyID,
		nullableInt64(log.UserID),
		nullableInt64(log.AccountID),
		nullableString(log.Platform),
		nullableString(log.Model),
		requestType,
		log.Stream,
		nullableString(log.RequestMethod),
		nullableString(log.RequestPath),
		nullString(log.InboundEndpoint),
		nullString(log.UpstreamEndpoint),
		nullableString(log.ClientRequestID),
		nullableString(log.RequestContentType),
		nullableString(log.ResponseContentType),
		log.StatusCode,
		nullBytes(log.RequestHeaderJSON),
		nullBytes(log.ResponseHeaderJSON),
		nullBytes(log.RequestBody),
		nullBytes(log.ResponseBody),
		log.RequestBodyBytes,
		log.ResponseBodyBytes,
		nullableString(log.RequestBodySHA256),
		nullableString(log.ResponseBodySHA256),
		log.RequestTruncated,
		log.ResponseTruncated,
		nullableString(log.StorageKind),
		nullTime(log.RequestStoredAt),
		nullTime(log.ResponseStoredAt),
		createdAt,
	}, &log.ID, &log.CreatedAt, &log.UpdatedAt)
}

func (r *gatewayBodyLogRepository) GetByUsageLogID(ctx context.Context, usageLogID int64) (*service.GatewayBodyLog, error) {
	if r == nil || r.sql == nil {
		return nil, service.ErrGatewayBodyLogNotFound
	}

	query := `
		SELECT
			id,
			usage_log_id,
			request_id,
			api_key_id,
			user_id,
			account_id,
			platform,
			model,
			request_type,
			stream,
			request_method,
			request_path,
			inbound_endpoint,
			upstream_endpoint,
			client_request_id,
			request_content_type,
			response_content_type,
			status_code,
			request_headers,
			response_headers,
			request_body,
			response_body,
			request_body_bytes,
			response_body_bytes,
			request_body_sha256,
			response_body_sha256,
			request_truncated,
			response_truncated,
			storage_kind,
			request_stored_at,
			response_stored_at,
			created_at,
			updated_at
		FROM gateway_body_logs
		WHERE usage_log_id = $1
	`

	var (
		log                 service.GatewayBodyLog
		userID              sql.NullInt64
		accountID           sql.NullInt64
		platform            sql.NullString
		model               sql.NullString
		requestType         int16
		requestMethod       sql.NullString
		requestPath         sql.NullString
		inboundEndpoint     sql.NullString
		upstreamEndpoint    sql.NullString
		clientRequestID     sql.NullString
		requestContentType  sql.NullString
		responseContentType sql.NullString
		requestHeaders      []byte
		responseHeaders     []byte
		requestBody         []byte
		responseBody        []byte
		requestBodySHA256   sql.NullString
		responseBodySHA256  sql.NullString
		storageKind         sql.NullString
		requestStoredAt     sql.NullTime
		responseStoredAt    sql.NullTime
	)

	err := scanSingleRow(ctx, r.sql, query, []any{usageLogID},
		&log.ID,
		&log.UsageLogID,
		&log.RequestID,
		&log.APIKeyID,
		&userID,
		&accountID,
		&platform,
		&model,
		&requestType,
		&log.Stream,
		&requestMethod,
		&requestPath,
		&inboundEndpoint,
		&upstreamEndpoint,
		&clientRequestID,
		&requestContentType,
		&responseContentType,
		&log.StatusCode,
		&requestHeaders,
		&responseHeaders,
		&requestBody,
		&responseBody,
		&log.RequestBodyBytes,
		&log.ResponseBodyBytes,
		&requestBodySHA256,
		&responseBodySHA256,
		&log.RequestTruncated,
		&log.ResponseTruncated,
		&storageKind,
		&requestStoredAt,
		&responseStoredAt,
		&log.CreatedAt,
		&log.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrGatewayBodyLogNotFound
		}
		return nil, err
	}

	log.UserID = nullableInt64Value(userID)
	log.AccountID = nullableInt64Value(accountID)
	log.Platform = nullableStringValue(platform)
	log.Model = nullableStringValue(model)
	log.RequestType = service.RequestTypeFromInt16(requestType)
	log.RequestMethod = nullableStringValue(requestMethod)
	log.RequestPath = nullableStringValue(requestPath)
	log.InboundEndpoint = nullableStringPtr(inboundEndpoint)
	log.UpstreamEndpoint = nullableStringPtr(upstreamEndpoint)
	log.ClientRequestID = nullableStringValue(clientRequestID)
	log.RequestContentType = nullableStringValue(requestContentType)
	log.ResponseContentType = nullableStringValue(responseContentType)
	log.RequestHeaderJSON = append([]byte(nil), requestHeaders...)
	log.ResponseHeaderJSON = append([]byte(nil), responseHeaders...)
	log.RequestBody = append([]byte(nil), requestBody...)
	log.ResponseBody = append([]byte(nil), responseBody...)
	log.RequestBodySHA256 = nullableStringValue(requestBodySHA256)
	log.ResponseBodySHA256 = nullableStringValue(responseBodySHA256)
	log.StorageKind = nullableStringValue(storageKind)
	log.RequestStoredAt = nullableTimePtr(requestStoredAt)
	log.ResponseStoredAt = nullableTimePtr(responseStoredAt)
	return &log, nil
}

func (r *gatewayBodyLogRepository) DeleteBefore(ctx context.Context, before any) (int64, error) {
	if r == nil || r.sql == nil {
		return 0, nil
	}
	result, err := r.sql.ExecContext(ctx, `DELETE FROM gateway_body_logs WHERE created_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func nullableInt64(v int64) sql.NullInt64 {
	if v == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: v, Valid: true}
}

func nullableString(v string) sql.NullString {
	if v == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: v, Valid: true}
}

func nullBytes(v []byte) any {
	if len(v) == 0 {
		return nil
	}
	return v
}

func nullTime(v *time.Time) sql.NullTime {
	if v == nil || v.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *v, Valid: true}
}

func nullableInt64Value(v sql.NullInt64) int64 {
	if !v.Valid {
		return 0
	}
	return v.Int64
}

func nullableStringValue(v sql.NullString) string {
	if !v.Valid {
		return ""
	}
	return v.String
}

func nullableStringPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	value := v.String
	return &value
}

func nullableTimePtr(v sql.NullTime) *time.Time {
	if !v.Valid {
		return nil
	}
	value := v.Time
	return &value
}
