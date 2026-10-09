package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type agentResponseWriter struct {
	http.ResponseWriter
	started bool
}

func (w *agentResponseWriter) WriteHeader(status int) {
	w.started = true
	w.ResponseWriter.WriteHeader(status)
}
func (w *agentResponseWriter) Write(data []byte) (int, error) {
	w.started = true
	return w.ResponseWriter.Write(data)
}
func (w *agentResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func setAgentResponseHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Notion-Protocol", "agent")
	w.Header().Set("X-Notion-Session-Mode", "history-replay")
	w.Header().Set("X-Notion-Streaming-Mode", "polling-buffered")
	w.Header().Set("X-Notion-Usage", "unavailable")
}

func agentErrorType(e *GatewayError) string {
	switch e.Kind {
	case "unknown_model":
		return "not_found_error"
	case "authentication":
		return "authentication_error"
	case "model_permission":
		return "permission_error"
	case "rate_limit", "quota":
		return "rate_limit_error"
	case "unsupported_capability", "unsupported_effort", "unsupported_protocol", "invalid_tool_history":
		return "invalid_request_error"
	case "agent_disabled", "no_account":
		return "overloaded_error"
	case "approval_required":
		return "approval_required"
	default:
		return "api_error"
	}
}

func handleAgentMessages(w http.ResponseWriter, r *http.Request, pool *AccountPool, req AnthropicRequest, model, requestID string, ask bool) {
	setAgentResponseHeaders(w)
	if req.Stream {
		if _, ok := w.(http.Flusher); !ok {
			writeAnthropicError(w, requestID, 500, "streaming not supported", "api_error")
			return
		}
	}
	tracked := &agentResponseWriter{ResponseWriter: w}
	w = tracked
	writeError := func(err error) {
		e := asGatewayError(err)
		writeAnthropicError(w, requestID, e.Status, e.Message, agentErrorType(e))
	}
	if !enhancedConfigSnapshot().Enabled {
		writeError(gatewayError("agent_disabled", 503, "Agent V3 is disabled; enable it in the dashboard", false))
		return
	}
	if req.Temperature != nil || req.TopP != nil || req.TopK != nil || len(req.StopSequences) > 0 || req.ContextManagement != nil {
		writeError(gatewayError("unsupported_capability", 400, "Agent V3 does not support sampling controls, stop sequences, or context_management", false))
		return
	}
	if thinking, ok := req.Thinking.(map[string]interface{}); ok && thinking["type"] != "disabled" {
		writeError(gatewayError("unsupported_capability", 400, "Agent V3 does not expose thinking blocks; use output_config.effort for supported reasoning levels", false))
		return
	}
	if req.OutputConfig != nil && req.OutputConfig.Format != nil && req.OutputConfig.Format.Type != "" {
		writeError(gatewayError("unsupported_capability", 400, "Agent V3 does not provide enforced structured output schemas", false))
		return
	}
	messages, attachments := convertAnthropicMessages(req.System, req.Messages)
	if len(attachments) > 0 || hasUnsupportedAgentBlocks(req.Messages) || ask || AppConfig.AskModeDefault() {
		writeError(gatewayError("unsupported_capability", 400, "Agent V3 currently accepts text and tool history; attachments and ASK mode are unsupported", false))
		return
	}
	if err := validateAgentHistory(messages); err != nil {
		writeError(err)
		return
	}
	workspaceSearch := AppConfig.WorkspaceSearchEnabled()
	if value := r.Header.Get("X-Workspace-Search"); value != "" {
		workspaceSearch = strings.EqualFold(value, "true") || value == "1"
	}
	if workspaceSearch {
		writeError(gatewayError("unsupported_capability", 400, "Agent workspace search override is unsupported", false))
		return
	}
	webSearch := AppConfig.WebSearchEnabled()
	if value := r.Header.Get("X-Web-Search"); value != "" {
		webSearch = strings.EqualFold(value, "true") || value == "1"
	}
	tools := make([]Tool, 0, len(req.Tools))
	for _, tool := range req.Tools {
		tools = append(tools, Tool{Type: "function", Function: ToolFunction{Name: tool.Name, Description: tool.Description, Parameters: tool.InputSchema}})
	}
	hasTools := len(tools) > 0
	choice, choiceErr := agentToolChoice(req.ToolChoice, tools)
	if choiceErr != nil {
		writeError(choiceErr)
		return
	}
	if hasTools {
		// nil Session forces the established full-history injection strategy.
		original := cloneChatMessages(messages)
		messages = injectToolsIntoMessages(cloneChatMessages(messages), tools, model, nil, req.ToolChoice)
		// The bridge may compact a large tool conversation to one prompt. Keep
		// complete supplied history and IDs alongside that prompt for replay.
		messages = append([]ChatMessage{{Role: "system", Content: "Historical context only; already executed calls must not be repeated.\n" + buildAgentHistoryReplay(original)}}, messages...)
	}
	ctx, cancel := context.WithTimeout(r.Context(), agentTimeout())
	defer cancel()
	options := CallOptions{Context: ctx, Protocol: "agent", AgentToolChoice: choice}
	if hasTools {
		options.AgentTools = tools
	}
	excluded := make(map[*Account]bool)
	var lastErr error
	for len(excluded) < pool.Count() {
		if ctx.Err() != nil {
			writeError(ctx.Err())
			return
		}
		acc := pool.NextAgentExcluding(excluded)
		if acc == nil {
			break
		}
		excluded[acc] = true
		var err error
		if req.Stream {
			err = handleAnthropicStream(w, acc, messages, model, requestID, hasTools, false, webSearch, nil, false, nil, req.OutputConfig, nil, options)
		} else {
			err = handleAnthropicNonStream(w, acc, messages, model, requestID, hasTools, false, webSearch, nil, false, nil, req.OutputConfig, nil, options)
		}
		// Preserve capability statistics in the shared account storage. These
		// writes never create a separate account pool or persist request text.
		if AppConfig.Server.AccountsDir != "" {
			_ = saveAccountFile(AppConfig.Server.AccountsDir, acc)
		}
		if err == nil {
			return
		}
		if tracked.started {
			if req.Stream && ctx.Err() == nil {
				e := asGatewayError(err)
				sendAnthropicSSE(w, tracked, "error", map[string]interface{}{"type": "error", "error": map[string]string{"type": agentErrorType(e), "message": e.Message}})
			}
			return // Never replay an account after client-visible output.
		}
		lastErr = err
		if !asGatewayError(err).Retryable {
			writeError(err)
			return
		}
	}
	if lastErr != nil {
		writeError(lastErr)
		return
	}
	writeError(gatewayError("no_account", 503, "no Agent account is available (empty pool or cooldown)", false))
}

func hasUnsupportedAgentBlocks(messages []AnthropicMessage) bool {
	for _, message := range messages {
		if blocks, ok := message.Content.([]interface{}); ok {
			for _, value := range blocks {
				block, ok := value.(map[string]interface{})
				if !ok {
					return true
				}
				kind, _ := block["type"].(string)
				switch kind {
				case "text", "tool_use", "tool_result", "thinking", "redacted_thinking":
				default:
					return true
				}
				if kind == "tool_result" {
					if nested, ok := block["content"].([]interface{}); ok {
						for _, item := range nested {
							b, ok := item.(map[string]interface{})
							if !ok || b["type"] != "text" {
								return true
							}
						}
					}
				}
			}
		}
	}
	return false
}

func validateAgentHistory(messages []ChatMessage) error {
	known := make(map[string]bool)
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			var args map[string]interface{}
			if json.Unmarshal([]byte(call.Function.Arguments), &args) != nil || args == nil {
				return gatewayError("invalid_tool_history", 400, "tool_use input must be an object", false)
			}
			if call.ID == "" || call.Function.Name == "" || known[call.ID] {
				return gatewayError("invalid_tool_history", 400, "tool history requires unique tool call IDs and names", false)
			}
			known[call.ID] = true
		}
		if message.Role == "tool" && (message.ToolCallID == "" || !known[message.ToolCallID]) {
			return gatewayError("invalid_tool_history", 400, "tool_result must reference an earlier tool_use ID", false)
		}
	}
	return nil
}

func agentToolChoice(choice interface{}, tools []Tool) (string, error) {
	if choice == nil {
		return "auto", nil
	}
	value := ""
	name := ""
	switch choice := choice.(type) {
	case string:
		value = choice
	case map[string]interface{}:
		value, _ = choice["type"].(string)
		name, _ = choice["name"].(string)
		if fn, ok := choice["function"].(map[string]interface{}); ok {
			name, _ = fn["name"].(string)
		}
	default:
		return "", gatewayError("unsupported_capability", 400, "unsupported Agent tool_choice format", false)
	}
	switch value {
	case "auto", "none":
		return value, nil
	case "required", "any":
		if len(tools) > 0 {
			return "any", nil
		}
	case "tool", "function":
		for _, tool := range tools {
			if name != "" && tool.Function.Name == name {
				return "named:" + name, nil
			}
		}
	}
	return "", gatewayError("unsupported_capability", 400, "tool_choice must reference a declared tool", false)
}
