//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestSimpleModeRecordUsageWindowOptIn(t *testing.T) {
	for _, openAI := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("openai=%v/enabled=%v/stream=%v", openAI, enabled, stream), func(t *testing.T) {
					logs := &openAIRecordUsageLogRepoStub{inserted: true}
					billing := &openAIRecordUsageBillingRepoStub{}
					users := &openAIRecordUsageUserRepoStub{}
					subs := &openAIRecordUsageSubRepoStub{}
					key := &APIKey{ID: 1, Quota: 100, RateLimit5h: 30, Group: &Group{RateMultiplier: 1}}
					user := &User{ID: 2, Balance: 0}
					account := &Account{ID: 3, Type: AccountTypeAPIKey}
					if openAI {
						svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, billing, users, subs, nil)
						svc.cfg.RunMode = config.RunModeSimple
						svc.cfg.SimpleModeKeyRateLimitEnabled = enabled
						err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
							Result: &OpenAIForwardResult{RequestID: "simple-request", Model: "gpt-5.1", Usage: OpenAIUsage{InputTokens: 100, OutputTokens: 20}, Stream: stream},
							APIKey: key, User: user, Account: account,
						})
						require.NoError(t, err)
					} else {
						svc := newGatewayRecordUsageServiceWithBillingRepoForTest(logs, billing, users, subs)
						svc.cfg.RunMode = config.RunModeSimple
						svc.cfg.SimpleModeKeyRateLimitEnabled = enabled
						err := svc.RecordUsage(context.Background(), &RecordUsageInput{
							Result: &ForwardResult{RequestID: "simple-request", Model: "claude-sonnet-4", Usage: ClaudeUsage{InputTokens: 100, OutputTokens: 20}, Stream: stream},
							APIKey: key, User: user, Account: account,
						})
						require.NoError(t, err)
					}
					require.Equal(t, 1, logs.calls)
					require.Positive(t, logs.lastLog.ActualCost)
					require.Zero(t, users.deductCalls)
					require.Zero(t, subs.incrementCalls)
					if !enabled {
						require.Zero(t, billing.calls)
						return
					}
					require.Equal(t, 1, billing.calls)
					require.InDelta(t, logs.lastLog.ActualCost, billing.lastCmd.APIKeyRateLimitCost, 1e-12)
					require.Zero(t, billing.lastCmd.BalanceCost)
					require.Zero(t, billing.lastCmd.SubscriptionCost)
					require.Zero(t, billing.lastCmd.APIKeyQuotaCost)
					require.Zero(t, billing.lastCmd.AccountQuotaCost)
				})
			}
		}
	}
}

func TestRecordUsageCapturesBodyLogAcrossSimpleModeAndBillingFailure(t *testing.T) {
	for _, openAI := range []bool{false, true} {
		for _, mode := range []string{"simple", "simple-rate-limit", "billing-error", "simple-rate-limit-billing-error"} {
			t.Run(fmt.Sprintf("openai=%v/mode=%s", openAI, mode), func(t *testing.T) {
				billingFails := mode == "billing-error" || mode == "simple-rate-limit-billing-error"
				rateLimitEnabled := mode == "simple-rate-limit" || mode == "simple-rate-limit-billing-error"
				logs := &openAIRecordUsageLogRepoStub{inserted: true}
				billing := &openAIRecordUsageBillingRepoStub{}
				bodyLogs := &gatewayBodyLogRepoStub{}
				settings := NewSettingService(&settingAntigravityUARepoStub{values: map[string]string{
					SettingKeyGatewayBodyLogEnabled:         "true",
					SettingKeyGatewayBodyLogCaptureRequest:  "true",
					SettingKeyGatewayBodyLogCaptureResponse: "true",
				}}, &config.Config{})
				bodyLogService := NewGatewayBodyLogService(bodyLogs, settings)
				key := &APIKey{ID: 1, RateLimit5h: 30, Group: &Group{RateMultiplier: 1}}
				user := &User{ID: 2}
				account := &Account{ID: 3, Type: AccountTypeAPIKey}
				if billingFails {
					billing.err = errors.New("billing unavailable")
				}
				var err error
				if openAI {
					svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, billing, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
					svc.gatewayBodyLogService = bodyLogService
					if mode != "billing-error" {
						svc.cfg.RunMode = config.RunModeSimple
					}
					svc.cfg.SimpleModeKeyRateLimitEnabled = rateLimitEnabled
					err = svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
						Result: &OpenAIForwardResult{RequestID: "body-log-openai", Model: "gpt-5.1", Usage: OpenAIUsage{InputTokens: 100}, ResponseBody: []byte("response-body")},
						APIKey: key, User: user, Account: account, RequestBody: []byte("request-body"),
					})
				} else {
					svc := newGatewayRecordUsageServiceWithBillingRepoForTest(logs, billing, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
					svc.gatewayBodyLogService = bodyLogService
					if mode != "billing-error" {
						svc.cfg.RunMode = config.RunModeSimple
					}
					svc.cfg.SimpleModeKeyRateLimitEnabled = rateLimitEnabled
					err = svc.RecordUsage(context.Background(), &RecordUsageInput{
						Result: &ForwardResult{RequestID: "body-log-gateway", Model: "claude-sonnet-4", Usage: ClaudeUsage{InputTokens: 100}, ResponseBody: []byte("response-body")},
						APIKey: key, User: user, Account: account, RequestBody: []byte("request-body"),
					})
				}
				if billingFails {
					require.ErrorIs(t, err, billing.err)
				} else {
					require.NoError(t, err)
				}
				require.Equal(t, 1, logs.calls)
				require.NotNil(t, bodyLogs.upserted)
				require.Equal(t, logs.lastLog.ID, bodyLogs.upserted.UsageLogID)
				require.Equal(t, []byte("request-body"), bodyLogs.upserted.RequestBody)
				require.Equal(t, []byte("response-body"), bodyLogs.upserted.ResponseBody)
			})
		}
	}
}
