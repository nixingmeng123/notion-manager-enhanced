package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Opt-in reproduction of a supplied client request. Account credentials and
// transcript traces remain local; this helper never approves upstream actions.
func TestAgentV3LiveClientRequest(t *testing.T) {
	requestPath := os.Getenv("NOTION_LIVE_REQUEST_FILE")
	if requestPath == "" {
		t.Skip("live client request not supplied")
	}
	accountData, err := os.ReadFile(os.Getenv("NOTION_LIVE_ACCOUNT_FILE"))
	var acc Account
	if err != nil || json.Unmarshal(accountData, &acc) != nil || acc.TokenV2 == "" || acc.SpaceID == "" {
		t.Fatal("incomplete live account")
	}
	payload, err := os.ReadFile(requestPath)
	if err != nil || !json.Valid(payload) {
		t.Fatal("invalid live request file")
	}
	previousConfig, previousClient := AppConfig, agentHTTPClient
	t.Cleanup(func() { AppConfig, agentHTTPClient = previousConfig, previousClient })
	AppConfig = DefaultConfig()
	AppConfig.AgentV3.Enabled = true
	AppConfig.AgentV3.TimeoutSeconds = 300
	AppConfig.AgentV3.MinRequestIntervalSeconds = 15
	AppConfig.Server.AccountsDir = ""
	if tracePath := os.Getenv("NOTION_LIVE_TRACE_FILE"); tracePath != "" {
		agentHTTPClient = func(timeout time.Duration) *http.Client {
			base := previousClient(timeout)
			copy := *base
			copy.Transport = &liveTraceTransport{base: base.Transport, path: tracePath, token: acc.TokenV2}
			return &copy
		}
	}
	pool := NewAccountPool()
	pool.accounts = []*Account{&acc}
	ctx, cancel := context.WithTimeout(context.Background(), 305*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(payload)).WithContext(ctx)
	if webSearch := os.Getenv("NOTION_LIVE_WEB_SEARCH"); webSearch != "" {
		req.Header.Set("X-Web-Search", webSearch)
	}
	rec := httptest.NewRecorder()
	HandleOpenAIResponses(pool).ServeHTTP(rec, req)
	text := redactSensitiveText(strings.ReplaceAll(rec.Body.String(), acc.TokenV2, "[REDACTED]"))
	if outputPath := os.Getenv("NOTION_LIVE_OUTPUT_FILE"); outputPath != "" {
		if err := os.WriteFile(outputPath, []byte(text), 0600); err != nil {
			t.Fatal("cannot save local live response")
		}
	}
	t.Logf("client Responses status=%d response_bytes=%d", rec.Code, len(text))
	if rec.Code != http.StatusOK || !strings.Contains(text, "response.completed") {
		t.Fatalf("live client response failed: %s", text)
	}
	if expected := os.Getenv("NOTION_LIVE_EXPECT_TEXT"); expected != "" {
		var output strings.Builder
		for _, line := range strings.Split(text, "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var event struct {
				Type  string `json:"type"`
				Delta string `json:"delta"`
			}
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) == nil && event.Type == "response.output_text.delta" {
				output.WriteString(event.Delta)
			}
		}
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("live client text does not include %q", expected)
		}
	}
}
