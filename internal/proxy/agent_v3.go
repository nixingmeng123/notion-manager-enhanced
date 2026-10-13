package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	jsonpatch "github.com/evanphx/json-patch/v5"
)

// Production uses the existing proxy-aware Chrome TLS transport. Tests replace
// only this factory with an httptest client, leaving request construction intact.
var agentHTTPClient = getChromeHTTPClient

const agentMaxResponseBytes = 8 << 20

func agentPostJSON(ctx context.Context, acc *Account, endpoint string, payload interface{}, result interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return gatewayError("invalid_request", 400, "invalid Agent request", false)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, NotionAPIBase+"/"+endpoint, bytes.NewReader(body))
	if err != nil {
		return gatewayError("configuration", 500, "invalid upstream API configuration", false)
	}
	setNotionHeadersJSON(req, acc)
	// Avoid decompression ambiguity and do not forward any client Authorization.
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := agentHTTPClient(AppConfig.APITimeoutDuration()).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return asGatewayError(ctx.Err())
		}
		var timeout interface{ Timeout() bool }
		if errors.As(err, &timeout) && timeout.Timeout() {
			return gatewayError("timeout", 504, "Agent upstream HTTP request timed out", false)
		}
		return gatewayError("upstream_error", 502, "Agent upstream HTTP request failed", true)
	}
	defer resp.Body.Close()
	reader, cleanup, err := decompressBody(resp)
	if err != nil {
		return gatewayError("transcript_parse", 502, "unsupported upstream response encoding", false)
	}
	if cleanup != nil {
		defer cleanup()
	}
	data, err := io.ReadAll(io.LimitReader(reader, agentMaxResponseBytes+1))
	if ctx.Err() != nil {
		return asGatewayError(ctx.Err())
	}
	if err != nil || len(data) > agentMaxResponseBytes {
		return gatewayError("transcript_parse", 502, "upstream response could not be read within the size limit", false)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return classifyAgentHTTPError(resp.StatusCode, data, endpoint)
	}
	if !utf8.Valid(data) {
		return gatewayError("transcript_parse", 502, "upstream response contains invalid UTF-8", false)
	}
	if result != nil && json.Unmarshal(data, result) != nil {
		return gatewayError("transcript_parse", 502, "upstream returned malformed JSON", false)
	}
	return nil
}

func classifyAgentHTTPError(status int, body []byte, endpoints ...string) (result *GatewayError) {
	defer func() {
		// Only fixed operation names and the HTTP status are exposed. Do not
		// echo a URL, upstream error text, header or arbitrary error code.
		operation := "Agent API"
		if len(endpoints) > 0 {
			switch endpoints[0] {
			case "getAvailableModels", "createAgentThread", "getThreadTranscript", "createAgentServiceFileUploadURL", "completeAgentServiceFileUpload", "getCreditRateLimitStatus", "getPersonalAgentCreditUsage":
				operation = endpoints[0]
			}
		}
		result.Message += " (" + operation + ", upstream HTTP " + strconv.Itoa(status) + ")"
	}()
	// Inspect only for classification. Never put the upstream body in an error
	// string or log, even when the upstream echoes cookies/Authorization.
	var detail struct {
		Name    string `json:"name"`
		Code    string `json:"code"`
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	_ = json.Unmarshal(body, &detail)
	code := strings.ToLower(detail.Name + " " + detail.Code + " " + detail.Message + " " + detail.Error)
	switch {
	case status == 401:
		return gatewayError("authentication", 401, "Notion login credentials were rejected", true)
	case status == 429 || strings.Contains(code, "rate_limit") || strings.Contains(code, "ratelimited") || strings.Contains(code, "rate limit"):
		return gatewayError("rate_limit", 429, "Notion Agent rate limit exceeded", true)
	case strings.Contains(code, "quota") || strings.Contains(code, "usage limit") || strings.Contains(code, "credit"):
		return gatewayError("quota", 429, "Notion Agent quota is exhausted", true)
	case status == 403:
		return gatewayError("model_permission", 403, "Notion rejected this Agent model or capability", true)
	case status == 408 || status == 504:
		return gatewayError("timeout", 504, "Notion Agent request timed out", false)
	case status >= 500:
		return gatewayError("upstream_error", 502, "Notion Agent is temporarily unavailable", true)
	default:
		return gatewayError("upstream_request", 502, "Notion rejected the Agent protocol request", false)
	}
}

type agentCreateRequest struct {
	Type                      string            `json:"type"`
	SpaceID                   string            `json:"spaceId"`
	ThreadID                  string            `json:"threadId"`
	Model                     string            `json:"model"`
	ReasoningEffort           string            `json:"reasoningEffort,omitempty"`
	CreatedSource             string            `json:"createdSource"`
	Content                   []agentTextBlock  `json:"content"`
	ClientMessageID           string            `json:"clientMessageId"`
	AgentMemorySettings       map[string]bool   `json:"agentMemorySettings"`
	Policies                  map[string]string `json:"policies"`
	BrowserEnabled            bool              `json:"browserEnabled"`
	EnableSuggestedEditsTools bool              `json:"enableSuggestedEditsTools"`
}

type agentTextBlock struct {
	Type   string      `json:"type"`
	Text   interface{} `json:"text,omitempty"`
	FileID string      `json:"file_id,omitempty"`
}

type agentEntity struct {
	ID         string           `json:"id"`
	Kind       string           `json:"kind"`
	Content    []agentTextBlock `json:"content"`
	Source     string           `json:"source"`
	StopReason string           `json:"stop_reason"`
	raw        json.RawMessage
}

func (e *agentEntity) UnmarshalJSON(data []byte) error {
	type plainEntity agentEntity
	var decoded plainEntity
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*e = agentEntity(decoded)
	e.raw = append(json.RawMessage(nil), data...)
	return nil
}

type agentTranscriptPatch struct {
	Op      string          `json:"op"`
	ID      string          `json:"id"`
	Entity  *agentEntity    `json:"entity"`
	Ops     json.RawMessage `json:"ops"`
	Session *struct {
		Status string `json:"status"`
	} `json:"session"`
}

type agentTranscriptResponse struct {
	Patches         *[]agentTranscriptPatch `json:"patches"`
	HasMore         bool                    `json:"hasMore"`
	NextCursor      json.RawMessage         `json:"nextCursor"`
	HasMoreBackward bool                    `json:"has_more_backward"`
	HasMoreForward  bool                    `json:"has_more_forward"`
}

type agentTranscriptState struct {
	entities      map[string]string
	order         []string
	previous      string
	revisions     int
	status        string
	turnCompleted bool
	documents     map[string]json.RawMessage
	documentBytes int
}

func (s *agentTranscriptState) removeEntity(id string) {
	s.removeAssistantText(id)
	s.documentBytes -= len(s.documents[id])
	delete(s.documents, id)
}

func (s *agentTranscriptState) removeAssistantText(id string) {
	delete(s.entities, id)
	order := s.order[:0]
	for _, existing := range s.order {
		if existing != id {
			order = append(order, existing)
		}
	}
	s.order = order
}

func (s *agentTranscriptState) putEntity(entity *agentEntity, patchID string, ordinal int) error {
	id := entity.ID
	if id == "" {
		id = patchID
	}
	if id == "" && entity.Kind == "assistant_message" {
		id = "snapshot-" + strconv.Itoa(ordinal)
	}
	if id != "" {
		data := entity.raw
		if len(data) == 0 {
			data, _ = json.Marshal(entity)
		}
		size := s.documentBytes - len(s.documents[id]) + len(data)
		if size > agentMaxResponseBytes {
			return gatewayError("transcript_parse", 502, "Agent transcript entity state exceeds the size limit", false)
		}
		s.documents[id], s.documentBytes = data, size
	}
	if entity.Kind != "assistant_message" {
		s.removeAssistantText(id)
	}
	switch entity.Kind {
	case "approval", "approval_request":
		return gatewayError("approval_required", 409, "Agent requires user approval in Notion; no operation was approved by the gateway", false)
	case "turn_failed", "turn_error":
		return gatewayError("upstream_error", 502, "Agent turn failed", true)
	case "turn_completed":
		if entity.Source == "committed" {
			if entity.StopReason != "end_turn" {
				return gatewayError("unsupported_capability", 502, "Agent returned an unsupported turn completion reason", false)
			}
			s.turnCompleted = true
		}
		return nil
	}
	if entity.Kind != "assistant_message" {
		// Tool results and thinking are transcript state, never assistant text.
		return nil
	}
	var blocks []string
	for _, block := range entity.Content {
		if block.Type != "text" {
			continue
		}
		text, err := agentPlainText(block.Text)
		if err != nil {
			return err
		}
		blocks = append(blocks, text)
	}
	if _, exists := s.entities[id]; !exists {
		s.order = append(s.order, id)
	}
	s.entities[id] = strings.Join(blocks, "\n")
	return nil
}

func (s *agentTranscriptState) patchedEntity(patch agentTranscriptPatch) (*agentEntity, error) {
	data, exists := s.documents[patch.ID]
	ops := bytes.TrimSpace(patch.Ops)
	if patch.ID == "" || !exists || len(ops) == 0 || ops[0] != '[' {
		return nil, gatewayError("transcript_parse", 502, "Agent entity patch is missing its target or operations", false)
	}
	operations, err := jsonpatch.DecodePatch(patch.Ops)
	if err != nil || len(operations) > 1024 {
		return nil, gatewayError("transcript_parse", 502, "Agent entity patch has invalid operations", false)
	}
	options := &jsonpatch.ApplyOptions{SupportNegativeIndices: false, AccumulatedCopySizeLimit: agentMaxResponseBytes}
	updated, err := operations.ApplyWithOptions(data, options)
	if err != nil || len(updated) > agentMaxResponseBytes || !utf8.Valid(updated) {
		return nil, gatewayError("transcript_parse", 502, "Agent entity patch could not be applied", false)
	}
	var entity agentEntity
	if json.Unmarshal(updated, &entity) != nil || entity.ID != patch.ID || entity.Kind == "" {
		return nil, gatewayError("transcript_parse", 502, "Agent entity patch produced an invalid entity", false)
	}
	return &entity, nil
}

// Agent text blocks are strings in the observed transcript. Some Notion rich
// text variants use [[text, annotations], ...]; annotations are not text.
func agentPlainText(raw interface{}) (string, error) {
	switch value := raw.(type) {
	case nil:
		return "", nil
	case string:
		if !utf8.ValidString(value) {
			return "", gatewayError("transcript_parse", 502, "Agent transcript contains invalid UTF-8", false)
		}
		return value, nil
	case []interface{}:
		var b strings.Builder
		for _, segment := range value {
			switch part := segment.(type) {
			case string:
				b.WriteString(part)
			case []interface{}:
				if len(part) > 0 {
					text, ok := part[0].(string)
					if !ok {
						return "", gatewayError("transcript_parse", 502, "unsupported Agent rich text", false)
					}
					b.WriteString(text)
				}
			default:
				return "", gatewayError("transcript_parse", 502, "unsupported Agent rich text", false)
			}
		}
		return b.String(), nil
	default:
		return "", gatewayError("transcript_parse", 502, "unsupported Agent text block", false)
	}
}

func (s *agentTranscriptState) apply(response agentTranscriptResponse) (string, bool, error) {
	if response.Patches == nil {
		return "", false, gatewayError("transcript_parse", 502, "Agent transcript is missing patches", false)
	}
	if response.HasMore || response.HasMoreBackward || response.HasMoreForward || (len(response.NextCursor) > 0 && string(response.NextCursor) != "null" && string(response.NextCursor) != `""`) {
		return "", false, gatewayError("transcript_truncated", 502, "paginated Agent transcripts are not supported", false)
	}
	if s.entities == nil {
		s.entities = make(map[string]string)
	}
	if s.documents == nil {
		s.documents = make(map[string]json.RawMessage)
	}
	// Anonymous entities are complete snapshots in the reference protocol.
	// Reset only this snapshot namespace; keyed incremental entities survive.
	anonymousSnapshot := false
	for _, patch := range *response.Patches {
		if patch.Op == "put" && patch.Entity != nil && patch.Entity.Kind == "assistant_message" && patch.Entity.ID == "" && patch.ID == "" {
			anonymousSnapshot = true
		}
	}
	if anonymousSnapshot {
		var order []string
		for _, id := range s.order {
			if strings.HasPrefix(id, "snapshot-") {
				delete(s.entities, id)
				s.documentBytes -= len(s.documents[id])
				delete(s.documents, id)
			} else {
				order = append(order, id)
			}
		}
		s.order = order
	}
	ordinal := 0
	for _, patch := range *response.Patches {
		switch patch.Op {
		case "committed":
			// Sequence acknowledgement observed in the live Agent service.
			continue
		case "session":
			if patch.Session == nil {
				return "", false, gatewayError("transcript_parse", 502, "Agent session patch is missing session data", false)
			}
			s.status = strings.ToLower(patch.Session.Status)
		case "put", "patch":
			entity := patch.Entity
			if patch.Op == "patch" {
				var err error
				entity, err = s.patchedEntity(patch)
				if err != nil {
					return "", false, err
				}
			}
			if entity == nil {
				return "", false, gatewayError("transcript_parse", 502, "Agent put patch is missing entity data", false)
			}
			if entity.Kind == "assistant_message" {
				ordinal++
			}
			if err := s.putEntity(entity, patch.ID, ordinal); err != nil {
				return "", false, err
			}
		case "delete", "remove":
			id := patch.ID
			if patch.Entity != nil && patch.Entity.ID != "" {
				id = patch.Entity.ID
			}
			s.removeEntity(id)
		case "approval", "approval_request":
			return "", false, gatewayError("approval_required", 409, "Agent requires user approval in Notion; no operation was approved by the gateway", false)
		default:
			return "", false, gatewayError("transcript_parse", 502, "unsupported Agent transcript patch operation", false)
		}
	}
	var parts []string
	for _, id := range s.order {
		if text := s.entities[id]; text != "" {
			parts = append(parts, text)
		}
	}
	text := strings.Join(parts, "\n")
	if longestCommonPrefixUTF8(s.previous, text) < len(s.previous) {
		s.revisions++
	}
	s.previous = text
	switch s.status {
	case "failed", "error":
		return "", false, gatewayError("upstream_error", 502, "Agent session failed", true)
	case "cancelled", "canceled":
		return "", false, gatewayError("upstream_cancelled", 502, "Agent session was cancelled upstream", false)
	case "awaiting_approval", "pending_approval", "requires_approval", "waiting_for_approval":
		return "", false, gatewayError("approval_required", 409, "Agent requires user approval in Notion; no operation was approved by the gateway", false)
	case "finished", "complete", "completed", "done":
		return text, true, nil
	default:
		// A committed turn_completed event is the completion marker observed
		// live. Idle/stable text by themselves remain insufficient.
		return text, s.turnCompleted, nil
	}
}

func buildAgentHistoryReplay(messages []ChatMessage) string {
	var b strings.Builder
	b.WriteString("Conversation context (history replay):\n")
	for _, message := range messages {
		b.WriteString("\n[" + message.Role + "]\n")
		b.WriteString(message.Content)
		for _, call := range message.ToolCalls {
			data, _ := json.Marshal(map[string]interface{}{"tool_call_id": call.ID, "name": call.Function.Name, "arguments": json.RawMessage(call.Function.Arguments)})
			b.WriteString("\n" + string(data))
		}
		if message.ToolCallID != "" {
			b.WriteString("\ntool_call_id=" + message.ToolCallID)
		}
	}
	return b.String()
}

// CallAgentInference polls mutable cumulative transcripts. Because the observed
// protocol has no immutable prefix marker, text is buffered until explicit
// completion, then sent as UTF-8 safe deltas. This is polling-based buffered SSE,
// never upstream token streaming. No Workflow Session or thread ID is reused.
func CallAgentInference(acc *Account, messages []ChatMessage, name string, disableBuiltinTools bool, cb StreamCallback, opts ...CallOptions) (resultErr error) {
	if !enhancedConfigSnapshot().Enabled {
		return gatewayError("agent_disabled", 503, "Agent V3 is disabled; enable it in the dashboard", false)
	}
	model, ok := lookupAgentModel(strings.TrimPrefix(name, "agent/"))
	if !ok {
		return gatewayError("unknown_model", 404, "unknown Agent model", false)
	}
	if acc == nil || acc.SpaceID == "" || acc.UserID == "" || (acc.TokenV2 == "" && acc.FullCookie == "") {
		return gatewayError("authentication", 401, "account credentials and workspace are required", true)
	}
	defer func() { acc.recordAgentResult(model, resultErr) }()
	var opt CallOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	if len(opt.Attachments) > 0 || opt.IsResearcher || opt.UseReadOnlyMode || (opt.EnableWorkspaceSearch != nil && *opt.EnableWorkspaceSearch) {
		return gatewayError("unsupported_capability", 400, "Agent V3 does not support Workflow attachments, researcher, ASK mode, or workspace search overrides", false)
	}
	ctx := opt.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, agentTimeout())
	defer cancel()
	effort, err := normalizeReasoningEffort(opt.ReasoningEffort)
	if err != nil {
		return gatewayError("unsupported_effort", 400, "unsupported Agent reasoning effort", false)
	}
	effort, err = authorizeAgentModel(ctx, acc, model, effort)
	if err != nil {
		return err
	}
	explicitEffort := strings.TrimSpace(opt.ReasoningEffort) != ""
	if !explicitEffort {
		// Absence is an upstream default, not a client demand for medium.
		effort = ""
	}
	threadID := generateUUIDv4()
	history := buildAgentHistoryReplay(messages)
	if len(opt.AgentTools) == 0 {
		history += "\n\nAPI client delivery format: This client receives assistant text only and cannot open Notion session-file:// embeds or files in your sandbox. When the user asks for HTML, SVG, CSS, JavaScript, or other source code, include the complete requested source in fenced code blocks in your final assistant message. A file link or embed alone does not deliver the requested code to this client. For other questions, answer normally in text."
	}
	payload := agentCreateRequest{
		Type: "personal_agent", SpaceID: acc.SpaceID, ThreadID: threadID, Model: model.Slug,
		ReasoningEffort: effort, CreatedSource: "full_page_chat", ClientMessageID: generateUUIDv4(),
		Content:             []agentTextBlock{{Type: "text", Text: [][]string{{history}}}},
		AgentMemorySettings: map[string]bool{"useMemories": false, "excludeChatFromMemories": true},
		Policies:            map[string]string{"approval_mode": "review_for_me"},
		// The Workflow prompt toggle does not control Agent browser access.
		// The Agent search setting/header is authoritative for this capability.
		BrowserEnabled:            opt.EnableWebSearch,
		EnableSuggestedEditsTools: false,
	}
	var created map[string]interface{}
	if err := waitAgentRequestSlot(ctx, acc); err != nil {
		return err
	}
	if len(opt.AgentImages) > agentMaxImages {
		return gatewayError("unsupported_capability", 400, "Agent requests accept at most 19 images", false)
	}
	for _, image := range opt.AgentImages {
		block, err := uploadAgentImage(ctx, acc, image)
		if err != nil {
			return err
		}
		payload.Content = append(payload.Content, block)
	}
	if err := agentPostJSON(ctx, acc, "createAgentThread", payload, &created); err != nil {
		return err
	}
	if value, exists := created["success"].(bool); exists && !value {
		return gatewayError("upstream_request", 502, "Agent thread creation was rejected", false)
	}
	if _, exists := created["error"]; exists {
		return gatewayError("upstream_request", 502, "Agent thread creation returned an error", false)
	}
	modelVerified := false
	if thread, ok := created["thread"].(map[string]interface{}); ok {
		if data, ok := thread["data"].(map[string]interface{}); ok {
			if provider, ok := data["agent_provider"].(map[string]interface{}); ok {
				actualModel, _ := provider["model"].(string)
				actualEffort, _ := provider["reasoningEffort"].(string)
				selection, _ := provider["modelSelection"].(string)
				acc.recordAgentModelSelection(model, actualModel, actualEffort, selection)
				if actual, ok := provider["model"].(string); ok && actual == model.Slug {
					modelVerified = true
				}
				if actual, ok := provider["model"].(string); ok && actual != "" && actual != model.Slug {
					return gatewayError("model_permission", 403, "Notion selected a different model than requested; this account cannot honor the selected model", true)
				}
				if explicitEffort && actualEffort == "" {
					return gatewayError("model_effort_unverified", 502, "Agent creation response did not identify the requested reasoning effort", false)
				}
				if explicitEffort && actualEffort != effort {
					return gatewayError("unsupported_effort", 400, "Notion selected a different reasoning effort than requested", false)
				}
			}
		}
	}
	if !modelVerified {
		return gatewayError("model_identity_unverified", 502, "Agent creation response did not identify the actual model", false)
	}
	state := &agentTranscriptState{}
	poll := time.Duration(enhancedConfigSnapshot().PollIntervalMS) * time.Millisecond
	for {
		if ctx.Err() != nil {
			return asGatewayError(ctx.Err())
		}
		var transcript agentTranscriptResponse
		if err := agentPostJSON(ctx, acc, "getThreadTranscript", map[string]interface{}{"spaceId": acc.SpaceID, "threadId": threadID, "direction": "backward", "limit": 100}, &transcript); err != nil {
			return err
		}
		text, done, err := state.apply(transcript)
		if err != nil {
			return err
		}
		if done {
			if strings.TrimSpace(text) == "" {
				return gatewayError("empty_response", 502, "Agent completed without an assistant text message", true)
			}
			if opt.AgentTools != nil {
				if err := validateAgentToolCalls(text, opt.AgentTools, opt.AgentToolChoice); err != nil {
					return err
				}
			}
			// Agent transcript has no verified token counts. nil usage prevents
			// estimated counts from being recorded as actual usage statistics.
			remaining := []rune(text)
			for len(remaining) > 0 {
				if ctx.Err() != nil {
					return asGatewayError(ctx.Err())
				}
				count := min(512, len(remaining))
				if cb != nil {
					cb(string(remaining[:count]), false, nil)
				}
				remaining = remaining[count:]
			}
			if cb != nil {
				cb("", true, nil)
			}
			return nil
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return asGatewayError(ctx.Err())
		case <-timer.C:
		}
	}
}
