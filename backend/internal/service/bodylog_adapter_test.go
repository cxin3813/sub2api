package service

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/bodylog"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestBodyLogPooledSocketIsScopedToEachLease(t *testing.T) {
	settings := bodylog.DefaultConfig()
	settings.Enabled = true
	logs, err := bodylog.NewManager(t.TempDir(), nil, settings)
	require.NoError(t, err)
	defer logs.Close()
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(&openAIWSFakeDialer{})
	defer pool.Close()
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	var connectionID string
	var captures []*bodylog.Session
	for _, user := range []int64{11, 22} {
		parent := logs.Begin(nil, "gateway_http", "GET", "/v1/responses")
		parent.Update(func(r *bodylog.Record) { r.UserID, r.APIKeyID = user, user+100 })
		ctx := bodylog.WithSession(context.Background(), parent)
		lease, err := pool.Acquire(ctx, openAIWSAcquireRequest{Account: account, WSURL: "wss://example.test/v1/responses", PreferredConnID: connectionID})
		require.NoError(t, err)
		if connectionID != "" {
			require.Equal(t, connectionID, lease.ConnID(), "exercise the same socket with a different user")
		}
		connectionID = lease.ConnID()
		require.NoError(t, lease.WriteJSON(map[string]any{"type": "response.create", "input": user}, time.Second))
		_, err = lease.ReadMessage(time.Second)
		require.NoError(t, err)
		captures = append(captures, lease.logSession)
		lease.Release()
		parent.End(101, nil)
		require.Error(t, lease.WriteJSON(map[string]string{"input": "after release"}, time.Second))
	}
	logs.Close()
	for i, capture := range captures {
		row, err := logs.Get(context.Background(), capture.Snapshot().ID)
		require.NoError(t, err)
		require.Equal(t, int64((i+1)*11), row.UserID)
		require.Len(t, row.Bodies, 2)
		require.NotEmpty(t, row.ParentID)
		for _, body := range row.Bodies {
			var content bytes.Buffer
			_, err := logs.WriteBody(context.Background(), row, body.ID, &content, 0, 0)
			require.NoError(t, err)
			if body.Direction == "upstream_request" {
				require.Contains(t, content.String(), fmt.Sprintf(`"input":%d`, row.UserID))
				require.NotContains(t, content.String(), fmt.Sprintf(`"input":%d`, int64(33)-row.UserID))
			}
		}
	}
}
