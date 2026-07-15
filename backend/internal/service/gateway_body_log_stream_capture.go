package service

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
)

type gatewayBodyLogStreamCapture struct {
	limit     int
	body      []byte
	bytes     int64
	hash      hash.Hash
	truncated bool
}

func newGatewayBodyLogStreamCapture(limit int) *gatewayBodyLogStreamCapture {
	limit = clampGatewayBodyLogMaxBytes(limit)
	return &gatewayBodyLogStreamCapture{
		limit: limit,
		body:  make([]byte, 0, min(limit, 32*1024)),
		hash:  sha256.New(),
	}
}

func (c *gatewayBodyLogStreamCapture) WriteString(value string) {
	if c == nil || value == "" {
		return
	}
	c.WriteBytes([]byte(value))
}

func (c *gatewayBodyLogStreamCapture) Write(value []byte) (int, error) {
	c.WriteBytes(value)
	return len(value), nil
}

func (c *gatewayBodyLogStreamCapture) WriteBytes(value []byte) {
	if c == nil || len(value) == 0 {
		return
	}
	c.bytes += int64(len(value))
	_, _ = c.hash.Write(value)
	if len(c.body) >= c.limit {
		c.truncated = true
		return
	}
	remaining := c.limit - len(c.body)
	if len(value) <= remaining {
		c.body = append(c.body, value...)
		return
	}
	c.body = append(c.body, value[:remaining]...)
	c.truncated = true
}

func (c *gatewayBodyLogStreamCapture) Snapshot() *GatewayBodyLogBodyCapture {
	if c == nil || c.bytes == 0 {
		return nil
	}
	return &GatewayBodyLogBodyCapture{
		Body:      cloneBytes(c.body),
		Bytes:     c.bytes,
		SHA256:    hex.EncodeToString(c.hash.Sum(nil)),
		Truncated: c.truncated,
	}
}
