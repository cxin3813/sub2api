package bodylog

import (
	"context"
	"time"
)

type taskIndex interface {
	BindTask(context.Context, string, string, string) error
	TaskParent(context.Context, string, string) (string, error)
}

func (s *Session) BindTask(ctx context.Context, kind, id string) {
	if s == nil {
		return
	}
	index, ok := s.manager.index.(taskIndex)
	if !ok {
		return
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	s.manager.failure(index.BindTask(writeCtx, kind, id, s.record.ID))
}

// ResumeTask recovers association from the extension's own table. It does not
// change the upstream task payload or the business queue's persistence contract.
func (m *Manager) ResumeTask(ctx context.Context, kind, id string) (context.Context, *Session) {
	if m == nil || !m.Config().Enabled {
		return ctx, nil
	}
	s := m.Begin(nil, "background_task", kind, id)
	if s == nil {
		return ctx, nil
	}
	s.Update(func(r *Record) { r.Metadata["task_id"] = id })
	if index, ok := m.index.(taskIndex); ok {
		readCtx, cancel := context.WithTimeout(ctx, time.Second)
		parent, err := index.TaskParent(readCtx, kind, id)
		cancel()
		if err == nil {
			s.Update(func(r *Record) { r.ParentID = parent })
			if original, getErr := m.Get(ctx, parent); getErr == nil {
				s.Update(func(r *Record) {
					r.UserID, r.APIKeyID, r.GroupID = original.UserID, original.APIKeyID, original.GroupID
					r.Platform = original.Platform
				})
			}
		} else {
			s.Update(func(r *Record) { r.Metadata["parent_state"] = "unavailable" })
		}
	}
	return WithSession(ctx, s), s
}
