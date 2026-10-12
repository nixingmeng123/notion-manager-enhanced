package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func decodeAgentPage(t *testing.T, body string) agentTranscriptResponse {
	t.Helper()
	var page agentTranscriptResponse
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatal(err)
	}
	return page
}

func TestAgentTranscriptAppliesToolResultAndAssistantPatches(t *testing.T) {
	state := &agentTranscriptState{}
	page := decodeAgentPage(t, `{"patches":[
		{"op":"put","entity":{"id":"tool:test","kind":"tool","name":"mcp_run_tool","text":"Searching"}},
		{"op":"patch","id":"tool:test","ops":[{"op":"add","path":"/result","value":{"is_error":false,"result_text":"PRIVATE TOOL BODY"}}]},
		{"op":"put","entity":{"id":"answer","kind":"assistant_message","content":[{"type":"text","text":"你好🙂 draft"}]}},
		{"op":"patch","id":"answer","ops":[{"op":"replace","path":"/content/0/text","value":"你好🙂 final"},{"op":"add","path":"/content/-","value":{"type":"text","text":"https://example.test/source"}}]},
		{"op":"session","session":{"status":"running"}}
	]}`)
	for i := 0; i < 2; i++ {
		text, done, err := state.apply(page)
		if err != nil || done || text != "你好🙂 final\nhttps://example.test/source" {
			t.Fatalf("patch snapshot=%q done=%v err=%v", text, done, err)
		}
		if strings.Contains(text, "PRIVATE TOOL BODY") {
			t.Fatal("tool result became assistant output")
		}
	}
	completion := decodeAgentPage(t, `{"patches":[{"op":"put","entity":{"id":"done","kind":"turn_completed","source":"committed","stop_reason":"end_turn"}}]}`)
	text, done, err := state.apply(completion)
	if err != nil || !done || text != "你好🙂 final\nhttps://example.test/source" {
		t.Fatalf("patched completion=%q done=%v err=%v", text, done, err)
	}
}

func TestAgentTranscriptRejectsInvalidEntityPatches(t *testing.T) {
	for name, operation := range map[string]string{
		"unknown_target": `{"op":"patch","id":"missing","ops":[{"op":"add","path":"/result","value":{}}]}`,
		"missing_ops":    `{"op":"patch","id":"answer"}`,
		"null_ops":       `{"op":"patch","id":"answer","ops":null}`,
		"missing_path":   `{"op":"patch","id":"answer","ops":[{"op":"replace","path":"/missing/child","value":"secret"}]}`,
		"changed_id":     `{"op":"patch","id":"answer","ops":[{"op":"replace","path":"/id","value":"other"}]}`,
		"negative_index": `{"op":"patch","id":"answer","ops":[{"op":"replace","path":"/content/-1/text","value":"secret"}]}`,
		"unknown_op":     `{"op":"patch","id":"answer","ops":[{"op":"future","path":"/content","value":[]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			state := &agentTranscriptState{}
			page := decodeAgentPage(t, `{"patches":[{"op":"put","entity":{"id":"answer","kind":"assistant_message","content":[{"type":"text","text":"original"}]}},`+operation+`]}`)
			_, done, err := state.apply(page)
			if err == nil || done || asGatewayError(err).Kind != "transcript_parse" || strings.Contains(err.Error(), "secret") {
				t.Fatalf("invalid patch accepted or leaked its body: done=%v err=%v", done, err)
			}
		})
	}
}

func TestAgentTranscriptRemoveAndReinsertDoesNotDuplicateText(t *testing.T) {
	state := &agentTranscriptState{}
	page := decodeAgentPage(t, `{"patches":[{"op":"put","entity":{"id":"answer","kind":"assistant_message","content":[{"type":"text","text":"old"}]}},{"op":"remove","id":"answer"},{"op":"put","entity":{"id":"answer","kind":"assistant_message","content":[{"type":"text","text":"new"}]}}]}`)
	text, _, err := state.apply(page)
	if err != nil || text != "new" {
		t.Fatalf("reinserted text=%q err=%v", text, err)
	}
}

func TestAgentTranscriptPatchedApprovalStillRequiresUser(t *testing.T) {
	page := decodeAgentPage(t, `{"patches":[{"op":"put","entity":{"id":"tool:test","kind":"tool"}},{"op":"patch","id":"tool:test","ops":[{"op":"replace","path":"/kind","value":"approval_request"}]}]}`)
	_, _, err := (&agentTranscriptState{}).apply(page)
	if err == nil || asGatewayError(err).Kind != "approval_required" {
		t.Fatal("patched approval request was ignored")
	}
}

func TestAgentSearchSettingIsIndependentOfWorkflowPromptToggle(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			var created atomic.Bool
			pool, _ := agentFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "getAvailableModels"):
					writeAgentCatalog(w, true)
				case strings.HasSuffix(r.URL.Path, "createAgentThread"):
					var payload agentCreateRequest
					if json.NewDecoder(r.Body).Decode(&payload) != nil {
						t.Error("invalid create payload")
					}
					created.Store(true)
					if payload.BrowserEnabled != enabled || payload.Policies["approval_mode"] != "review_for_me" || payload.EnableSuggestedEditsTools {
						t.Errorf("unexpected browser or approval policy: %+v", payload)
					}
					writeAgentCreated(w)
				default:
					writeAgentTranscript(w, "SEARCH_OK", "finished")
				}
			})
			AppConfig.Proxy.DisableNotionPrompt = true
			AppConfig.Proxy.EnableWebSearch = boolPtr(!enabled)
			req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"agent/opus-5.5","input":"hello","stream":true}`))
			req.Header.Set("X-Web-Search", map[bool]string{false: "false", true: "true"}[enabled])
			rec := httptest.NewRecorder()
			HandleOpenAIResponses(pool)(rec, req)
			if rec.Code != 200 || !created.Load() || !strings.Contains(rec.Body.String(), "response.completed") {
				t.Fatalf("search request failed: %s", rec.Body.String())
			}
		})
	}
}
