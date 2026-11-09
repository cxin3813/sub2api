package bodylog

import (
	"bufio"
	"context"
	"strings"
	"testing"
)

func newParser(input bool) *parser {
	return &parser{input: input, items: []Item{}, positions: map[string]int{}, parameters: map[string]any{}}
}

func TestAnthropicToolResultsKeepTheirOriginalRole(t *testing.T) {
	p := newParser(true)
	p.document([]byte(`{"system":"follow instructions","messages":[
{"role":"assistant","content":[{"type":"tool_use","id":"call_7","name":"read_file","input":{"path":"a.txt"}}]},
{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_7","content":"file contents","is_error":false},{"type":"text","text":"continue"}]}]}`), "")
	var result, history, user bool
	for _, item := range p.items {
		if item.Kind == "tool_result" {
			result = item.CallID == "call_7" && item.Role == "tool" && item.OriginalRole == "user" && item.Error != nil && !*item.Error
		}
		if item.Kind == "tool_call" {
			history = item.History && item.Name == "read_file"
		}
		if item.Role == "user" && item.Text == "continue" {
			user = true
		}
	}
	if !result || !history || !user {
		t.Fatalf("role/block semantics lost: %+v", p.items)
	}
}

func TestResponsesFinalSnapshotDoesNotDuplicateDeltas(t *testing.T) {
	p := newParser(false)
	p.document([]byte(`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"hello"}`), "event:0")
	p.document([]byte(`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":" world"}`), "event:1")
	p.document([]byte(`{"type":"response.completed","response":{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello world"}]}],"usage":{"input_tokens":9,"output_tokens":2}}}`), "event:2")
	if len(p.items) != 1 || p.items[0].Text != "hello world" {
		t.Fatalf("terminal snapshot duplicated streaming text: %+v", p.items)
	}
}

func TestParallelChatToolCallsRemainSeparate(t *testing.T) {
	p := newParser(false)
	p.document([]byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"lookup","arguments":"{\"x\":"}},{"index":1,"id":"b","function":{"name":"lookup","arguments":"{\"y\":"}}]}}]}`), "event:0")
	p.document([]byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"2}"}},{"index":0,"function":{"arguments":"1}"}}]}}]}`), "event:1")
	if len(p.items) != 2 || p.items[0].CallID != "a" || p.items[0].Arguments != `{"x":1}` || p.items[1].Arguments != `{"y":2}` {
		t.Fatalf("parallel calls merged: %+v", p.items)
	}
}

func TestSSEMultilineAndWSRoundTrips(t *testing.T) {
	m := testManager(t, nil)
	s := m.Begin(nil, "gateway_http", "GET", "/responses")
	ObserveMessage(WithSession(context.Background(), s), "client_response", 1, []byte(`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"你好"}`))
	ObserveMessage(WithSession(context.Background(), s), "client_response", 1, []byte(`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"你好"}`))
	s.End(101, nil)
	m.syncRecord(s.record.ID)
	r, err := m.Get(context.Background(), s.record.ID)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := m.Parse(context.Background(), r, r.Bodies[0].ID, 0, 50)
	if err != nil || len(parsed.Items) != 1 || parsed.Items[0].Text != "你好你好" {
		t.Fatalf("framing or repeated text lost: %+v %v", parsed, err)
	}
	ss := m.Begin(nil, "gateway_http", "POST", "/responses")
	w := ss.Body("client_response", "text/event-stream", "", nil)
	_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\ndata: \"delta\":\"ok\",\"output_index\":0,\"content_index\":0}\n\n"))
	w.Finish(true)
	ss.End(200, nil)
	m.syncRecord(ss.record.ID)
	rr, _ := m.Get(context.Background(), ss.record.ID)
	parsed, err = m.Parse(context.Background(), rr, rr.Bodies[0].ID, 0, 50)
	if err != nil || len(parsed.Items) != 1 || parsed.Items[0].Text != "ok" {
		t.Fatalf("multiline SSE lost: %+v %v", parsed, err)
	}
}

func TestPreviewNeverChangesArchivedBody(t *testing.T) {
	m := testManager(t, nil)
	s := m.Begin(nil, "gateway_http", "POST", "/chat/completions")
	w := s.Body("client_request", "application/json", "", nil)
	payload := `{"messages":[{"role":"user","content":"` + strings.Repeat("x", 2<<20) + `"}]}`
	_, _ = w.Write([]byte(payload))
	w.Finish(true)
	s.End(200, nil)
	m.syncRecord(s.record.ID)
	r, _ := m.Get(context.Background(), s.record.ID)
	p, err := m.Parse(context.Background(), r, r.Bodies[0].ID, 0, 50)
	if err != nil || len(p.Items) != 1 || !p.Items[0].HasMore || len(p.Items[0].Text) > 64<<10 {
		t.Fatalf("preview did not paginate large content: %+v %v", p, err)
	}
	if r.Bodies[0].Saved != int64(len(payload)) || r.Bodies[0].State != "complete" {
		t.Fatal("preview truncated storage")
	}
}

func TestGeminiStreamingCandidatesAndOriginalRoles(t *testing.T) {
	p := newParser(false)
	p.document([]byte(`{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"first "}]}}]}`), "event:0")
	p.document([]byte(`{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"answer"}]}},{"index":1,"content":{"role":"model","parts":[{"text":"another answer"}]}}]}`), "event:1")
	if len(p.items) != 2 || p.items[0].Text != "first answer" || p.items[1].Text != "another answer" || p.items[0].OriginalRole != "model" {
		t.Fatalf("Gemini candidates or roles were mixed: %+v", p.items)
	}
}

func TestAnthropicStartAndFinalUsageAreCombined(t *testing.T) {
	p := newParser(false)
	p.document([]byte(`{"type":"message_start","message":{"id":"msg_1","model":"claude","role":"assistant","usage":{"input_tokens":12,"output_tokens":0}}}`), "event:0")
	p.document([]byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`), "event:1")
	usage := object(p.parameters["usage"])
	if text(usage["input_tokens"]) != "12" || text(usage["output_tokens"]) != "7" || p.parameters["model"] != "claude" {
		t.Fatalf("metadata overwritten by terminal delta: %+v", p.parameters)
	}
}

func TestConfiguredLimitResetsAtEachExplicitWSRequest(t *testing.T) {
	m := testManager(t, nil)
	cfg := m.Config()
	cfg.Limit = 128
	m.config.Store(&cfg)
	s := m.Begin(nil, "gateway_http", "GET", "/v1/responses")
	ctx := WithSession(context.Background(), s)
	for range 3 {
		ObserveMessage(ctx, "client_request", 1, []byte(`{"type":"response.create","input":"each request gets its own budget"}`))
	}
	s.End(101, nil)
	row := s.Snapshot()
	if len(row.Bodies) != 3 {
		t.Fatalf("expected a body per request, got %d", len(row.Bodies))
	}
	for _, body := range row.Bodies {
		if body.State != "complete" || body.Saved != body.Bytes {
			t.Fatalf("connection-wide capture limit truncated a later request: %+v", body)
		}
	}
}

func TestSSELineEndingsIncludeLoneCarriageReturn(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("data: first\rdata: second\r\n\r\n"))
	scanner.Split(splitEventLines)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) != 3 || lines[0] != "data: first" || lines[1] != "data: second" || lines[2] != "" {
		t.Fatalf("invalid SSE line boundaries: %#v", lines)
	}
}

func TestInterruptedStreamHasPartialSemanticState(t *testing.T) {
	m := testManager(t, nil)
	s := m.Begin(nil, "gateway_http", "POST", "/v1/responses")
	w := s.Body("client_response", "text/event-stream", "", nil)
	_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"unfinished\",\"output_index\":0,\"content_index\":0}\n\n"))
	w.Finish(true)
	s.End(200, nil)
	m.syncRecord(s.record.ID)
	r, err := m.Get(context.Background(), s.record.ID)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := m.Parse(context.Background(), r, r.Bodies[0].ID, 0, 50)
	if err != nil || parsed.State != "partial" || parsed.Reason != "missing_stream_terminal_event" || parsed.Items[0].Text != "unfinished" {
		t.Fatalf("incomplete generation was presented as complete: %+v %v", parsed, err)
	}
}

func TestProtocolCompletionIndependentOfCaptureEOF(t *testing.T) {
	m := testManager(t, nil)
	session := m.Begin(nil, "gateway_http", "POST", "/v1/chat/completions")
	sink := session.Body("client_response", "text/event-stream", "", nil)
	_, _ = sink.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	sink.Finish(false)
	session.End(200, context.Canceled)
	row := session.Snapshot()
	parsed, err := m.Parse(t.Context(), row, row.Bodies[0].ID, 0, 50)
	if err != nil || parsed.ProtocolState != "completed" || parsed.CaptureState != "partial" || parsed.State != "ready" {
		t.Fatalf("completion evidence conflated: %+v %v", parsed, err)
	}
}
