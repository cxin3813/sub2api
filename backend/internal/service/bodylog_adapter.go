package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/bodylog"
	openaiwsv2 "github.com/Wei-Shaw/sub2api/internal/service/openai_ws_v2"
	coderws "github.com/coder/websocket"
)

type observedBatchImageProcessor struct {
	base BatchImageProcessor
	logs *bodylog.Manager
}

func (s *OpenAIGatewayService) SetBodyLogManager(logs *bodylog.Manager) {
	s.bodyLogs = logs
}

func (s *OpenAIGatewayService) liveBodyLogContext(ctx context.Context, record *LiveCallRecord) (context.Context, *bodylog.Session) {
	ctx, capture := s.bodyLogs.ResumeTask(ctx, "live", record.CallHash)
	capture.Update(func(r *bodylog.Record) {
		r.UserID, r.APIKeyID, r.AccountID = record.UserID, record.APIKeyID, record.AccountID
		r.GroupID, r.Model, r.Platform = liveOptionalID(record.GroupID), record.Model, PlatformOpenAI
	})
	return ctx, capture
}

func (p *observedBatchImageProcessor) Process(ctx context.Context, id string) (BatchImageProcessResult, error) {
	scoped, s := p.logs.ResumeTask(ctx, "batch_image", id)
	result, err := p.base.Process(scoped, id)
	s.End(0, err)
	return result, err
}

// Only direct frame relays use this wrapper. Pooled connections keep their
// original optional capability interfaces and observe at the lease boundary.
type bodyLogScopedFrames struct {
	openaiwsv2.FrameConn
	scope   context.Context
	session *bodylog.Session
}

func (c *bodyLogScopedFrames) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	return c.FrameConn.ReadFrame(bodylog.CopyContext(c.scope, ctx))
}

func (c *bodyLogScopedFrames) WriteFrame(ctx context.Context, kind coderws.MessageType, payload []byte) error {
	return c.FrameConn.WriteFrame(bodylog.CopyContext(c.scope, ctx), kind, payload)
}

func (c *bodyLogScopedFrames) Close() error {
	err := c.FrameConn.Close()
	c.session.End(101, err)
	return err
}
