package bodylog

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const parseBudget = 32 << 20

type Item struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Role         string `json:"role"`
	OriginalRole string `json:"original_role,omitempty"`
	History      bool   `json:"history"`
	Text         string `json:"text,omitempty"`
	Name         string `json:"name,omitempty"`
	CallID       string `json:"call_id,omitempty"`
	Arguments    string `json:"arguments,omitempty"`
	Error        *bool  `json:"is_error,omitempty"`
	Source       string `json:"source"`
	Choice       int    `json:"choice,omitempty"`
	HasMore      bool   `json:"has_more"`
}

type Parsed struct {
	Version    int            `json:"parser_version"`
	Protocol   string         `json:"protocol"`
	State      string         `json:"state"`
	Reason     string         `json:"reason,omitempty"`
	Parameters map[string]any `json:"parameters"`
	Items      []Item         `json:"items"`
	Next       int            `json:"next_cursor"`
	HasMore    bool           `json:"has_more"`
}

type parser struct {
	input      bool
	protocol   string
	items      []Item
	positions  map[string]int
	parameters map[string]any
	prefix     string
	turn       int
	parseError string
	streamText bool
	stream     bool
}

func (m *Manager) Parse(ctx context.Context, r Record, bodyID string, cursor, limit int) (Parsed, error) {
	p, err := m.parseBody(ctx, r, bodyID)
	if err != nil {
		return Parsed{}, err
	}
	result := Parsed{Version: 1, Protocol: p.protocol, State: "ready", Parameters: p.parameters, Items: []Item{}}
	var b Body
	for _, candidate := range r.Bodies {
		if candidate.ID == bodyID {
			b = candidate
		}
	}
	if b.Saved > parseBudget {
		result.State, result.Reason = "partial", "parse_budget_exceeded; complete saved content remains available"
	}
	if p.parseError != "" {
		result.State, result.Reason = "partial", p.parseError
	}
	if result.State == "ready" && p.stream && !p.input && len(p.items) > 0 {
		known := p.protocol == "responses" || p.protocol == "anthropic" || p.protocol == "chat_completions" || p.protocol == "gemini"
		terminal := p.parameters["stream_terminal"] != nil || p.parameters["finish_reason"] != nil
		for key := range p.parameters {
			terminal = terminal || strings.HasSuffix(key, "_finish_reason")
		}
		if known && !terminal {
			result.State, result.Reason = "partial", "missing_stream_terminal_event"
		}
	}
	if b.State != "complete" && b.State != "empty" {
		result.State, result.Reason = "partial", b.State
	}
	if len(p.items) == 0 && b.Saved > 0 {
		result.State = "unsupported"
		if b.Saved > parseBudget {
			result.State, result.Reason = "partial", "parse_budget_exceeded"
		}
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	cursor = min(max(0, cursor), len(p.items))
	end := min(cursor+limit, len(p.items))
	for _, item := range p.items[cursor:end] {
		item.Text, item.HasMore = preview(item.Text)
		arguments, more := preview(item.Arguments)
		item.Arguments, item.HasMore = arguments, item.HasMore || more
		result.Items = append(result.Items, item)
	}
	result.Next, result.HasMore = end, end < len(p.items)
	return result, nil
}

func preview(s string) (string, bool) {
	if len(s) <= 64<<10 {
		return s, false
	}
	n := 64 << 10
	for n > 0 && s[n]&0xc0 == 0x80 {
		n--
	}
	return s[:n], true
}

func (m *Manager) parseBody(ctx context.Context, r Record, bodyID string) (*parser, error) {
	select {
	case m.parseSlots <- struct{}{}:
		defer func() { <-m.parseSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	p := &parser{positions: map[string]int{}, parameters: map[string]any{}, items: []Item{}}
	var body *Body
	for i := range r.Bodies {
		if r.Bodies[i].ID == bodyID {
			body = &r.Bodies[i]
		}
	}
	if body == nil || r.State == "expired" {
		return nil, ErrNotFound
	}
	if strings.Contains(r.Path, "/messages") || body.Headers.Get("anthropic-version") != "" {
		p.protocol = "anthropic"
	}
	p.input = strings.HasSuffix(body.Direction, "request")
	var raw bytes.Buffer
	if _, err := m.WriteBody(ctx, r, bodyID, &raw, 0, parseBudget); err != nil {
		return nil, err
	}
	data := raw.Bytes()
	switch {
	case body.ContentType == "application/x-bodylog-ws":
		p.protocol, p.stream = "websocket", true
		reader := bytes.NewReader(data)
		var magic [4]byte
		_, _ = io.ReadFull(reader, magic[:])
		if string(magic[:]) != "BWS1" {
			return p, nil
		}
		seq := 0
		for reader.Len() >= 13 && ctx.Err() == nil {
			var header [13]byte
			_, _ = io.ReadFull(reader, header[:])
			size := int64(binary.BigEndian.Uint32(header[:4]))
			if size > int64(reader.Len()) {
				break
			}
			payload := make([]byte, size)
			_, _ = io.ReadFull(reader, payload)
			p.document(payload, fmt.Sprintf("message:%d", seq))
			seq++
		}
		if reader.Len() != 0 {
			p.parseError = "incomplete_message_archive"
		}
	case strings.Contains(body.ContentType, "event-stream") || bytes.HasPrefix(bytes.TrimSpace(data), []byte("data:")) || bytes.HasPrefix(bytes.TrimSpace(data), []byte("event:")):
		p.protocol, p.stream = "sse", true
		scanner := bufio.NewScanner(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
		scanner.Split(splitEventLines)
		scanner.Buffer(make([]byte, 4096), 8<<20)
		var parts []string
		seq := 0
		flush := func() {
			if len(parts) > 0 {
				p.document([]byte(strings.Join(parts, "\n")), fmt.Sprintf("event:%d", seq))
				seq++
			}
			parts = nil
		}
		for scanner.Scan() && ctx.Err() == nil {
			line := scanner.Text()
			if line == "" {
				flush()
			} else if strings.HasPrefix(line, "data:") {
				parts = append(parts, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
		}
		if err := scanner.Err(); err != nil {
			p.parseError = "event_parse_budget_exceeded"
		} else if len(parts) > 0 {
			p.parseError = "unterminated_sse_event"
		}
		// An unterminated SSE event is not silently promoted to a complete event.
	default:
		p.document(data, "")
	}
	return p, ctx.Err()
}

func splitEventLines(data []byte, atEOF bool) (int, []byte, error) {
	for i, c := range data {
		if c == '\n' {
			return i + 1, data[:i], nil
		}
		if c == '\r' {
			if i+1 == len(data) && !atEOF {
				return 0, nil, nil
			}
			n := i + 1
			if n < len(data) && data[n] == '\n' {
				n++
			}
			return n, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func (p *parser) put(key string, item Item, appendText bool) {
	key = p.prefix + key
	if i, ok := p.positions[key]; ok {
		old := &p.items[i]
		if appendText {
			old.Text += item.Text
			old.Arguments += item.Arguments
			if item.Name != "" && old.Name == "" {
				old.Name = item.Name
			}
		} else {
			if item.Text != "" {
				old.Text = item.Text
			}
			if item.Arguments != "" {
				old.Arguments = item.Arguments
			}
			if item.Kind != "" {
				old.Kind = item.Kind
			}
			if item.Name != "" {
				old.Name = item.Name
			}
		}
		if item.CallID != "" {
			old.CallID = item.CallID
		}
		return
	}
	item.ID = key
	item.History = p.input && item.Role == "assistant"
	p.positions[key] = len(p.items)
	p.items = append(p.items, item)
}

func text(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	raw, _ := json.MarshalIndent(v, "", "  ")
	return string(raw)
}
func object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func array(v any) []any           { a, _ := v.([]any); return a }
func number(v any) int {
	n, _ := v.(json.Number)
	i, _ := strconv.Atoi(string(n))
	return i
}

func (p *parser) document(raw []byte, path string) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("[DONE]")) {
		p.parameters["stream_terminal"] = "[DONE]"
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		p.parseError = "invalid_or_incomplete_json"
		return
	}
	v := object(value)
	if v == nil {
		p.put(path, Item{Kind: "data", Text: text(value), Source: path}, false)
		return
	}
	if p.event(v, path) {
		return
	}
	p.metadata(v)
	if v["messages"] != nil {
		if p.protocol != "anthropic" {
			p.protocol = "chat_completions"
		}
		for i, msg := range array(v["messages"]) {
			p.message(object(msg), fmt.Sprintf("%s/messages/%d", path, i))
		}
	}
	if v["system"] != nil {
		p.content(v["system"], "system", path+"/system")
	}
	if v["instructions"] != nil {
		p.content(v["instructions"], "developer", path+"/instructions")
	}
	if input, exists := v["input"]; exists {
		p.protocol = "responses"
		switch x := input.(type) {
		case string:
			p.content(x, "user", path+"/input")
		case []any:
			for i, item := range x {
				p.typed(object(item), fmt.Sprintf("%s/input/%d", path, i), true)
			}
		}
	}
	if output, exists := v["output"]; exists {
		p.protocol = "responses"
		for i, item := range array(output) {
			p.typed(object(item), fmt.Sprintf("resp:%d", i), false)
		}
	}
	if v["systemInstruction"] != nil {
		p.content(object(v["systemInstruction"])["parts"], "system", path+"/systemInstruction/parts")
	}
	if contents := array(v["contents"]); contents != nil {
		p.protocol = "gemini"
		for i, item := range contents {
			content := object(item)
			role := text(content["role"])
			p.content(content["parts"], role, fmt.Sprintf("%s/contents/%d/parts", path, i))
		}
	}
	if candidates := array(v["candidates"]); candidates != nil {
		p.protocol = "gemini"
		for i, item := range candidates {
			candidate := object(item)
			content := object(candidate["content"])
			index := i
			if candidate["index"] != nil {
				index = number(candidate["index"])
			}
			p.streamText = strings.HasPrefix(path, "event:") || strings.HasPrefix(path, "message:")
			role := text(content["role"])
			if role == "" {
				role = "model"
			}
			p.content(content["parts"], role, fmt.Sprintf("candidate:%d/parts", index))
			p.streamText = false
			if finish := candidate["finishReason"]; finish != nil {
				p.parameters[fmt.Sprintf("candidate_%d_finish_reason", index)] = finish
			}
		}
	}
	if choices := array(v["choices"]); choices != nil {
		p.protocol = "chat_completions"
		for i, item := range choices {
			choice := object(item)
			if message := object(choice["message"]); message != nil {
				p.message(message, fmt.Sprintf("%s/choices/%d/message", path, i))
			}
		}
	}
	if v["content"] != nil && v["role"] != nil {
		p.protocol = "anthropic"
		p.content(v["content"], text(v["role"]), path+"/content")
	}
	if len(p.items) == 0 {
		// Non-dialogue APIs keep their actual parameter/result structure.
		p.put("data", Item{Kind: "data", Text: text(value), Source: path}, false)
	}
}

func (p *parser) metadata(v map[string]any) {
	for key, value := range v {
		switch key {
		case "messages", "input", "output", "content", "contents", "candidates", "choices", "system", "systemInstruction", "instructions", "delta", "item", "response", "type":
			continue
		default:
			if key == "usage" {
				if previous := object(p.parameters[key]); previous != nil {
					if update := object(value); update != nil {
						for k, val := range update {
							previous[k] = val
						}
						value = previous
					}
				}
			}
			if raw, err := json.Marshal(value); err == nil && len(raw) <= 64<<10 {
				p.parameters[key] = value
			} else {
				p.parameters[key] = map[string]string{"source": "/" + key, "state": "expand_in_original"}
			}
		}
	}
}

func (p *parser) message(v map[string]any, path string) {
	if v == nil {
		return
	}
	role := text(v["role"])
	if role == "" {
		role = "assistant"
	}
	if role == "tool" || role == "function" {
		p.put(path, Item{Kind: "tool_result", Role: "tool", OriginalRole: role, Text: text(v["content"]), CallID: text(v["tool_call_id"]), Name: text(v["name"]), Source: path}, false)
		return
	}
	p.content(v["content"], role, path+"/content")
	for i, call := range array(v["tool_calls"]) {
		t := object(call)
		f := object(t["function"])
		p.put(fmt.Sprintf("%s/tool_calls/%d", path, i), Item{Kind: "tool_call", Role: "assistant", OriginalRole: role, Name: text(f["name"]), Arguments: text(f["arguments"]), CallID: text(t["id"]), Source: path}, false)
	}
	if f := object(v["function_call"]); f != nil {
		p.put(path+"/function_call", Item{Kind: "tool_call", Role: "assistant", Name: text(f["name"]), Arguments: text(f["arguments"]), Source: path}, false)
	}
	for _, key := range []string{"reasoning_content", "reasoning", "refusal"} {
		if value, ok := v[key]; ok {
			p.put(path+"/"+key, Item{Kind: key, Role: role, Text: text(value), Source: path + "/" + key}, false)
		}
	}
}

func (p *parser) typed(v map[string]any, path string, input bool) {
	if v == nil {
		return
	}
	t := text(v["type"])
	switch t {
	case "", "message":
		role := text(v["role"])
		if role == "" {
			role = "assistant"
			if input {
				role = "user"
			}
		}
		p.content(v["content"], role, path+"/content")
	case "function_call", "custom_tool_call":
		arguments := v["arguments"]
		if t == "custom_tool_call" {
			arguments = v["input"]
		}
		p.put(path, Item{Kind: "tool_call", Role: "assistant", Name: text(v["name"]), CallID: text(v["call_id"]), Arguments: text(arguments), Source: path}, false)
	case "function_call_output", "custom_tool_call_output":
		p.put(path, Item{Kind: "tool_result", Role: "tool", CallID: text(v["call_id"]), Text: text(v["output"]), Source: path}, false)
	case "reasoning":
		p.put(path, Item{Kind: "reasoning", Role: "assistant", Text: text(v["summary"]), Source: path}, false)
	default:
		p.put(path, Item{Kind: t, Text: text(v), Source: path}, false)
	}
}

func (p *parser) content(value any, role, path string) {
	if value == nil {
		return
	}
	originalRole := role
	if role == "model" {
		role = "assistant"
	}
	if str, ok := value.(string); ok {
		kind := "message"
		if role == "system" || role == "developer" {
			kind = "instruction"
		}
		p.put(path, Item{Kind: kind, Role: role, OriginalRole: originalRole, Text: str, Source: path}, p.streamText)
		return
	}
	for i, raw := range array(value) {
		v := object(raw)
		key := fmt.Sprintf("%s/%d", path, i)
		item := Item{Role: role, OriginalRole: originalRole, Source: key, Kind: text(v["type"])}
		switch item.Kind {
		case "text", "input_text", "output_text", "":
			item.Kind, item.Text = "message", text(v["text"])
			if thought, _ := v["thought"].(bool); thought {
				item.Kind = "reasoning"
			}
			if call := object(v["functionCall"]); call != nil {
				item.Kind, item.Role, item.Name, item.CallID, item.Arguments = "tool_call", "assistant", text(call["name"]), text(call["id"]), text(call["args"])
			} else if result := object(v["functionResponse"]); result != nil {
				item.Kind, item.Role, item.Name, item.CallID, item.Text = "tool_result", "tool", text(result["name"]), text(result["id"]), text(result["response"])
			} else if item.Text == "" {
				item.Kind, item.Text = "attachment", text(v)
			}
		case "tool_use":
			item.Kind, item.Role, item.Name, item.CallID, item.Arguments = "tool_call", "assistant", text(v["name"]), text(v["id"]), text(v["input"])
		case "tool_result":
			item.Kind, item.Role, item.CallID, item.Text = "tool_result", "tool", text(v["tool_use_id"]), text(v["content"])
			if e, ok := v["is_error"].(bool); ok {
				item.Error = &e
			}
		case "thinking":
			item.Kind, item.Text = "reasoning", text(v["thinking"])
		default:
			item.Text = text(v)
		}
		if role == "system" || role == "developer" {
			item.Kind = "instruction"
		}
		p.put(key, item, p.streamText && (item.Kind == "message" || item.Kind == "reasoning"))
	}
}

func (p *parser) event(v map[string]any, path string) bool {
	eventType := text(v["type"])
	if eventType == "response.create" {
		p.turn++
		p.prefix = fmt.Sprintf("turn:%d:", p.turn)
		container := object(v["response"])
		if container == nil {
			container = make(map[string]any, len(v))
			for k, value := range v {
				if k != "type" {
					container[k] = value
				}
			}
		}
		raw, _ := json.Marshal(container)
		p.document(raw, path)
		return true
	}
	if strings.HasPrefix(eventType, "response.") {
		p.protocol = "responses"
		if eventType == "response.created" {
			p.turn++
			p.prefix = fmt.Sprintf("turn:%d:", p.turn)
			p.metadata(object(v["response"]))
		}
		index := number(v["output_index"])
		key := fmt.Sprintf("resp:%d", index)
		switch eventType {
		case "response.output_text.delta", "response.refusal.delta":
			p.put(key+"/content/"+strconv.Itoa(number(v["content_index"])), Item{Kind: "message", Role: "assistant", Text: text(v["delta"]), Source: path}, true)
		case "response.function_call_arguments.delta":
			p.put(key, Item{Kind: "tool_call", Role: "assistant", Arguments: text(v["delta"]), Source: path}, true)
		case "response.output_item.added", "response.output_item.done":
			p.typed(object(v["item"]), key, false)
		case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled":
			if response := object(v["response"]); response != nil {
				p.metadata(response)
				for i, item := range array(response["output"]) {
					p.typed(object(item), fmt.Sprintf("resp:%d", i), false)
				}
			}
			p.parameters["stream_terminal"] = eventType
		}
		if eventType == "response.function_call_arguments.done" {
			p.put(key, Item{Kind: "tool_call", Role: "assistant", Arguments: text(v["arguments"]), Source: path}, false)
		}
		return true
	}
	switch eventType {
	case "message_start", "message_delta", "message_stop", "content_block_start", "content_block_delta", "content_block_stop", "ping":
		p.protocol = "anthropic"
		key := fmt.Sprintf("anth:%d", number(v["index"]))
		if eventType == "content_block_start" {
			block := object(v["content_block"])
			kind := text(block["type"])
			item := Item{Role: "assistant", Kind: "message", Text: text(block["text"]), Source: path}
			if kind == "tool_use" {
				item.Kind, item.CallID, item.Name = "tool_call", text(block["id"]), text(block["name"])
				if len(object(block["input"])) > 0 {
					item.Arguments = text(block["input"])
				}
			} else if kind == "thinking" {
				item.Kind, item.Text = "reasoning", text(block["thinking"])
			}
			p.put(key, item, false)
		}
		if eventType == "message_start" {
			p.metadata(object(v["message"]))
		}
		if eventType == "content_block_delta" {
			delta := object(v["delta"])
			item := Item{Role: "assistant", Source: path}
			switch text(delta["type"]) {
			case "text_delta":
				item.Kind, item.Text = "message", text(delta["text"])
			case "input_json_delta":
				item.Kind, item.Arguments = "tool_call", text(delta["partial_json"])
			case "thinking_delta":
				item.Kind, item.Text = "reasoning", text(delta["thinking"])
			}
			p.put(key, item, true)
		}
		if eventType == "message_stop" {
			p.parameters["stream_terminal"] = eventType
		}
		if eventType == "message_delta" {
			p.metadata(v)
			p.metadata(object(v["delta"]))
		}
		return true
	}
	choices := array(v["choices"])
	isChunk := false
	for _, rawChoice := range choices {
		choice := object(rawChoice)
		delta := object(choice["delta"])
		if delta == nil {
			continue
		}
		isChunk, p.protocol = true, "chat_completions"
		key := fmt.Sprintf("chat:%d", number(choice["index"]))
		if value, exists := delta["content"]; exists {
			p.put(key, Item{Kind: "message", Role: "assistant", Text: text(value), Source: path}, true)
		}
		for _, field := range []string{"reasoning_content", "reasoning", "refusal"} {
			if value, exists := delta[field]; exists {
				p.put(key+"/"+field, Item{Kind: field, Role: "assistant", Text: text(value), Source: path}, true)
			}
		}
		for _, rawCall := range array(delta["tool_calls"]) {
			call := object(rawCall)
			f := object(call["function"])
			p.put(fmt.Sprintf("%s:tool:%d", key, number(call["index"])), Item{Kind: "tool_call", Role: "assistant", Name: text(f["name"]), CallID: text(call["id"]), Arguments: text(f["arguments"]), Source: path}, true)
		}
		if choice["finish_reason"] != nil {
			p.parameters["finish_reason"] = choice["finish_reason"]
		}
	}
	if isChunk {
		p.metadata(v)
	}
	return isChunk
}
