//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNativeAnthropicForwardCapturesBodyLog(t *testing.T) {
	for _, platform := range []string{PlatformOpenCodeGo, PlatformCommandCode} {
		for _, protocol := range []string{APIProtocolResponses, APIProtocolChatCompletions} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/stream=%t", platform, protocol, stream), func(t *testing.T) {
					account := nativeAnthropicTestAccount()
					account.Platform = platform
					resp := nativeAnthropicStreamResponse()
					responseBody, err := io.ReadAll(resp.Body)
					require.NoError(t, err)
					upstream := &httpUpstreamRecorder{resp: nativeAnthropicStreamResponse()}
					svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
					body := []byte(fmt.Sprintf(`{"model":"claude-sonnet-4.5","stream":%t,"input":"hi","messages":[{"role":"user","content":"hi"}]}`, stream))
					ctx := adaptiveProtocolTestContext("/v1/"+protocol, body)
					var result *OpenAIForwardResult
					if protocol == APIProtocolResponses {
						result, err = svc.forwardResponsesViaNativeAnthropic(context.Background(), ctx, account, body, "")
					} else {
						result, err = svc.forwardChatCompletionsViaNativeAnthropic(context.Background(), ctx, account, body, "")
					}
					require.NoError(t, err)
					require.NotNil(t, result)
					require.NotEmpty(t, result.RequestBody)
					require.Equal(t, upstream.lastBody, result.RequestBody)
					require.Contains(t, string(result.RequestBody), `"messages"`)
					require.NotNil(t, result.ResponseCapture)
					require.Equal(t, responseBody, result.ResponseBody)
					require.Equal(t, int64(len(responseBody)), result.ResponseCapture.Bytes)
					require.False(t, result.ResponseCapture.Truncated)
					require.Equal(t, http.StatusOK, result.StatusCode)
					require.Equal(t, "text/event-stream", result.ResponseContentType)
					require.Equal(t, "text/event-stream", result.ResponseHeaders.Get("Content-Type"))
				})
			}
		}
	}
}
