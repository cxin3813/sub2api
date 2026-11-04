package server

import (
	"github.com/Wei-Shaw/sub2api/internal/bodylog"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func enrichBodyLog(c *gin.Context, s *bodylog.Session) {
	key, _ := c.Get(string(middleware.ContextKeyAPIKey))
	apiKey, _ := key.(*service.APIKey)
	if apiKey == nil {
		return
	}
	s.Update(func(r *bodylog.Record) {
		r.APIKeyID, r.UserID, r.GroupID = apiKey.ID, apiKey.UserID, apiKey.GroupID
		if apiKey.Group != nil {
			r.Platform = apiKey.Group.Platform
		}
	})
}
