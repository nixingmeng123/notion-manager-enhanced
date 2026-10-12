package proxy

import (
	"gopkg.in/yaml.v3"
	"sort"
	"strconv"
	"time"
)

func setYAMLInt(mapping *yaml.Node, key string, value int) {
	setYAMLString(mapping, key, strconv.Itoa(value))
	for i := 0; i < len(mapping.Content)-1; i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1].Tag = "!!int"
			mapping.Content[i+1].Style = 0
			return
		}
	}
}

func buildEnhancedModels(pool *AccountPool, includeDisabled bool) []publicModel {
	workflow := buildPublicModels(pool.AllModels())
	result := make([]publicModel, 0, len(workflow)*2+len(agentModelsSnapshot()))
	for _, model := range workflow {
		model.Protocol = "workflow"
		model.InternalSlug = ResolveModel(model.ID)
		model.Authorization = "workflow_catalog"
		model.Available = pool.AvailableCount() > 0
		result = append(result, model)
		if enhancedConfigSnapshot().Enabled || includeDisabled {
			model.ID = "workflow/" + model.ID
			result = append(result, model)
		}
	}
	if enhancedConfigSnapshot().Enabled || includeDisabled {
		for _, candidate := range agentModelsSnapshot() {
			model := publicModel{ID: agentAlias(candidate), Object: "model", Created: publicModelCreatedAt, OwnedBy: "notion-manager", Protocol: "agent", InternalSlug: candidate.Slug,
				ReasoningEfforts: candidate.Efforts, Authorization: "unknown", SessionMode: "history-replay", StreamingMode: "polling-buffered", AuthorizationScope: "unverified", ReasoningSource: "candidate_registry"}
			denied := false
			for _, account := range pool.AccountsSnapshot() {
				capability := account.agentSnapshot()
				if capability.CheckedAt.IsZero() || time.Since(capability.CheckedAt) > time.Duration(enhancedConfigSnapshot().ModelCacheSeconds)*time.Second {
					continue
				}
				for _, authorized := range capability.Models {
					if authorized.Slug != candidate.Slug {
						continue
					}
					if authorized.Authorization == "denied" {
						denied = true
						continue
					}
					if model.Authorization != "confirmed" {
						model.Authorization = authorized.Authorization
					}
					if authorized.EffortsDiscovered {
						model.ReasoningEfforts = authorized.Efforts
						model.ReasoningSource = "getAvailableModels"
					}
					if authorized.Authorization == "confirmed" && enhancedConfigSnapshot().Enabled && !time.Now().Before(capability.CooldownUntil) {
						model.Available = true
					}
				}
			}
			if model.Authorization == "unknown" && denied {
				model.Authorization = "denied"
			}
			result = append(result, model)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// Client pickers commonly ignore availability metadata. Keep diagnostic
// candidates in the dashboard and expose only account catalog entries or
// completed, identity-verified Agent inferences here. Never use static fallback
// mappings to populate the client list.
func buildClientModels(pool *AccountPool) []publicModel {
	config := enhancedConfigSnapshot()
	accounts := pool.AccountsSnapshot()
	var workflowEntries []ModelEntry
	confirmed := make(map[string]AgentModelAuthorization)
	for _, account := range accounts {
		if !pool.isUnusable(account) {
			workflowEntries = append(workflowEntries, account.modelsSnapshot()...)
		}
		capability := account.agentSnapshot()
		if !config.Enabled || account.SpaceID == "" || account.UserID == "" || (account.TokenV2 == "" && account.FullCookie == "") || pool.hasNoWorkspace(account) || time.Now().Before(capability.CooldownUntil) {
			continue
		}
		for _, model := range capability.Models {
			if model.Authorization == "confirmed" && model.ActualSlug == model.Slug {
				confirmed[model.Slug] = model
			}
		}
	}
	result := make([]publicModel, 0, len(workflowEntries)+len(confirmed))
	for _, model := range buildPublicModels(workflowEntries) {
		model.Protocol = "workflow"
		model.InternalSlug = ResolveModel(model.ID)
		model.Authorization = "workflow_catalog"
		model.Available = true
		if config.DefaultProtocol == "agent" {
			model.ID = "workflow/" + model.ID
		}
		result = append(result, model)
	}
	seenAgent := make(map[string]bool)
	for _, candidate := range agentModelsSnapshot() {
		verified, ok := confirmed[candidate.Slug]
		if !ok || seenAgent[candidate.Slug] {
			continue
		}
		seenAgent[candidate.Slug] = true
		model := publicModel{ID: agentAlias(candidate), Object: "model", Created: publicModelCreatedAt, OwnedBy: "notion-manager",
			Protocol: "agent", InternalSlug: candidate.Slug, Available: true, Authorization: "confirmed",
			AuthorizationScope: "runtime_inference", SessionMode: "history-replay", StreamingMode: "polling-buffered",
			ReasoningEfforts: candidate.Efforts, ReasoningSource: "candidate_registry"}
		if verified.EffortsDiscovered {
			model.ReasoningEfforts = verified.Efforts
			model.ReasoningSource = "getAvailableModels"
		}
		result = append(result, model)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (p *AccountPool) agentStats() map[string]interface{} {
	requests := 0
	failures := make(map[string]int)
	for _, account := range p.AccountsSnapshot() {
		capability := account.agentSnapshot()
		requests += capability.Requests
		for kind, count := range capability.Failures {
			failures[kind] += count
		}
	}
	return map[string]interface{}{"requests": requests, "failures": failures, "quota": "unknown", "usage": "unavailable"}
}

func enhancedSettingsFields() map[string]interface{} {
	config := enhancedConfigSnapshot()
	return map[string]interface{}{
		"enable_web_search": AppConfig.WebSearchEnabled(), "enable_workspace_search": AppConfig.WorkspaceSearchEnabled(),
		"ask_mode_default": AppConfig.AskModeDefault(), "disable_notion_prompt": AppConfig.Proxy.DisableNotionPrompt,
		"debug_logging": AppConfig.Server.DebugLogging, "notion_proxy": AppConfig.NotionProxyURL(),
		"default_protocol": config.DefaultProtocol, "agent_v3_enabled": config.Enabled,
		"agent_timeout_seconds": config.TimeoutSeconds, "agent_poll_interval_ms": config.PollIntervalMS,
		"agent_model_cache_seconds":          config.ModelCacheSeconds,
		"agent_min_request_interval_seconds": config.MinRequestIntervalSeconds,
		"agent_session_mode":                 "history-replay", "agent_streaming_mode": "polling-buffered",
	}
}
