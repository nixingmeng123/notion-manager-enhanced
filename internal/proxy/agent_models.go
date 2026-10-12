package proxy

import (
	"context"
	_ "embed"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
)

// Candidate mapping only. No entry in this embedded registry grants permission.
// Source and pinned revisions are recorded in docs/AGENT_V3.md.
//
//go:embed agent_model_registry.json
var agentRegistryJSON []byte

type AgentModel struct {
	Name     string   `json:"name"`
	Slug     string   `json:"internal_slug"`
	Family   string   `json:"family"`
	Efforts  []string `json:"efforts"`
	Disabled bool     `json:"disabled"`
}

var agentRegistryMu sync.RWMutex
var agentRegistry = initialAgentRegistry()

func initialAgentRegistry() map[string]AgentModel {
	var raw map[string]AgentModel
	if err := json.Unmarshal(agentRegistryJSON, &raw); err != nil {
		panic("invalid embedded Agent candidate registry")
	}
	models := make(map[string]AgentModel, len(raw))
	for slug, model := range raw {
		model.Slug = slug
		models[normalizeModelName(model.Name)] = model
	}
	return models
}

func lookupAgentModel(alias string) (AgentModel, bool) {
	if alias == "opus" {
		alias = "opus-5.5"
	}
	if alias == "sol" {
		alias = "gpt-6.1-sol"
	}
	agentRegistryMu.RLock()
	defer agentRegistryMu.RUnlock()
	model, ok := agentRegistry[alias]
	model.Efforts = append([]string(nil), model.Efforts...)
	return model, ok
}

func agentModelsSnapshot() []AgentModel {
	agentRegistryMu.RLock()
	defer agentRegistryMu.RUnlock()
	models := make([]AgentModel, 0, len(agentRegistry))
	for _, m := range agentRegistry {
		m.Efforts = append([]string(nil), m.Efforts...)
		models = append(models, m)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })
	return models
}

type AgentModelAuthorization struct {
	Slug              string    `json:"internal_slug"`
	Name              string    `json:"name"`
	Authorization     string    `json:"authorization"` // catalog_enabled, denied, confirmed
	Efforts           []string  `json:"reasoning_efforts"`
	EffortsDiscovered bool      `json:"efforts_discovered"`
	ActualSlug        string    `json:"actual_slug,omitempty"`
	ActualEffort      string    `json:"actual_effort,omitempty"`
	ModelSelection    string    `json:"model_selection,omitempty"`
	ModelCheckedAt    time.Time `json:"model_checked_at,omitempty"`
}

// AgentCapability is stored in the existing account file; Workflow quota is
// intentionally never read or modified by this backend. Agent quota remains
// unknown until the upstream explicitly rejects an inference.
type AgentCapability struct {
	Models                   []AgentModelAuthorization `json:"models"`
	CheckedAt                time.Time                 `json:"checked_at"`
	Source                   string                    `json:"source"`
	ProtocolScope            string                    `json:"protocol_scope"`
	Status                   string                    `json:"status"`
	Quota                    string                    `json:"quota"`
	CooldownUntil            time.Time                 `json:"cooldown_until,omitempty"`
	Failures                 map[string]int            `json:"failures"`
	Requests                 int                       `json:"requests"`
	LastStartedAt            time.Time                 `json:"last_started_at,omitempty"`
	ModelSelectionRestricted bool                      `json:"model_selection_restricted"`
}

func (acc *Account) agentSnapshot() AgentCapability {
	acc.mu.RLock()
	defer acc.mu.RUnlock()
	c := AgentCapability{Status: "unknown", Quota: "unknown", Failures: make(map[string]int), ProtocolScope: "unverified"}
	if acc.AgentCapability != nil {
		c = *acc.AgentCapability
		c.Models = append([]AgentModelAuthorization(nil), c.Models...)
		for i := range c.Models {
			c.Models[i].Efforts = append([]string(nil), c.Models[i].Efforts...)
		}
		c.Failures = make(map[string]int)
		for k, v := range acc.AgentCapability.Failures {
			c.Failures[k] = v
		}
	}
	return c
}

func (acc *Account) updateAgentCapability(fn func(*AgentCapability)) {
	acc.mu.Lock()
	defer acc.mu.Unlock()
	if acc.AgentCapability == nil {
		acc.AgentCapability = &AgentCapability{Status: "unknown", Quota: "unknown", Failures: make(map[string]int), ProtocolScope: "unverified"}
	}
	if acc.AgentCapability.Failures == nil {
		acc.AgentCapability.Failures = make(map[string]int)
	}
	fn(acc.AgentCapability)
}

func FetchAgentModels(ctx context.Context, acc *Account) error {
	if acc == nil {
		return gatewayError("authentication", 401, "account credentials are required", false)
	}
	acc.agentDiscoveryMu.Lock()
	defer acc.agentDiscoveryMu.Unlock()
	return refreshAgentModels(ctx, acc)
}

func ensureAgentModels(ctx context.Context, acc *Account) error {
	acc.agentDiscoveryMu.Lock()
	defer acc.agentDiscoveryMu.Unlock()
	c := acc.agentSnapshot()
	if !c.CheckedAt.IsZero() && time.Since(c.CheckedAt) < time.Duration(enhancedConfigSnapshot().ModelCacheSeconds)*time.Second {
		return nil
	}
	return refreshAgentModels(ctx, acc)
}

func refreshAgentModels(ctx context.Context, acc *Account) error {
	// This is the observed getAvailableModels request, not an invented Agent
	// entitlement endpoint. Its scope is advertised explicitly as unverified.
	var response struct {
		Models                   json.RawMessage `json:"models"`
		ModelSelectionRestricted bool            `json:"modelSelectionRestricted"`
	}
	if err := agentPostJSON(ctx, acc, "getAvailableModels", map[string]string{"spaceId": acc.SpaceID}, &response); err != nil {
		acc.updateAgentCapability(func(c *AgentCapability) { c.CheckedAt = time.Time{}; c.Status = "discovery_failed" })
		return err
	}
	var raw []struct {
		Model                     string    `json:"model"`
		ModelMessage              string    `json:"modelMessage"`
		ModelFamily               string    `json:"modelFamily"`
		IsDisabled                bool      `json:"isDisabled"`
		IsAuthorized              *bool     `json:"isAuthorized"`
		Available                 *bool     `json:"available"`
		ReasoningEfforts          *[]string `json:"reasoningEfforts"`
		SupportedReasoningEfforts *[]string `json:"supportedReasoningEfforts"`
		ThreadTypes               []string  `json:"threadTypes"`
	}
	if len(response.Models) == 0 || string(response.Models) == "null" || json.Unmarshal(response.Models, &raw) != nil {
		return gatewayError("model_discovery", 502, "upstream model catalog has an unsupported format", true)
	}
	var found []AgentModelAuthorization
	for _, m := range raw {
		if m.Model == "" {
			continue
		}
		// Unknown slugs are advertised as Agent candidates only when the API
		// explicitly includes personal_agent among their supported protocols.
		known := false
		for _, candidate := range agentModelsSnapshot() {
			if candidate.Slug == m.Model {
				known = true
				break
			}
		}
		for _, protocol := range m.ThreadTypes {
			if protocol == "personal_agent" {
				known = true
			}
		}
		if !known {
			continue
		}
		authorization := "catalog_enabled"
		if m.IsDisabled || (m.IsAuthorized != nil && !*m.IsAuthorized) || (m.Available != nil && !*m.Available) {
			authorization = "denied"
		}
		if len(m.ThreadTypes) > 0 {
			supported := false
			for _, protocol := range m.ThreadTypes {
				if protocol == "personal_agent" {
					supported = true
				}
			}
			if !supported {
				authorization = "denied"
			}
		}
		efforts := m.SupportedReasoningEfforts
		if efforts == nil {
			efforts = m.ReasoningEfforts
		}
		entry := AgentModelAuthorization{Slug: m.Model, Name: m.ModelMessage, Authorization: authorization, EffortsDiscovered: efforts != nil}
		if efforts != nil {
			entry.Efforts = append([]string(nil), (*efforts)...)
		}
		found = append(found, entry)
		if m.ModelMessage != "" {
			alias := normalizeModelName(m.ModelMessage)
			agentRegistryMu.Lock()
			if _, exists := agentRegistry[alias]; !exists {
				agentRegistry[alias] = AgentModel{Name: m.ModelMessage, Slug: m.Model, Family: m.ModelFamily, Efforts: entry.Efforts, Disabled: authorization == "denied"}
			}
			agentRegistryMu.Unlock()
		}
	}
	acc.updateAgentCapability(func(c *AgentCapability) {
		c.Models = found
		c.CheckedAt = time.Now()
		c.Source = "getAvailableModels"
		c.ProtocolScope = "unverified"
		c.Status = "discovered"
		c.ModelSelectionRestricted = response.ModelSelectionRestricted
	})
	return nil
}

func authorizeAgentModel(ctx context.Context, acc *Account, model AgentModel, effort string) (string, error) {
	if err := ensureAgentModels(ctx, acc); err != nil {
		return "", err
	}
	for _, m := range acc.agentSnapshot().Models {
		if m.Slug != model.Slug {
			continue
		}
		if m.Authorization == "denied" {
			return "", gatewayError("model_permission", 403, "account is not authorized for this Agent model", true)
		}
		allowed := model.Efforts
		if m.EffortsDiscovered {
			allowed = m.Efforts
		}
		return selectAgentEffort(allowed, effort)
	}
	// getAvailableModels has no verified Personal Agent authorization scope.
	// Absence is unknown, rather than a denial. An explicit client selection
	// may try a static candidate; the Notion backend remains authoritative.
	// Do not advertise it as available until the entire inference succeeds.
	if model.Disabled {
		return "", gatewayError("model_permission", 403, "disabled candidate has no positive API authorization", false)
	}
	selected, err := selectAgentEffort(model.Efforts, effort)
	if err != nil {
		return "", err
	}
	acc.updateAgentCapability(func(c *AgentCapability) {
		c.Models = append(c.Models, AgentModelAuthorization{Slug: model.Slug, Name: model.Name, Authorization: "unknown"})
	})
	return selected, nil
}

func selectAgentEffort(allowed []string, effort string) (string, error) {
	if effort == "" {
		if len(allowed) == 0 {
			return "", nil
		}
		for _, value := range allowed {
			if value == "medium" {
				return value, nil
			}
		}
		return allowed[0], nil
	}
	for _, value := range allowed {
		if value == effort {
			return effort, nil
		}
	}
	return "", gatewayError("unsupported_effort", 400, "reasoning effort is not supported by this Agent model", false)
}

func (p *AccountPool) NextAgentExcluding(excluded map[*Account]bool) *Account {
	// Shared account inventory, independent protocol availability.
	accounts := p.AccountsSnapshot()
	if len(accounts) == 0 {
		return nil
	}
	start := p.agentIndex.Add(1) - 1
	for i := range accounts {
		acc := accounts[(start+uint64(i))%uint64(len(accounts))]
		if excluded[acc] || acc.SpaceID == "" {
			continue
		}
		if p.hasNoWorkspace(acc) {
			continue
		}
		c := acc.agentSnapshot()
		if time.Now().Before(c.CooldownUntil) {
			continue
		}
		return acc
	}
	return nil
}

func (acc *Account) recordAgentModelSelection(model AgentModel, actual, effort, selection string) {
	acc.updateAgentCapability(func(c *AgentCapability) {
		for i := range c.Models {
			if c.Models[i].Slug == model.Slug {
				c.Models[i].ActualSlug = actual
				c.Models[i].ActualEffort = effort
				c.Models[i].ModelSelection = selection
				c.Models[i].ModelCheckedAt = time.Now()
				return
			}
		}
	})
}

func (acc *Account) recordAgentResult(model AgentModel, err error) {
	acc.updateAgentCapability(func(c *AgentCapability) {
		c.Requests++
		if err == nil {
			c.Status = "available"
			c.Quota = "not_rejected"
			c.CooldownUntil = time.Time{}
			for i := range c.Models {
				if c.Models[i].Slug == model.Slug {
					c.Models[i].Authorization = "confirmed"
				}
			}
			return
		}
		e := asGatewayError(err)
		c.Failures[e.Kind]++
		c.Status = e.Kind
		switch e.Kind {
		case "authentication", "rate_limit":
			c.CooldownUntil = time.Now().Add(time.Minute)
		case "quota":
			c.Quota = "exhausted"
			c.CooldownUntil = time.Now().Add(5 * time.Minute)
		case "model_permission":
			for i := range c.Models {
				if c.Models[i].Slug == model.Slug {
					c.Models[i].Authorization = "denied"
				}
			}
		}
	})
}

func validateAgentToolCalls(content string, tools []Tool, choices ...string) error {
	allowed := make(map[string]bool)
	for _, tool := range tools {
		allowed[tool.Function.Name] = true
	}
	// The legacy parser substitutes {} for missing arguments. Validate the
	// original JSON first so Agent calls never inherit that repair behavior.
	var candidates []string
	for _, match := range toolCallXMLRegex.FindAllStringSubmatch(content, -1) {
		candidates = append(candidates, match[1])
	}
	for _, match := range mdFenceRegex.FindAllStringSubmatch(content, -1) {
		candidates = append(candidates, match[1])
	}
	if len(candidates) == 0 {
		if json.Valid([]byte(strings.TrimSpace(content))) {
			candidates = append(candidates, content)
		} else {
			candidates = strings.Split(content, "\n")
		}
	}
	for _, candidate := range candidates {
		var raw map[string]json.RawMessage
		if json.Unmarshal([]byte(strings.TrimSpace(candidate)), &raw) != nil {
			if strings.Contains(candidate, `"name"`) || strings.Contains(candidate, `"tool_call"`) {
				return gatewayError("invalid_tool_call", 502, "Agent emitted malformed tool call JSON", false)
			}
			continue
		}
		if wrapped, ok := raw["tool_call"]; ok {
			if json.Unmarshal(wrapped, &raw) != nil {
				return gatewayError("invalid_tool_call", 502, "Agent emitted malformed tool call JSON", false)
			}
		}
		nameRaw, hasName := raw["name"]
		if !hasName {
			continue
		}
		var name string
		var arguments map[string]interface{}
		if json.Unmarshal(nameRaw, &name) != nil || (!allowed[name] && name != "__done__") || json.Unmarshal(raw["arguments"], &arguments) != nil || arguments == nil {
			return gatewayError("invalid_tool_call", 502, "Agent tool calls require a declared name and supplied object JSON arguments", false)
		}
		if name == "__done__" {
			if _, ok := arguments["result"].(string); !ok {
				return gatewayError("invalid_tool_call", 502, "Agent completion requires a text result", false)
			}
		}
	}
	prepared := prepareToolBridgeResponse(content, nil, true)
	choice := "auto"
	if len(choices) > 0 && choices[0] != "" {
		choice = choices[0]
	}
	if choice == "none" && prepared.HasCalls {
		return gatewayError("invalid_tool_call", 502, "Agent produced tool calls despite tool_choice=none", false)
	}
	if (choice == "any" || strings.HasPrefix(choice, "named:")) && !prepared.HasCalls {
		return gatewayError("invalid_tool_call", 502, "Agent did not produce the required tool call", false)
	}
	if !prepared.HasCalls {
		return nil
	} // Plain text is an honest end_turn.
	for _, call := range prepared.ToolCalls {
		if strings.HasPrefix(choice, "named:") && call.Function.Name != strings.TrimPrefix(choice, "named:") {
			return gatewayError("invalid_tool_call", 502, "Agent produced a different tool than the required tool", false)
		}
		var arguments map[string]interface{}
		if !allowed[call.Function.Name] || json.Unmarshal([]byte(call.Function.Arguments), &arguments) != nil || arguments == nil {
			return gatewayError("invalid_tool_call", 502, "Agent emitted an unknown tool or non-object JSON arguments", false)
		}
	}
	return nil
}

func agentAlias(model AgentModel) string {
	return "agent/" + strings.ToLower(normalizeModelName(model.Name))
}
