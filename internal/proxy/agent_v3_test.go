package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func agentFixture(t *testing.T, handler http.HandlerFunc) (*AccountPool, *Account) {
	t.Helper()
	oldConfig, oldBase, oldClient := AppConfig, NotionAPIBase, agentHTTPClient
	AppConfig = DefaultConfig()
	AppConfig.AgentV3.Enabled = true
	AppConfig.AgentV3.PollIntervalMS = 1
	AppConfig.Server.AccountsDir = ""
	AppConfig.Proxy.EnableWebSearch = boolPtr(false)
	server := httptest.NewServer(handler)
	NotionAPIBase = server.URL + "/api/v3"
	agentHTTPClient = func(time.Duration) *http.Client { return server.Client() }
	t.Cleanup(func() { server.Close(); AppConfig, NotionAPIBase, agentHTTPClient = oldConfig, oldBase, oldClient })
	acc := &Account{TokenV2: "private-cookie-value", FullCookie: "token_v2=private-cookie-value", UserID: "user", SpaceID: "space", UserEmail: "a@example.test", ClientVersion: DefaultClientVersion}
	pool := NewAccountPool()
	pool.accounts = []*Account{acc}
	return pool, acc
}

func writeAgentCatalog(w http.ResponseWriter, enabled bool) {
	json.NewEncoder(w).Encode(map[string]interface{}{"models": []interface{}{map[string]interface{}{"model": "albuquerque-quinn", "modelMessage": "Opus 5.5", "isDisabled": !enabled, "supportedReasoningEfforts": []string{"low", "medium", "high"}}}})
}

func writeAgentCreated(w http.ResponseWriter) {
	w.Write([]byte(`{"thread":{"data":{"agent_provider":{"model":"albuquerque-quinn","reasoningEffort":"medium"}}}}`))
}

func writeAgentTranscript(w http.ResponseWriter, text, status string) {
	json.NewEncoder(w).Encode(map[string]interface{}{"patches": []interface{}{
		map[string]interface{}{"op": "put", "entity": map[string]interface{}{"id": "assistant-1", "kind": "assistant_message", "content": []interface{}{map[string]interface{}{"type": "text", "text": text}}}},
		map[string]interface{}{"op": "session", "session": map[string]string{"status": status}},
	}})
}

func TestAgentV3RevisionSafeCompletion(t *testing.T) {
	var polls atomic.Int32
	var requests []agentCreateRequest
	var mu sync.Mutex
	_, acc := agentFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("client Authorization leaked upstream")
		}
		if !strings.Contains(r.Header.Get("Cookie"), "private-cookie-value") {
			t.Error("missing shared account cookie")
		}
		switch r.URL.Path {
		case "/api/v3/getAvailableModels":
			writeAgentCatalog(w, true)
		case "/api/v3/createAgentThread":
			var req agentCreateRequest
			json.NewDecoder(r.Body).Decode(&req)
			mu.Lock()
			requests = append(requests, req)
			mu.Unlock()
			writeAgentCreated(w)
		case "/api/v3/getThreadTranscript":
			switch polls.Add(1) {
			case 1:
				writeAgentTranscript(w, "你好🙂 draft", "running")
			case 2:
				writeAgentTranscript(w, "你好🙂 corrected", "running")
			case 3, 4, 5, 6:
				writeAgentTranscript(w, "你好🙂 corrected", "idle")
			default:
				writeAgentTranscript(w, "你好🙂 final", "finished")
			}
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	var text strings.Builder
	doneCalls := 0
	err := CallInference(acc, []ChatMessage{{Role: "user", Content: "hello"}}, "agent/opus-5.5", false, func(delta string, done bool, usage *UsageInfo) {
		if polls.Load() < 7 {
			t.Error("mutable text was emitted before completion")
		}
		if usage != nil {
			t.Error("invented Agent token usage")
		}
		text.WriteString(delta)
		if done {
			doneCalls++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if text.String() != "你好🙂 final" || doneCalls != 1 {
		t.Fatalf("text=%q done=%d", text.String(), doneCalls)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("created %d threads", len(requests))
	}
	req := requests[0]
	if req.Type != "personal_agent" || req.Model != "albuquerque-quinn" || req.ReasoningEffort != "medium" || req.ThreadID == "" || req.ClientMessageID == "" || req.CreatedSource != "full_page_chat" {
		t.Fatalf("incorrect create request: %+v", req)
	}
	if req.EnableSuggestedEditsTools || req.Policies["approval_mode"] != "review_for_me" {
		t.Fatal("approval/edit policy changed")
	}
	if acc.agentSnapshot().Models[0].Authorization != "confirmed" {
		t.Fatal("successful inference did not confirm model capability")
	}
}

func TestAgentV3CancellationAndIdleTimeout(t *testing.T) {
	for _, mode := range []string{"idle", "cancelled", "blocked_http"} {
		t.Run(mode, func(t *testing.T) {
			var callbacks atomic.Int32
			_, acc := agentFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v3/getAvailableModels":
					writeAgentCatalog(w, true)
				case "/api/v3/createAgentThread":
					writeAgentCreated(w)
				default:
					if mode == "blocked_http" {
						io.Copy(io.Discard, r.Body)
						select {
						case <-r.Context().Done():
						case <-time.After(200 * time.Millisecond):
						}
						return
					}
					writeAgentTranscript(w, "partial must not be reported as success", "idle")
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			start := time.Now()
			err := CallAgentInference(acc, []ChatMessage{{Role: "user", Content: "hi"}}, "opus-5.5", false, func(string, bool, *UsageInfo) { callbacks.Add(1) }, CallOptions{Context: ctx})
			want := "timeout"
			if mode == "cancelled" {
				want = "cancelled"
			}
			if err == nil || asGatewayError(err).Kind != want {
				t.Fatalf("error=%v, want %s", err, want)
			}
			if time.Since(start) > time.Second || callbacks.Load() != 0 {
				t.Fatal("cancellation did not promptly release resources")
			}
			if acc.PermanentlyExhausted || !acc.agentSnapshot().CooldownUntil.IsZero() {
				t.Fatal("timeout/cancellation disabled a normal account")
			}
		})
	}
}

func TestAgentV3HTTPFailureClassificationAndRedaction(t *testing.T) {
	cases := []struct {
		status     int
		body, kind string
	}{
		{401, `{"message":"token_v2=secret"}`, "authentication"},
		{403, `{"message":"Authorization: Bearer secret"}`, "model_permission"},
		{403, `{"code":"quota_exhausted"}`, "quota"},
		{429, `{}`, "rate_limit"}, {500, `{}`, "upstream_error"}, {504, `{}`, "timeout"},
	}
	for _, tc := range cases {
		err := classifyAgentHTTPError(tc.status, []byte(tc.body))
		if err.Kind != tc.kind || strings.Contains(err.Error(), "secret") {
			t.Fatalf("classification/redaction failed: %+v", err)
		}
		acc := &Account{}
		acc.recordAgentResult(AgentModel{Slug: "albuquerque-quinn"}, err)
		if acc.PermanentlyExhausted || acc.QuotaExhaustedAt != nil {
			t.Fatal("Agent failure changed Workflow quota")
		}
	}
	redacted := redactSensitiveText(`{"token_v2":"secret-token","full_cookie":"secret-cookie","Authorization":"Bearer secret-auth"}` + "\nAPI Key: secret-key")
	if strings.Contains(redacted, "secret-") {
		t.Fatalf("credentials leaked: %s", redacted)
	}
}

func TestAgentV3ModelAuthorizationAndCache(t *testing.T) {
	var catalogCalls, createCalls atomic.Int32
	var enabled atomic.Bool
	_, acc := agentFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/getAvailableModels":
			catalogCalls.Add(1)
			writeAgentCatalog(w, enabled.Load())
		case "/api/v3/createAgentThread":
			createCalls.Add(1)
			writeAgentCreated(w)
		default:
			writeAgentTranscript(w, "ok", "finished")
		}
	})
	model, _ := lookupAgentModel("opus-5.5")
	if _, err := authorizeAgentModel(context.Background(), acc, model, "medium"); err == nil || asGatewayError(err).Kind != "model_permission" {
		t.Fatal("disabled catalog model was granted permission")
	}
	if _, err := authorizeAgentModel(context.Background(), acc, model, "medium"); err == nil || catalogCalls.Load() != 1 {
		t.Fatal("catalog caching failed")
	}
	enabled.Store(true)
	if err := FetchAgentModels(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	if _, err := authorizeAgentModel(context.Background(), acc, model, "max"); err == nil || asGatewayError(err).Status != 400 {
		t.Fatal("unsupported effort was silently clamped")
	}
	if _, err := authorizeAgentModel(context.Background(), acc, model, "low"); err != nil {
		t.Fatal(err)
	}
	if createCalls.Load() != 0 {
		t.Fatal("discovery unexpectedly ran an inference")
	}
	acc.updateAgentCapability(func(c *AgentCapability) { c.CheckedAt = time.Now().Add(-time.Hour) })
	if _, err := authorizeAgentModel(context.Background(), acc, model, "low"); err != nil {
		t.Fatal(err)
	}
	if catalogCalls.Load() != 3 {
		t.Fatal("expired discovery was not refreshed")
	}
}

func TestAgentV3ProtocolAccountIsolation(t *testing.T) {
	pool, acc := agentFixture(t, func(w http.ResponseWriter, r *http.Request) { writeAgentCatalog(w, true) })
	acc.PermanentlyExhausted = true
	if pool.NextAgentExcluding(nil) != acc {
		t.Fatal("Workflow exhaustion incorrectly blocks Agent")
	}
	acc.PermanentlyExhausted = false
	acc.updateAgentCapability(func(c *AgentCapability) { c.CooldownUntil = time.Now().Add(time.Hour) })
	if pool.NextAgentExcluding(nil) != nil {
		t.Fatal("Agent cooldown ignored")
	}
	if pool.NextBest() != acc {
		t.Fatal("Agent cooldown incorrectly blocks Workflow")
	}
	for _, name := range []string{"agent/unknown-opus", "workflow/opus-99", "opus-99", "other/opus-5.5"} {
		if _, err := resolveInferenceRoute(name); err == nil {
			t.Fatalf("unknown route accepted: %s", name)
		}
	}
	if got := ResolveModel("opus-99"); got != "opus-99" {
		t.Fatal("unknown version silently downgraded")
	}
	if route, err := resolveInferenceRoute("opus-4.6"); err != nil || route.Protocol != "workflow" {
		t.Fatal("legacy default changed")
	}
}

func TestAgentV3TranscriptParser(t *testing.T) {
	cases := []struct{ name, body, kind string }{
		{"missing", `{}`, "transcript_parse"},
		{"bad_put", `{"patches":[{"op":"put"}]}`, "transcript_parse"},
		{"bad_session", `{"patches":[{"op":"session"}]}`, "transcript_parse"},
		{"unknown", `{"patches":[{"op":"future_operation"}]}`, "transcript_parse"},
		{"pagination", `{"patches":[],"hasMore":true}`, "transcript_truncated"},
		{"failed", `{"patches":[{"op":"session","session":{"status":"failed"}}]}`, "upstream_error"},
		{"approval", `{"patches":[{"op":"session","session":{"status":"awaiting_approval"}}]}`, "approval_required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var response agentTranscriptResponse
			json.Unmarshal([]byte(tc.body), &response)
			_, _, err := (&agentTranscriptState{}).apply(response)
			if err == nil || asGatewayError(err).Kind != tc.kind {
				t.Fatalf("error=%v", err)
			}
		})
	}
	state := &agentTranscriptState{}
	for i, text := range []string{"错🙂", "对🙂", "对🙂 final"} {
		var response agentTranscriptResponse
		body, _ := json.Marshal(map[string]interface{}{"patches": []interface{}{map[string]interface{}{"op": "put", "entity": map[string]interface{}{"kind": "assistant_message", "content": []interface{}{map[string]interface{}{"type": "text", "text": [][]interface{}{{text, map[string]string{"annotation": "must not appear"}}}}}}}}})
		json.Unmarshal(body, &response)
		got, done, err := state.apply(response)
		if err != nil || done || got != text {
			t.Fatalf("poll %d text=%q err=%v", i, got, err)
		}
	}
	if state.revisions != 1 {
		t.Fatal("UTF-8 revision detection failed")
	}
}

func TestAgentV3AllAPIFormats(t *testing.T) {
	for _, endpoint := range []string{"anthropic", "chat", "responses"} {
		for _, stream := range []bool{false, true} {
			t.Run(endpoint+map[bool]string{true: "_stream", false: "_json"}[stream], func(t *testing.T) {
				pool, _ := agentFixture(t, func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/api/v3/getAvailableModels":
						writeAgentCatalog(w, true)
					case "/api/v3/createAgentThread":
						writeAgentCreated(w)
					default:
						writeAgentTranscript(w, "你好, Agent", "finished")
					}
				})
				var handler http.HandlerFunc
				var body interface{}
				switch endpoint {
				case "anthropic":
					handler = HandleAnthropicMessages(pool)
					body = map[string]interface{}{"model": "agent/opus-5.5", "stream": stream, "max_tokens": 100, "messages": []interface{}{map[string]string{"role": "user", "content": "hi"}}}
				case "chat":
					handler = HandleOpenAIChatCompletions(pool)
					body = map[string]interface{}{"model": "agent/opus-5.5", "stream": stream, "messages": []interface{}{map[string]string{"role": "user", "content": "hi"}}}
				case "responses":
					handler = HandleOpenAIResponses(pool)
					body = map[string]interface{}{"model": "agent/opus-5.5", "stream": stream, "input": "hi"}
				}
				data, _ := json.Marshal(body)
				rec := httptest.NewRecorder()
				req := httptest.NewRequest("POST", "/v1/"+endpoint, strings.NewReader(string(data)))
				handler(rec, req)
				if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Agent") || strings.Contains(rec.Body.String(), "private-cookie-value") {
					t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
				}
				if rec.Header().Get("X-Notion-Session-Mode") != "history-replay" || rec.Header().Get("X-Notion-Streaming-Mode") != "polling-buffered" {
					t.Fatalf("missing protocol transparency headers: %v", rec.Header())
				}
				if stream {
					if !strings.Contains(rec.Header().Get("Content-Type"), "text/event-stream") {
						t.Fatal("missing SSE content type")
					}
					terminal := map[string]string{"anthropic": "message_stop", "chat": "[DONE]", "responses": "response.completed"}[endpoint]
					if !strings.Contains(rec.Body.String(), terminal) {
						t.Fatalf("missing terminal %s", terminal)
					}
				} else {
					if !json.Valid(rec.Body.Bytes()) {
						t.Fatal("invalid response JSON")
					}
				}
			})
		}
	}
}

func TestAgentV3ToolHistoryAndMultiCalls(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{true: "stream", false: "json"}[stream], func(t *testing.T) {
			var createCount atomic.Int32
			var threadIDs []string
			var prompts []string
			var mu sync.Mutex
			pool, _ := agentFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v3/getAvailableModels":
					writeAgentCatalog(w, true)
				case "/api/v3/createAgentThread":
					var payload map[string]interface{}
					json.NewDecoder(r.Body).Decode(&payload)
					raw, _ := json.Marshal(payload["content"])
					mu.Lock()
					threadIDs = append(threadIDs, payload["threadId"].(string))
					prompts = append(prompts, string(raw))
					mu.Unlock()
					createCount.Add(1)
					writeAgentCreated(w)
				default:
					if createCount.Load() == 1 {
						writeAgentTranscript(w, "{\"name\":\"Read\",\"arguments\":{\"file_path\":\"a.go\"}}\n{\"name\":\"Bash\",\"arguments\":{\"command\":\"go test\"}}", "finished")
					} else {
						writeAgentTranscript(w, `{"name":"__done__","arguments":{"result":"tests passed"}}`, "finished")
					}
				}
			})
			tools := []interface{}{map[string]interface{}{"name": "Read", "input_schema": map[string]string{"type": "object"}}, map[string]interface{}{"name": "Bash", "input_schema": map[string]string{"type": "object"}}}
			body := map[string]interface{}{"model": "agent/opus-5.5", "max_tokens": 100, "stream": stream, "tools": tools, "messages": []interface{}{map[string]string{"role": "user", "content": "read a.go and run tests"}}}
			invoke := func() *httptest.ResponseRecorder {
				data, _ := json.Marshal(body)
				rec := httptest.NewRecorder()
				HandleAnthropicMessages(pool)(rec, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(string(data))))
				return rec
			}
			first := invoke()
			if first.Code != 200 || !strings.Contains(first.Body.String(), "tool_use") {
				t.Fatalf("first=%s", first.Body.String())
			}
			var calls []AnthropicContentBlock
			if stream {
				for _, line := range strings.Split(first.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					var frame struct {
						ContentBlock AnthropicContentBlock `json:"content_block"`
					}
					json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame)
					if frame.ContentBlock.Type == "tool_use" {
						calls = append(calls, frame.ContentBlock)
					}
				}
			} else {
				var response AnthropicResponse
				json.Unmarshal(first.Body.Bytes(), &response)
				calls = response.Content
			}
			if len(calls) != 2 || calls[0].ID == "" || calls[0].ID == calls[1].ID {
				t.Fatalf("invalid multi tool calls: %+v", calls)
			}
			assistant := []interface{}{}
			results := []interface{}{}
			for _, call := range calls {
				assistant = append(assistant, map[string]interface{}{"type": "tool_use", "id": call.ID, "name": call.Name, "input": map[string]interface{}{}})
				results = append(results, map[string]interface{}{"type": "tool_result", "tool_use_id": call.ID, "content": "result-ok"})
			}
			body["messages"] = []interface{}{map[string]string{"role": "user", "content": "read a.go and run tests"}, map[string]interface{}{"role": "assistant", "content": assistant}, map[string]interface{}{"role": "user", "content": results}}
			second := invoke()
			if second.Code != 200 || !strings.Contains(second.Body.String(), "tests passed") || strings.Contains(second.Body.String(), `"stop_reason":"tool_use"`) {
				t.Fatalf("second=%s", second.Body.String())
			}
			mu.Lock()
			defer mu.Unlock()
			if len(threadIDs) != 2 || threadIDs[0] == threadIDs[1] {
				t.Fatal("History Replay reused a native thread")
			}
			for _, call := range calls {
				if !strings.Contains(prompts[1], call.ID) {
					t.Fatal("history lost tool call ID")
				}
			}
		})
	}
}

func TestAgentV3StrictToolValidation(t *testing.T) {
	tools := []Tool{{Function: ToolFunction{Name: "Bash"}}}
	for _, text := range []string{`{"name":"Bash"}`, `{"name":"Bash","arguments":null}`, `{"name":"Bash","arguments":[]}`, `{"name":"Unknown","arguments":{}}`, `{"name":"Bash","arguments":`, `{"name":"__done__","arguments":{}}`} {
		if err := validateAgentToolCalls(text, tools); err == nil {
			t.Fatalf("invalid tool accepted: %s", text)
		}
	}
	for _, text := range []string{"ordinary model text", `{"name":"Bash","arguments":{"command":"pwd"}}`, `{"name":"__done__","arguments":{"result":"done"}}`} {
		if err := validateAgentToolCalls(text, tools); err != nil {
			t.Fatalf("valid output rejected: %v", err)
		}
	}
	if err := validateAgentToolCalls("ordinary model text", tools, "any"); err == nil {
		t.Fatal("required tool call was disguised as successful plain text")
	}
	if err := validateAgentToolCalls(`{"name":"Bash","arguments":{}}`, tools, "none"); err == nil {
		t.Fatal("tool_choice=none was ignored")
	}
	if err := validateAgentToolCalls(`{"name":"Bash","arguments":{}}`, tools, "named:Bash"); err != nil {
		t.Fatalf("required named tool rejected: %v", err)
	}
}
