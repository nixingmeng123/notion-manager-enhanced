package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAgentV3OnlyEnforcesExplicitReasoningEffort(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "upstream_default", true: "explicit_medium"}[explicit], func(t *testing.T) {
			_, acc := agentFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "getAvailableModels"):
					writeAgentCatalog(w, true)
				case strings.HasSuffix(r.URL.Path, "createAgentThread"):
					var payload agentCreateRequest
					json.NewDecoder(r.Body).Decode(&payload)
					want := ""
					if explicit {
						want = "medium"
					}
					if payload.ReasoningEffort != want {
						t.Errorf("effort=%q want=%q", payload.ReasoningEffort, want)
					}
					w.Write([]byte(`{"thread":{"data":{"agent_provider":{"model":"albuquerque-quinn","reasoningEffort":"high"}}}}`))
				default:
					writeAgentTranscript(w, "OK", "finished")
				}
			})
			opts := CallOptions{Context: context.Background()}
			if explicit {
				opts.ReasoningEffort = "medium"
			}
			emitted := false
			err := CallAgentInference(acc, []ChatMessage{{Role: "user", Content: "hi"}}, "opus-5.5", true, func(string, bool, *UsageInfo) { emitted = true }, opts)
			if explicit {
				if err == nil || asGatewayError(err).Kind != "unsupported_effort" || emitted {
					t.Fatal("explicit effort substitution was accepted")
				}
			} else if err != nil || !emitted || acc.agentSnapshot().Models[0].ActualEffort != "high" {
				t.Fatal("upstream default effort was rejected or not recorded")
			}
		})
	}
}

func TestAgentV3LiveCompletionShape(t *testing.T) {
	var response agentTranscriptResponse
	// Minimal shape observed on 2026-10-09. No account/thread identifiers.
	json.Unmarshal([]byte(`{"patches":[{"op":"committed","sequence":22},{"op":"put","entity":{"id":"answer","kind":"assistant_message","source":"committed","content":[{"type":"text","text":"你好🙂"}]}},{"op":"put","entity":{"id":"finished","kind":"turn_completed","source":"committed","stop_reason":"end_turn"}},{"op":"session","session":{"status":"idle"}},{"op":"committed","sequence":24}],"has_more_forward":false,"has_more_backward":false}`), &response)
	text, done, err := (&agentTranscriptState{}).apply(response)
	if err != nil || !done || text != "你好🙂" {
		t.Fatalf("completion=%q done=%v err=%v", text, done, err)
	}
	response.Patches = nil
	if _, _, err := (&agentTranscriptState{}).apply(response); err == nil {
		t.Fatal("missing patches accepted")
	}
}

func TestAgentV3RejectsUpstreamModelSubstitution(t *testing.T) {
	var polls atomic.Int32
	_, acc := agentFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "getAvailableModels"):
			writeAgentCatalog(w, true)
		case strings.HasSuffix(r.URL.Path, "createAgentThread"):
			w.Write([]byte(`{"thread":{"data":{"agent_provider":{"model":"omniberry-sundae","modelSelection":"auto","reasoningEffort":"medium"}}}}`))
		default:
			polls.Add(1)
			writeAgentTranscript(w, "do not report as Opus", "finished")
		}
	})
	emitted := false
	err := CallAgentInference(acc, []ChatMessage{{Role: "user", Content: "hi"}}, "opus-5.5", true, func(string, bool, *UsageInfo) { emitted = true })
	if err == nil || asGatewayError(err).Status != 403 || emitted || polls.Load() != 0 {
		t.Fatalf("silent model substitution err=%v emitted=%v polls=%d", err, emitted, polls.Load())
	}
	if acc.agentSnapshot().Models[0].Authorization != "denied" {
		t.Fatal("substituted model was advertised as confirmed")
	}
	if acc.agentSnapshot().Models[0].ActualSlug != "omniberry-sundae" {
		t.Fatal("actual returned model was not recorded")
	}
}

func TestAgentV3RoundRobinSharesAccountPool(t *testing.T) {
	pool, first := agentFixture(t, func(w http.ResponseWriter, r *http.Request) { writeAgentCatalog(w, true) })
	second := &Account{TokenV2: "test", UserID: "user2", SpaceID: "space2"}
	pool.accounts = append(pool.accounts, second)
	if pool.NextAgentExcluding(nil) != first || pool.NextAgentExcluding(nil) != second || pool.NextAgentExcluding(nil) != first {
		t.Fatal("Agent account rotation is not round-robin")
	}
	pool.applyWorkspaceCount(second, 0)
	if pool.NextAgentExcluding(nil) != first {
		t.Fatal("account with no workspace was selected")
	}
}

func TestAgentV3EmptyCatalogIsUnknownUntilInference(t *testing.T) {
	_, acc := agentFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "getAvailableModels"):
			w.Write([]byte(`{"models":[],"modelSelectionRestricted":true}`))
		case strings.HasSuffix(r.URL.Path, "createAgentThread"):
			writeAgentCreated(w)
		default:
			writeAgentTranscript(w, "confirmed by actual inference", "finished")
		}
	})
	model, _ := lookupAgentModel("opus-5.5")
	if _, err := authorizeAgentModel(t.Context(), acc, model, ""); err != nil {
		t.Fatal(err)
	}
	if acc.agentSnapshot().Models[0].Authorization != "unknown" {
		t.Fatal("empty catalog granted authorization")
	}
	if err := CallAgentInference(acc, []ChatMessage{{Role: "user", Content: "hi"}}, "opus-5.5", true, nil); err != nil {
		t.Fatal(err)
	}
	if acc.agentSnapshot().Models[0].Authorization != "confirmed" {
		t.Fatal("actual inference did not confirm model")
	}
}
