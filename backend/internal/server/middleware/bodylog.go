package middleware

import (
	"github.com/Wei-Shaw/sub2api/internal/bodylog"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Record the loaded identity before admission can reject the request. This
// does not mark the request authenticated or change any admission decision.
func observeBodyLogIdentity(c *gin.Context, key *service.APIKey) {
	if c.Request == nil {
		return
	}
	bodylog.FromContext(c.Request.Context()).Update(func(r *bodylog.Record) {
		r.UserID, r.APIKeyID, r.GroupID = key.UserID, key.ID, key.GroupID
		if key.Group != nil {
			r.Platform = key.Group.Platform
		}
	})
}
