package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestAgentV3SettingsValidationAndPersistence(t *testing.T) {
	pool, _ := agentFixture(t, func(w http.ResponseWriter, r *http.Request) { writeAgentCatalog(w, true) })
	_ = pool
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("# retain this comment\nserver:\n  api_key: do-not-change\nproxy:\n  default_model: opus-4.6\n"), 0600); err != nil {
		t.Fatal(err)
	}
	auth := NewDashboardAuth("", "")
	handler := HandleAdminSettings(path, auth)
	invoke := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest("PUT", "/admin/settings", strings.NewReader(body)))
		return rec
	}
	for _, body := range []string{`{"default_protocol":"unknown"}`, `{"agent_timeout_seconds":0}`, `{"agent_poll_interval_ms":99}`, `{"agent_v3_enabled":false,"default_protocol":"agent"}`} {
		if rec := invoke(body); rec.Code != 400 {
			t.Fatalf("invalid settings accepted: %s: %s", body, rec.Body.String())
		}
	}
	if enhancedConfigSnapshot().DefaultProtocol != "workflow" {
		t.Fatal("invalid update partially changed configuration")
	}
	rec := invoke(`{"default_protocol":"agent","agent_v3_enabled":true,"agent_timeout_seconds":120,"agent_poll_interval_ms":500,"agent_model_cache_seconds":60}`)
	if rec.Code != 200 {
		t.Fatalf("valid update rejected: %s", rec.Body.String())
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "retain this comment") || !strings.Contains(string(data), "do-not-change") {
		t.Fatal("settings persistence lost unrelated configuration")
	}
	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil || config.AgentV3.TimeoutSeconds != 120 || config.AgentV3.PollIntervalMS != 500 || config.Proxy.DefaultProtocol != "agent" {
		t.Fatalf("persisted config invalid: %s err=%v", data, err)
	}
	if route, err := resolveInferenceRoute("opus-5.5"); err != nil || route.Protocol != "agent" {
		t.Fatal("configured default protocol was ignored")
	}
	if route, err := resolveInferenceRoute("workflow/opus-4.6"); err != nil || route.Protocol != "workflow" {
		t.Fatal("explicit Workflow prefix was ignored")
	}
}

func TestAgentV3ModelsAndAccountMetadata(t *testing.T) {
	pool, acc := agentFixture(t, func(w http.ResponseWriter, r *http.Request) { writeAgentCatalog(w, true) })
	models := buildEnhancedModels(pool, true)
	for _, model := range models {
		if model.Protocol == "agent" && model.Available {
			t.Fatal("static candidates advertised as authorized")
		}
	}
	if err := FetchAgentModels(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, model := range buildEnhancedModels(pool, false) {
		if model.ID == "agent/opus-5.5" {
			found = true
			if model.Available || model.Authorization != "catalog_enabled" || model.AuthorizationScope != "unverified" || model.InternalSlug != "albuquerque-quinn" || model.ReasoningSource != "getAvailableModels" {
				t.Fatalf("incorrect authorization metadata: %+v", model)
			}
		}
	}
	if !found {
		t.Fatal("Agent model omitted")
	}
	acc.recordAgentResult(AgentModel{Slug: "albuquerque-quinn"}, nil)
	for _, model := range buildEnhancedModels(pool, false) {
		if model.ID == "agent/opus-5.5" && (!model.Available || model.Authorization != "confirmed") {
			t.Fatal("successful inference did not confirm availability")
		}
	}
	accountData, _ := json.Marshal(pool.GetAccountDetails())
	if strings.Contains(string(accountData), "private-cookie-value") || !strings.Contains(string(accountData), "agent_capability") {
		t.Fatalf("account metadata leaked credentials or lost capabilities: %s", accountData)
	}
	acc.updateAgentCapability(func(c *AgentCapability) { c.CheckedAt = time.Now().Add(-time.Hour) })
	for _, model := range buildEnhancedModels(pool, false) {
		if model.ID == "agent/opus-5.5" && model.Available {
			t.Fatal("expired cache advertised availability")
		}
	}
}

func TestAgentV3ConcurrentAccountPersistence(t *testing.T) {
	_, acc := agentFixture(t, func(w http.ResponseWriter, r *http.Request) { writeAgentCatalog(w, true) })
	dir := t.TempDir()
	if _, err := SaveAccountToFile(acc, dir); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Go(func() {
			for j := 0; j < 15; j++ {
				acc.recordAgentResult(AgentModel{Slug: "albuquerque-quinn"}, nil)
				acc.setModels([]ModelEntry{{ID: "claude-opus-4-6", Name: "Opus 4.6"}})
				acc.setQuotaInfo(&QuotaInfo{UserUsage: j, UserLimit: 200}, nil)
				if err := saveAccountFile(dir, acc); err != nil {
					t.Errorf("concurrent save failed: %v", err)
				}
			}
		})
	}
	workers.Wait()
	if err := saveAccountFile(dir, acc); err != nil {
		t.Fatal(err)
	}
	loaded := NewAccountPool()
	if err := loaded.LoadFromDir(dir); err != nil {
		t.Fatal(err)
	}
	if len(loaded.AccountsSnapshot()) != 1 || loaded.AccountsSnapshot()[0].agentSnapshot().Requests != 60 {
		t.Fatal("concurrent saves corrupted account state")
	}
}

func TestAgentV3AccountPersistence(t *testing.T) {
	_, acc := agentFixture(t, func(w http.ResponseWriter, r *http.Request) { writeAgentCatalog(w, true) })
	if err := FetchAgentModels(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := SaveAccountToFile(acc, dir); err != nil {
		t.Fatal(err)
	}
	acc.recordAgentResult(AgentModel{Slug: "albuquerque-quinn"}, gatewayError("rate_limit", 429, "rate limit", true))
	if err := saveAccountFile(dir, acc); err != nil {
		t.Fatal(err)
	}
	loaded := NewAccountPool()
	if err := loaded.LoadFromDir(dir); err != nil {
		t.Fatal(err)
	}
	c := loaded.AccountsSnapshot()[0].agentSnapshot()
	if c.Failures["rate_limit"] != 1 || c.Requests != 1 || len(c.Models) != 1 || c.CooldownUntil.IsZero() {
		t.Fatalf("protocol state was not persisted: %+v", c)
	}
}

func TestAgentV3ConcurrentSnapshots(t *testing.T) {
	pool, acc := agentFixture(t, func(w http.ResponseWriter, r *http.Request) { writeAgentCatalog(w, true) })
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Go(func() {
			for i := 0; i < 100; i++ {
				acc.recordAgentResult(AgentModel{Slug: "albuquerque-quinn"}, nil)
				_ = acc.agentSnapshot()
				_ = pool.NextAgentExcluding(nil)
				_ = pool.agentStats()
				_ = buildEnhancedModels(pool, true)
			}
		})
	}
	wg.Wait()
	if acc.agentSnapshot().Requests != 400 {
		t.Fatal("concurrent metrics updates were lost")
	}
}

func TestAgentV3ClientErrorsKeepHTTPStatus(t *testing.T) {
	for _, endpoint := range []string{"messages", "chat", "responses"} {
		for _, stream := range []bool{false, true} {
			t.Run(endpoint+map[bool]string{true: "_stream", false: "_json"}[stream], func(t *testing.T) {
				pool, _ := agentFixture(t, func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "getAvailableModels") {
						writeAgentCatalog(w, true)
					} else {
						w.WriteHeader(403)
						w.Write([]byte(`{"message":"token_v2=secret-echo"}`))
					}
				})
				body := map[string]interface{}{"model": "agent/opus-5.5", "stream": stream, "messages": []interface{}{map[string]string{"role": "user", "content": "hi"}}, "input": "hi"}
				data, _ := json.Marshal(body)
				rec := httptest.NewRecorder()
				req := httptest.NewRequest("POST", "/v1/"+endpoint, strings.NewReader(string(data)))
				switch endpoint {
				case "messages":
					HandleAnthropicMessages(pool)(rec, req)
				case "chat":
					HandleOpenAIChatCompletions(pool)(rec, req)
				case "responses":
					HandleOpenAIResponses(pool)(rec, req)
				}
				if rec.Code != 403 || strings.Contains(rec.Body.String(), "secret-echo") || strings.Contains(rec.Body.String(), "message_stop") || strings.Contains(rec.Body.String(), "response.completed") {
					t.Fatalf("incorrect error: %d %s", rec.Code, rec.Body.String())
				}
			})
		}
	}
}
