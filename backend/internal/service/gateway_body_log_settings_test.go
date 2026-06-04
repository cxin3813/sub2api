//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestSettingServiceGatewayBodyLogSettings_Defaults(t *testing.T) {
	svc := NewSettingService(&settingAntigravityUARepoStub{values: map[string]string{}}, &config.Config{})

	got, err := svc.GetGatewayBodyLogSettings(context.Background())

	require.NoError(t, err)
	require.False(t, got.Enabled)
	require.Equal(t, 262144, got.MaxBytes)
	require.True(t, got.CaptureRequest)
	require.True(t, got.CaptureResponse)
}

func TestSettingServiceGatewayBodyLogSettings_ClampsMaxBytes(t *testing.T) {
	svc := NewSettingService(&settingAntigravityUARepoStub{values: map[string]string{
		SettingKeyGatewayBodyLogEnabled:         "true",
		SettingKeyGatewayBodyLogMaxBytes:        "9999999",
		SettingKeyGatewayBodyLogCaptureRequest:  "false",
		SettingKeyGatewayBodyLogCaptureResponse: "true",
	}}, &config.Config{})

	got, err := svc.GetGatewayBodyLogSettings(context.Background())

	require.NoError(t, err)
	require.True(t, got.Enabled)
	require.Equal(t, 1048576, got.MaxBytes)
	require.False(t, got.CaptureRequest)
	require.True(t, got.CaptureResponse)
}

func TestSettingServiceUpdateSettings_PersistsGatewayBodyLogSettings(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := NewSettingService(repo, &config.Config{})

	err := svc.UpdateSettings(context.Background(), &SystemSettings{
		GatewayBodyLogEnabled:         true,
		GatewayBodyLogMaxBytes:        9999999,
		GatewayBodyLogCaptureRequest:  true,
		GatewayBodyLogCaptureResponse: false,
	})

	require.NoError(t, err)
	require.Equal(t, "true", repo.updates[SettingKeyGatewayBodyLogEnabled])
	require.Equal(t, "1048576", repo.updates[SettingKeyGatewayBodyLogMaxBytes])
	require.Equal(t, "true", repo.updates[SettingKeyGatewayBodyLogCaptureRequest])
	require.Equal(t, "false", repo.updates[SettingKeyGatewayBodyLogCaptureResponse])
}
