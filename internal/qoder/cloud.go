package qoder

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"qoder2api/internal/protocol"
)

type cloudBackend struct {
	baseURL       string
	pat           string
	defaultModel  string
	workspaceRoot string
	client        *http.Client

	mu            sync.Mutex
	environmentID string
	agents        map[string]*cloudAgentEntry
}

// cloudAgentEntry 缓存按“客户端工具集”建立的云端 agent。
// reverse 是“云端工具名 -> 客户端原始工具名”的回译表：云端内建名会和客户端工具名冲突
// （实测 Claude Code 的 Bash/Read/Write/Edit/Glob/Grep/WebFetch 都冲突），注册时改名，
// 回译时还原成客户端认识的名字。
type cloudAgentEntry struct {
	id      string
	reverse map[string]string
}

type cloudListResponse[T any] struct {
	Data []T `json:"data"`
}

type cloudEnvironment struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type cloudAgent struct {
	ID string `json:"id"`
}

type cloudSession struct {
	ID string `json:"id"`
}

type cloudEventStream struct {
	resp     *http.Response
	scanner  *bufio.Scanner
	lastType string
	reverse  map[string]string
}

func (b *cloudBackend) Capabilities() Capabilities {
	return Capabilities{}
}

func NewCloudBackend(opts Options, client *http.Client) Backend {
	return &cloudBackend{
		baseURL:       strings.TrimRight(opts.BaseURL, "/") + "/api/v1/cloud",
		pat:           opts.PAT,
		defaultModel:  opts.DefaultModel,
		workspaceRoot: opts.WorkspaceRoot,
		client:        client,
		agents:        map[string]*cloudAgentEntry{},
	}
}

func (b *cloudBackend) Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error) {
	stream, err := b.Stream(ctx, req)
	if err != nil {
		return CompleteResponse{}, err
	}
	defer stream.Close()

	var content strings.Builder
	var reasoning strings.Builder
	var toolCalls json.RawMessage
	var usage *protocol.Usage
	for {
		event, err := stream.Next()
		if err != nil {
			return CompleteResponse{}, err
		}
		if event.Done {
			break
		}
		if event.Content != "" {
			content.WriteString(event.Content)
		}
		if event.Reasoning != "" {
			reasoning.WriteString(event.Reasoning)
		}
		if !rawJSONIsEmpty(event.ToolCalls) {
			toolCalls = event.ToolCalls
		}
		if event.Usage != nil {
			usage = event.Usage
		}
	}
	return CompleteResponse{
		Content:   content.String(),
		Reasoning: reasoning.String(),
		ToolCalls: toolCalls,
		Usage:     usage,
	}, nil
}

func (b *cloudBackend) Stream(ctx context.Context, req CompleteRequest) (Stream, error) {
	agent, err := b.ensureResources(ctx, req.Model, req.Tools)
	if err != nil {
		return nil, err
	}

	sessionID, err := b.createSession(ctx, agent.id)
	if err != nil {
		return nil, err
	}
	if err := b.sendMessage(ctx, sessionID, req); err != nil {
		return nil, err
	}
	return b.openStream(ctx, sessionID, agent.reverse)
}

func (b *cloudBackend) ensureResources(ctx context.Context, model string, tools json.RawMessage) (*cloudAgentEntry, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.environmentID == "" {
		envID, err := b.ensureEnvironment(ctx)
		if err != nil {
			return nil, err
		}
		b.environmentID = envID
	}
	if b.agents == nil {
		b.agents = map[string]*cloudAgentEntry{}
	}
	cloudTools, reverse := cloudToolsFromRequest(tools)
	sig := agentSignature(model, cloudTools)
	if entry, ok := b.agents[sig]; ok {
		return entry, nil
	}
	id, err := b.createAgent(ctx, model, cloudTools)
	if err != nil {
		return nil, err
	}
	entry := &cloudAgentEntry{id: id, reverse: reverse}
	b.agents[sig] = entry
	return entry, nil
}

func (b *cloudBackend) ensureEnvironment(ctx context.Context) (string, error) {
	var list cloudListResponse[cloudEnvironment]
	if err := b.getJSON(ctx, "/environments", &list); err != nil {
		return "", err
	}
	for _, env := range list.Data {
		if env.Status == "" || env.Status == "ready" {
			return env.ID, nil
		}
	}

	body := map[string]any{
		"name": "qoder2api-default",
		"config": map[string]any{
			"type": "cloud",
			"networking": map[string]any{
				"type": "unrestricted",
			},
		},
	}
	var created cloudEnvironment
	if err := b.postJSON(ctx, "/environments", body, &created); err != nil {
		return "", err
	}
	return created.ID, nil
}

func (b *cloudBackend) createAgent(ctx context.Context, model string, tools []map[string]any) (string, error) {
	model = normalizeModel(model, b.defaultModel)

	instructions := strings.TrimSpace(`
You are an efficient programming assistant.
Reply concisely and correctly.
When the user supplies chat history, treat the provided transcript as authoritative context.
If the task is coding-related, prefer concrete patches, commands, and implementation details.
Use the provided tools to inspect and modify the workspace whenever the task requires it.
Only call tools that are explicitly provided in this request; never invent tool names.
`)

	body := map[string]any{
		"name":   "qoder2api-go-bridge",
		"model":  model,
		"system": instructions,
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	var created cloudAgent
	if err := b.postJSON(ctx, "/agents", body, &created); err != nil {
		return "", err
	}
	return created.ID, nil
}

// Qoder 云端 Agent API 只接受官方模型 key；Codex / Claude Code 等客户端发来的
// 模型名（如 claude-sonnet-5、gpt-*）不属于此列，需要回落到默认模型。
var knownQoderModels = map[string]bool{
	"auto": true, "ultimate": true, "fast": true, "performance": true,
	"efficient": true, "smodel": true, "cmodel": true, "qmodel_38max": true,
	"qfmodel": true, "qmodel_latest": true, "qmodel": true, "kmodel_latest": true,
	"kmodel": true, "gmodel": true, "gfmodel": true, "dmodel": true,
	"dfmodel": true, "mmodel": true,
}

func normalizeModel(requested, fallback string) string {
	m := strings.TrimSpace(requested)
	if m != "" && knownQoderModels[strings.ToLower(m)] {
		return m
	}
	return fallback
}

// Qoder 云端 Agent 自带的工具名；注册客户端工具时若重名需改名。
var builtinCloudToolNames = map[string]bool{
	"bash": true, "read": true, "write": true, "edit": true, "glob": true,
	"grep": true, "webfetch": true, "web_search": true, "websearch": true,
}

// cloudToolsFromRequest 把客户端工具定义（OpenAI function 形状）转成云端 custom tool，
// 并返回“云端名 -> 客户端原名”的回译表。
func cloudToolsFromRequest(raw json.RawMessage) ([]map[string]any, map[string]string) {
	if rawJSONIsEmpty(raw) {
		return nil, nil
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, nil
	}
	tools := make([]map[string]any, 0, len(items))
	reverse := map[string]string{}
	used := map[string]bool{}
	for _, item := range items {
		name, desc, schema := toolNameDescSchema(item)
		name = sanitizeToolName(name)
		if name == "" {
			continue
		}
		safe := name
		lower := strings.ToLower(safe)
		if builtinCloudToolNames[lower] || used[safe] || strings.HasPrefix(lower, "mcp__") {
			safe = "cc_" + name
			for used[safe] {
				safe += "_"
			}
		}
		used[safe] = true
		if safe != name {
			reverse[safe] = name
		}
		tools = append(tools, map[string]any{
			"type":         "custom",
			"name":         safe,
			"description":  desc,
			"input_schema": schema,
		})
	}
	if len(tools) == 0 {
		return nil, nil
	}
	return tools, reverse
}

func toolNameDescSchema(item map[string]any) (string, string, any) {
	if fn, ok := item["function"].(map[string]any); ok {
		schema := fn["parameters"]
		if schema == nil {
			schema = objectSchema()
		}
		return stringValue(fn["name"]), stringValue(fn["description"]), schema
	}
	schema := item["input_schema"]
	if schema == nil {
		schema = item["parameters"]
	}
	if schema == nil {
		schema = objectSchema()
	}
	return stringValue(item["name"]), stringValue(item["description"]), schema
}

func objectSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func sanitizeToolName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	var sb strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			sb.WriteRune(r)
		default:
			sb.WriteRune('_')
		}
	}
	return sb.String()
}

func agentSignature(model string, tools []map[string]any) string {
	buf, _ := json.Marshal(tools)
	sum := sha256.Sum256(append([]byte(model+"\x00"), buf...))
	return fmt.Sprintf("%x", sum[:8])
}

func (b *cloudBackend) createSession(ctx context.Context, agentID string) (string, error) {
	body := map[string]any{
		"agent":          agentID,
		"environment_id": b.environmentID,
	}
	if b.workspaceRoot != "" {
		body["metadata"] = map[string]any{
			"workspace_root": b.workspaceRoot,
		}
	}
	var created cloudSession
	if err := b.postJSON(ctx, "/sessions", body, &created); err != nil {
		return "", err
	}
	return created.ID, nil
}

func (b *cloudBackend) sendMessage(ctx context.Context, sessionID string, req CompleteRequest) error {
	body := map[string]any{
		"events": []map[string]any{{
			"type":    "user.message",
			"content": []any{map[string]any{"type": "text", "text": renderPrompt(req)}},
		}},
	}
	return b.postJSON(ctx, "/sessions/"+sessionID+"/events", body, nil)
}

func (b *cloudBackend) openStream(ctx context.Context, sessionID string, reverse map[string]string) (Stream, error) {
	url := b.baseURL + "/sessions/" + sessionID + "/events/stream"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+b.pat)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("open stream http %d: %s", resp.StatusCode, string(data))
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	return &cloudEventStream{resp: resp, scanner: scanner, reverse: reverse}, nil
}

func (s *cloudEventStream) Next() (protocol.DeltaEvent, error) {
	var eventType string
	var dataLine string
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			s.lastType = eventType
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLine = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			continue
		}
		if line == "" && (eventType != "" || dataLine != "") {
			return parseCloudEvent(eventType, dataLine, s.reverse)
		}
	}
	if err := s.scanner.Err(); err != nil {
		return protocol.DeltaEvent{}, err
	}
	return protocol.DeltaEvent{Done: true}, nil
}

func (s *cloudEventStream) Close() error {
	return s.resp.Body.Close()
}

func parseCloudEvent(eventType, dataLine string, reverse map[string]string) (protocol.DeltaEvent, error) {
	if eventType == "session.status_idle" || eventType == "terminated" {
		return protocol.DeltaEvent{Done: true}, nil
	}
	if eventType == "session.error" {
		return protocol.DeltaEvent{}, fmt.Errorf("session error: %s", dataLine)
	}
	if eventType != "agent.message" && eventType != "agent.tool_use" && eventType != "agent.custom_tool_use" {
		return protocol.DeltaEvent{}, nil
	}

	var payload map[string]any
	if dataLine != "" && dataLine != "{}" {
		_ = json.Unmarshal([]byte(dataLine), &payload)
	}
	switch eventType {
	case "agent.message":
		return protocol.DeltaEvent{
			Content: normalizeContent(payload["content"]),
		}, nil
	case "agent.tool_use", "agent.custom_tool_use":
		name := stringValue(payload["name"])
		if orig, ok := reverse[name]; ok {
			name = orig
		}
		input := payload["input"]
		call := []map[string]any{{
			"id":   "call_" + randomHex(12),
			"type": "function",
			"function": map[string]any{
				"name":      name,
				"arguments": mustJSON(input),
			},
		}}
		raw, _ := json.Marshal(call)
		return protocol.DeltaEvent{ToolCalls: raw}, nil
	default:
		return protocol.DeltaEvent{}, nil
	}
}

func (b *cloudBackend) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+b.pat)
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("get %s http %d: %s", path, resp.StatusCode, string(data))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (b *cloudBackend) postJSON(ctx context.Context, path string, body any, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+b.pat)
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("post %s http %d: %s", path, resp.StatusCode, string(data))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func renderPrompt(req CompleteRequest) string {
	var sb strings.Builder
	sb.WriteString("You are handling an OpenAI-style chat completion request.\n")
	sb.WriteString("Conversation transcript:\n")
	for _, msg := range req.Messages {
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "user"
		}
		sb.WriteString("\n[")
		sb.WriteString(role)
		sb.WriteString("]\n")
		if !rawJSONIsEmpty(msg.ToolCalls) {
			sb.WriteString("tool_calls: ")
			sb.Write(msg.ToolCalls)
			sb.WriteString("\n")
		}
		if msg.ToolCallID != "" {
			sb.WriteString("tool_call_id: ")
			sb.WriteString(msg.ToolCallID)
			sb.WriteString("\n")
		}
		if content := normalizeContent(msg.Content); content != "" {
			sb.WriteString(content)
			sb.WriteString("\n")
		}
	}
	sb.WriteString("\nRespond to the latest user request using the transcript above.")
	return sb.String()
}

func normalizeContent(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if stringValue(m["type"]) == "text" {
				parts = append(parts, stringValue(m["text"]))
			}
		}
		return strings.Join(parts, "\n")
	default:
		buf, _ := json.Marshal(v)
		return string(buf)
	}
}

func mustJSON(v any) string {
	if v == nil {
		return "{}"
	}
	buf, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(buf)
}
