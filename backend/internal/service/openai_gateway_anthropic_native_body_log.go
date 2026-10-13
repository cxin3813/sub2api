package service

import (
	"io"
	"net/http"
	"sync"
)

type nativeAnthropicBodyLogCapture struct {
	io.ReadCloser
	mu      sync.Mutex
	capture *gatewayBodyLogStreamCapture
}

func newNativeAnthropicBodyLogCapture(body io.ReadCloser) *nativeAnthropicBodyLogCapture {
	return &nativeAnthropicBodyLogCapture{ReadCloser: body, capture: newGatewayBodyLogStreamCapture(GatewayBodyLogMaxBytesLimit)}
}

func (c *nativeAnthropicBodyLogCapture) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	// The native line pump reads upstream on a background goroutine.
	c.mu.Lock()
	c.capture.WriteBytes(p[:n])
	c.mu.Unlock()
	return n, err
}

func attachNativeAnthropicBodyLog(result *OpenAIForwardResult, body []byte, resp *http.Response, capture *nativeAnthropicBodyLogCapture) {
	if result == nil {
		return
	}
	result.RequestBody = cloneBytes(body)
	result.ResponseHeaders = resp.Header.Clone()
	result.StatusCode = resp.StatusCode
	result.ResponseContentType = resp.Header.Get("Content-Type")
	capture.mu.Lock()
	result.ResponseCapture = capture.capture.Snapshot()
	capture.mu.Unlock()
	if result.ResponseCapture != nil {
		result.ResponseBody = result.ResponseCapture.Body
	}
}
