package repository

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/bodylog"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func ProvideBodyLog(db *sql.DB, cfg *config.Config) (*bodylog.Manager, error) {
	index := &bodylog.PostgresIndex{DB: db}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := index.Init(ctx); err != nil {
		return nil, err
	}
	settings, err := index.LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	rootPath := os.Getenv("BODY_LOG_DIR")
	if rootPath == "" {
		rootPath = filepath.Join(cfg.Pricing.DataDir, "bodylogs")
	}
	root, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, err
	}
	return bodylog.NewManager(root, index, settings)
}

type observedHTTPUpstream struct{ service.HTTPUpstream }

func ProvideObservedHTTPUpstream(cfg *config.Config) service.HTTPUpstream {
	return &observedHTTPUpstream{HTTPUpstream: NewHTTPUpstream(cfg)}
}

func (u *observedHTTPUpstream) Do(req *http.Request, proxy string, accountID int64, concurrency int) (*http.Response, error) {
	return bodylog.ObserveHTTP(req, accountID, func(r *http.Request) (*http.Response, error) {
		return u.HTTPUpstream.Do(r, proxy, accountID, concurrency)
	})
}

func (u *observedHTTPUpstream) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return bodylog.ObserveHTTP(req, accountID, func(r *http.Request) (*http.Response, error) {
		return u.HTTPUpstream.DoWithTLS(r, proxy, accountID, concurrency, profile)
	})
}

type observedUsageLogRepository struct {
	*usageLogRepository
	db *sql.DB
}

func ProvideObservedUsageLogRepository(client *ent.Client, db *sql.DB) service.UsageLogRepository {
	return &observedUsageLogRepository{usageLogRepository: newUsageLogRepositoryWithSQL(client, db), db: db}
}

type observedBatchImageRepository struct{ service.BatchImageRepository }

func ProvideObservedBatchImageRepository(db *sql.DB) service.BatchImageRepository {
	return &observedBatchImageRepository{BatchImageRepository: NewBatchImageRepository(db)}
}

func (r *observedBatchImageRepository) CreateBatchImageJob(ctx context.Context, params service.CreateBatchImageJobParams) (*service.BatchImageJob, error) {
	job, err := r.BatchImageRepository.CreateBatchImageJob(ctx, params)
	if err == nil && job != nil {
		bodylog.FromContext(ctx).BindTask(ctx, "batch_image", job.BatchID)
	}
	return job, err
}

func (r *observedUsageLogRepository) Create(ctx context.Context, log *service.UsageLog) (bool, error) {
	created, err := r.usageLogRepository.Create(ctx, log)
	if err == nil && log != nil {
		r.link(ctx, log)
	}
	return created, err
}

func (r *observedUsageLogRepository) CreateBestEffort(ctx context.Context, log *service.UsageLog) error {
	err := r.usageLogRepository.CreateBestEffort(ctx, log)
	if err == nil && log != nil {
		r.link(ctx, log)
	}
	return err
}

func (r *observedUsageLogRepository) link(ctx context.Context, log *service.UsageLog) {
	s := bodylog.FromContext(ctx)
	if s == nil {
		return
	}
	id := log.ID
	if id <= 0 && log.RequestID != "" && r.db != nil {
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		err := r.db.QueryRowContext(readCtx, `SELECT id FROM usage_logs WHERE request_id=$1 AND api_key_id=$2`, log.RequestID, log.APIKeyID).Scan(&id)
		cancel()
		if err != nil {
			s.Update(func(record *bodylog.Record) { record.Metadata["usage_link_state"] = "unavailable" })
			return
		}
	}
	s.AddUsage(id)
}
