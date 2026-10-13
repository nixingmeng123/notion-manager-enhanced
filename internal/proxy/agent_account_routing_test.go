package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func confirmedAgentAccount(user, slug string) *Account {
	return &Account{TokenV2: "fixture-token", UserID: user, SpaceID: "fixture-space", ClientVersion: DefaultClientVersion,
		AgentCapability: &AgentCapability{Status: "available", CheckedAt: time.Now(), Models: []AgentModelAuthorization{
			{Slug: slug, ActualSlug: slug, Authorization: "confirmed"},
		}}}
}

func TestAgentRoutingUsesRequestedModelIdentityAndRotatesVerifiedPeers(t *testing.T) {
	pool := NewAccountPool()
	unknown := &Account{TokenV2: "fixture", UserID: "unknown", SpaceID: "space"}
	sol1 := confirmedAgentAccount("sol1", "omniberry-sundae")
	opus := confirmedAgentAccount("opus", "albuquerque-quinn")
	sol2 := confirmedAgentAccount("sol2", "omniberry-sundae")
	mismatch := confirmedAgentAccount("mismatch", "omniberry-sundae")
	mismatch.AgentCapability.Models[0].ActualSlug = "albuquerque-quinn"
	denied := confirmedAgentAccount("denied", "omniberry-sundae")
	denied.AgentCapability.Models[0].Authorization = "denied"
	pool.accounts = []*Account{unknown, sol1, opus, mismatch, sol2, denied}
	for i := 0; i < 6; i++ {
		want := sol1
		if i%2 == 1 {
			want = sol2
		}
		if got := pool.NextAgentExcluding(nil, "gpt-6.1-sol"); got != want {
			t.Fatalf("request %d selected %v, expected %v", i, got, want)
		}
	}
	if got := pool.NextAgentExcluding(nil, "opus-5.5"); got != opus {
		t.Fatal("Sol identity incorrectly used for Opus")
	}
	if got := pool.NextAgentExcluding(map[*Account]bool{sol1: true}, "sol"); got != sol2 {
		t.Fatal("excluded verified account selected")
	}
	sol2.updateAgentCapability(func(c *AgentCapability) { c.CooldownUntil = time.Now().Add(time.Minute) })
	if got := pool.NextAgentExcluding(nil, "gpt-6.1-sol"); got != sol1 {
		t.Fatal("cooldown ignored")
	}
	pool.applyWorkspaceCount(sol1, 0)
	got := pool.NextAgentExcluding(nil, "gpt-6.1-sol")
	if got == nil || got == sol1 || got == sol2 {
		t.Fatal("explicit bootstrap did not respect workspace/cooldown")
	}
}

func TestAgentRoutingKeepsBootstrapWhenNoIdentityIsVerified(t *testing.T) {
	pool := NewAccountPool()
	a := &Account{SpaceID: "a"}
	b := &Account{SpaceID: "b"}
	pool.accounts = []*Account{a, b}
	if pool.NextAgentExcluding(nil, "gpt-6.1-sol") != a || pool.NextAgentExcluding(nil, "gpt-6.1-sol") != b {
		t.Fatal("unverified explicit requests cannot bootstrap")
	}
}

func TestAgentAPIAdvertisedModelAvoidsUnverifiedRejectingAccount(t *testing.T) {
	for _, endpoint := range []string{"messages", "chat", "responses"} {
		t.Run(endpoint, func(t *testing.T) {
			creates := 0
			pool, bad := agentFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("x-notion-active-user-header") != "verified-user" {
					t.Error("advertised model routed to an unverified account")
					w.WriteHeader(400)
					_, _ = w.Write([]byte(`{"name":"UserValidationError"}`))
					return
				}
				switch {
				case strings.HasSuffix(r.URL.Path, "getAvailableModels"):
					_, _ = w.Write([]byte(`{"models":[],"modelSelectionRestricted":true}`))
				case strings.HasSuffix(r.URL.Path, "createAgentThread"):
					creates++
					_, _ = w.Write([]byte(`{"thread":{"data":{"agent_provider":{"model":"omniberry-sundae","reasoningEffort":"medium","modelSelection":"auto"}}}}`))
				default:
					writeAgentTranscript(w, "VERIFIED_ACCOUNT_OK", "finished")
				}
			})
			bad.UserID = "rejecting-user"
			good := confirmedAgentAccount("verified-user", "omniberry-sundae")
			pool.accounts = append(pool.accounts, good)
			models := buildClientModels(pool)
			if len(models) != 1 || models[0].ID != "agent/gpt-6.1-sol" {
				t.Fatal("fixture model discovery mismatch")
			}
			for i := 0; i < 3; i++ {
				body, _ := json.Marshal(map[string]interface{}{"model": "agent/gpt-6.1-sol", "messages": []map[string]string{{"role": "user", "content": "hi"}}, "input": "hi"})
				req := httptest.NewRequest("POST", "/v1/"+endpoint, strings.NewReader(string(body)))
				w := httptest.NewRecorder()
				switch endpoint {
				case "messages":
					HandleAnthropicMessages(pool)(w, req)
				case "chat":
					HandleOpenAIChatCompletions(pool)(w, req)
				default:
					HandleOpenAIResponses(pool)(w, req)
				}
				if w.Code != 200 || !strings.Contains(w.Body.String(), "VERIFIED_ACCOUNT_OK") {
					t.Fatalf("advertised model request %d failed: %d %s", i, w.Code, w.Body.String())
				}
			}
			if creates != 3 || bad.agentSnapshot().Requests != 0 {
				t.Fatal("request was replayed or rejecting account was used")
			}
		})
	}
}
