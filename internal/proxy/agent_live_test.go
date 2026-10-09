package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

type liveTraceTransport struct {
	base        http.RoundTripper
	path, token string
}

func (r *liveTraceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := r.base.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, agentMaxResponseBytes+1))
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(strings.NewReader(string(data)))
	clean := redactSensitiveText(strings.ReplaceAll(string(data), r.token, "[REDACTED]"))
	file, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err == nil {
		_ = json.NewEncoder(file).Encode(map[string]interface{}{"endpoint": req.URL.Path, "status": resp.StatusCode, "body": json.RawMessage(clean)})
		_ = file.Close()
	}
	return resp, nil
}

// Explicitly opt in with NOTION_LIVE_ACCOUNT_FILE and NOTION_LIVE_AGENT_MODEL.
// Never emits a cookie, token, request header, or account JSON. Normal tests
// remain offline and this test never approves a Notion operation.
func TestAgentV3LiveAccount(t *testing.T) {
	path := os.Getenv("NOTION_LIVE_ACCOUNT_FILE")
	if path == "" {
		t.Skip("live account not supplied")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("cannot read live account file")
	}
	var acc Account
	if json.Unmarshal(data, &acc) != nil || acc.TokenV2 == "" || acc.SpaceID == "" {
		t.Fatal("incomplete live account")
	}
	previous := AppConfig
	AppConfig = DefaultConfig()
	AppConfig.AgentV3.Enabled = true
	AppConfig.AgentV3.PollIntervalMS = 1000
	AppConfig.AgentV3.TimeoutSeconds = 60
	t.Cleanup(func() { AppConfig = previous })
	if tracePath := os.Getenv("NOTION_LIVE_TRACE_FILE"); tracePath != "" {
		previousClient := agentHTTPClient
		agentHTTPClient = func(timeout time.Duration) *http.Client {
			base := getChromeHTTPClient(timeout)
			copy := *base
			copy.Transport = &liveTraceTransport{base: base.Transport, path: tracePath, token: acc.TokenV2}
			return &copy
		}
		t.Cleanup(func() { agentHTTPClient = previousClient })
	}
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	var catalog map[string]json.RawMessage
	if err := agentPostJSON(ctx, &acc, "getAvailableModels", map[string]string{"spaceId": acc.SpaceID}, &catalog); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(catalog))
	for key := range catalog {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	t.Logf("catalog keys=%v", keys)
	var entries []map[string]json.RawMessage
	if json.Unmarshal(catalog["models"], &entries) == nil {
		t.Logf("catalog entries=%d", len(entries))
		for _, m := range entries {
			var slug, name string
			_ = json.Unmarshal(m["model"], &slug)
			_ = json.Unmarshal(m["modelMessage"], &name)
			t.Logf("catalog model=%q name=%q", slug, name)
		}
	}
	model := os.Getenv("NOTION_LIVE_AGENT_MODEL")
	if model == "" {
		return
	}
	var output strings.Builder
	err = CallAgentInference(&acc, []ChatMessage{{Role: "user", Content: "Connectivity test: reply with exactly AGENT_OK. Do not use any tools or modify any workspace content."}}, model, true,
		func(text string, _ bool, _ *UsageInfo) { output.WriteString(text) }, CallOptions{Context: ctx})
	t.Logf("inference model=%q output=%q", model, output.String())
	if err != nil {
		t.Fatalf("inference kind=%s status=%d: %s", asGatewayError(err).Kind, asGatewayError(err).Status, err)
	}
}

// Diagnostic-only matrix: a substitution is recorded under its actual slug,
// never returned to an API client under the requested alias. Enable explicitly
// with NOTION_LIVE_MODEL_MATRIX=1. A restricted catalog is recorded as evidence;
// no selection policy or approval setting is overridden.
func TestAgentV3LiveModelIdentity(t *testing.T) {
	if os.Getenv("NOTION_LIVE_MODEL_MATRIX") != "1" {
		t.Skip("live model matrix not enabled")
	}
	path := os.Getenv("NOTION_LIVE_ACCOUNT_FILE")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("cannot read live account file")
	}
	var acc Account
	if json.Unmarshal(data, &acc) != nil || acc.TokenV2 == "" || acc.SpaceID == "" {
		t.Fatal("incomplete live account")
	}
	previous := AppConfig
	AppConfig = DefaultConfig()
	AppConfig.AgentV3.Enabled = true
	t.Cleanup(func() { AppConfig = previous })
	type observation struct {
		Alias         string `json:"requested_alias"`
		Requested     string `json:"requested_slug"`
		Actual        string `json:"actual_slug,omitempty"`
		ActualEffort  string `json:"actual_effort,omitempty"`
		Selection     string `json:"model_selection,omitempty"`
		IdentityMatch bool   `json:"identity_match"`
		Completed     bool   `json:"completed"`
		Output        string `json:"output,omitempty"`
		ErrorKind     string `json:"error_kind,omitempty"`
	}
	var catalog struct {
		Models     []json.RawMessage `json:"models"`
		Restricted bool              `json:"modelSelectionRestricted"`
	}
	if err := agentPostJSON(t.Context(), &acc, "getAvailableModels", map[string]string{"spaceId": acc.SpaceID}, &catalog); err != nil {
		t.Fatal(err)
	}
	var observations []observation
	for _, alias := range []string{"opus-5.5", "gpt-6.1-sol", "kimi-k3"} {
		model, ok := lookupAgentModel(alias)
		if !ok {
			t.Fatalf("missing candidate %s", alias)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		threadID := generateUUIDv4()
		effort, _ := selectAgentEffort(model.Efforts, "")
		payload := agentCreateRequest{Type: "personal_agent", SpaceID: acc.SpaceID, ThreadID: threadID, Model: model.Slug, ReasoningEffort: effort,
			CreatedSource: "full_page_chat", ClientMessageID: generateUUIDv4(),
			Content:             []agentTextBlock{{Type: "text", Text: [][]string{{"Model routing verification. Reply with exactly MODEL_CHECK_OK. Do not use tools or modify any pages."}}}},
			AgentMemorySettings: map[string]bool{"useMemories": false, "excludeChatFromMemories": true}, Policies: map[string]string{"approval_mode": "review_for_me"}, BrowserEnabled: false, EnableSuggestedEditsTools: false}
		row := observation{Alias: alias, Requested: model.Slug}
		var created struct {
			Thread struct {
				Data struct {
					Provider struct {
						Model     string `json:"model"`
						Effort    string `json:"reasoningEffort"`
						Selection string `json:"modelSelection"`
					} `json:"agent_provider"`
				} `json:"data"`
			} `json:"thread"`
		}
		err := agentPostJSON(ctx, &acc, "createAgentThread", payload, &created)
		if err == nil {
			row.Actual = created.Thread.Data.Provider.Model
			row.ActualEffort = created.Thread.Data.Provider.Effort
			row.Selection = created.Thread.Data.Provider.Selection
			row.IdentityMatch = row.Actual == row.Requested
			state := &agentTranscriptState{}
			for ctx.Err() == nil {
				var transcript agentTranscriptResponse
				err = agentPostJSON(ctx, &acc, "getThreadTranscript", map[string]interface{}{"spaceId": acc.SpaceID, "threadId": threadID, "direction": "backward", "limit": 100}, &transcript)
				if err != nil {
					break
				}
				text, done, parseErr := state.apply(transcript)
				if parseErr != nil {
					err = parseErr
					break
				}
				if done {
					row.Completed = true
					row.Output = text
					break
				}
				timer := time.NewTimer(time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
				case <-timer.C:
				}
			}
			if !row.Completed && err == nil {
				err = ctx.Err()
			}
		}
		if err != nil {
			row.ErrorKind = asGatewayError(err).Kind
		}
		cancel()
		observations = append(observations, row)
		encoded, _ := json.Marshal(row)
		t.Log(string(encoded))
	}
	if reportPath := os.Getenv("NOTION_LIVE_REPORT_FILE"); reportPath != "" {
		report := map[string]interface{}{"checked_at": time.Now().Format(time.RFC3339), "catalog_model_count": len(catalog.Models), "model_selection_restricted": catalog.Restricted, "observations": observations}
		encoded, _ := json.MarshalIndent(report, "", "  ")
		if err := os.WriteFile(reportPath, append(encoded, '\n'), 0600); err != nil {
			t.Fatal("cannot write sanitized live report")
		}
	}
}
