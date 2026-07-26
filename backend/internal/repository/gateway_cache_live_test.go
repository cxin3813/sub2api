package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayCacheLiveCallIdentityAndController(t *testing.T) {
	redisServer := miniredis.RunT(t)
	writerClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { require.NoError(t, writerClient.Close()) })
	readerClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { require.NoError(t, readerClient.Close()) })

	cache, ok := NewGatewayCache(writerClient).(service.LiveCallStore)
	require.True(t, ok)
	otherInstance, ok := NewGatewayCache(readerClient).(service.LiveCallStore)
	require.True(t, ok)
	sessionID := "live-session-id"
	record := &service.LiveCallRecord{
		CallID:                "call_secret",
		CallHash:              HashLiveCallID("call_secret"),
		AccountID:             11,
		APIKeyID:              22,
		UserID:                33,
		GroupID:               44,
		LeaseID:               "lease",
		Model:                 "gpt-live-test",
		AttestationCiphertext: "encrypted-attestation",
		SessionID:             &sessionID,
		CreatedAt:             time.Now(),
		ExpiresAt:             time.Now().Add(time.Hour),
		Controller:            service.LiveControllerPending,
	}
	require.NoError(t, cache.SaveLiveCall(context.Background(), record, time.Hour))

	loaded, err := otherInstance.GetLiveCall(context.Background(), record.CallHash)
	require.NoError(t, err)
	require.Equal(t, record.CallID, loaded.CallID)
	require.Equal(t, record.AccountID, loaded.AccountID)
	require.Equal(t, record.AttestationCiphertext, loaded.AttestationCiphertext)
	require.NotNil(t, loaded.SessionID)
	require.Equal(t, sessionID, *loaded.SessionID)

	claimed, err := cache.ClaimLiveController(context.Background(), record.CallHash, service.LiveControllerObserver, "observer-1")
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = cache.ClaimLiveController(context.Background(), record.CallHash, service.LiveControllerProxy, "proxy-1")
	require.NoError(t, err)
	require.True(t, claimed)
	controller, err := cache.GetLiveController(context.Background(), record.CallHash)
	require.NoError(t, err)
	require.Equal(t, service.LiveControllerProxy, controller)

	released, err := cache.ReleaseLiveController(context.Background(), record.CallHash, "proxy-1")
	require.NoError(t, err)
	require.True(t, released)
	closed, err := cache.MarkLiveCallClosed(context.Background(), record.CallHash, time.Hour)
	require.NoError(t, err)
	require.True(t, closed)
	closed, err = cache.MarkLiveCallClosed(context.Background(), record.CallHash, time.Hour)
	require.NoError(t, err)
	require.False(t, closed)
}

func TestGatewayCacheLiveCallSessionIDNilWhenAbsentOrBlank(t *testing.T) {
	redisServer := miniredis.RunT(t)
	writerClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { require.NoError(t, writerClient.Close()) })
	readerClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { require.NoError(t, readerClient.Close()) })

	writer, ok := NewGatewayCache(writerClient).(service.LiveCallStore)
	require.True(t, ok)
	reader, ok := NewGatewayCache(readerClient).(service.LiveCallStore)
	require.True(t, ok)
	record := &service.LiveCallRecord{
		CallID:     "call_without_session",
		CallHash:   HashLiveCallID("call_without_session"),
		CreatedAt:  time.Now(),
		ExpiresAt:  time.Now().Add(time.Hour),
		Controller: service.LiveControllerPending,
	}
	require.NoError(t, writer.SaveLiveCall(context.Background(), record, time.Hour))

	loaded, err := reader.GetLiveCall(context.Background(), record.CallHash)
	require.NoError(t, err)
	require.Nil(t, loaded.SessionID)

	require.NoError(t, writerClient.HSet(context.Background(), liveCallKey(record.CallHash), "session_id", "  ").Err())
	loaded, err = reader.GetLiveCall(context.Background(), record.CallHash)
	require.NoError(t, err)
	require.Nil(t, loaded.SessionID)
}
