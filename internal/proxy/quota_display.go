package proxy

import (
	"context"
	"encoding/json"
	"math"
	"time"
)

// Display-only observations. Workflow eligibility and Agent authorization keep
// their existing, independent checks; historical free usage is not a month cap.
type QuotaDisplay struct {
	Source        string              `json:"source"`
	Scope         string              `json:"scope"`
	AIUsage       AIUsageDisplay      `json:"ai_usage"`
	MemberCredits MemberCreditDisplay `json:"member_credits"`
}

type AIUsageWindow struct {
	Used        float64 `json:"used"`
	Limit       float64 `json:"limit"`
	UsedPercent float64 `json:"used_percent"`
	ResetsAtMS  int64   `json:"resets_at_ms,omitempty"`
	Window      string  `json:"window,omitempty"`
}

type AIUsageDisplay struct {
	Status    string         `json:"status"`
	Source    string         `json:"source"`
	CheckedAt time.Time      `json:"checked_at"`
	Monthly   *AIUsageWindow `json:"monthly,omitempty"`
	Rolling   *AIUsageWindow `json:"rolling,omitempty"`
}

type MemberCreditDisplay struct {
	Status string   `json:"status"`
	Used   *float64 `json:"used,omitempty"`
	Limit  *float64 `json:"limit,omitempty"`
}

type creditRateWindow struct {
	Scope       string   `json:"scope"`
	Used        *float64 `json:"used"`
	Limit       *float64 `json:"limit"`
	PeriodEndMS int64    `json:"periodEndMs"`
	Window      string   `json:"window"`
}

type creditRateStatus struct {
	Status              string            `json:"status"`
	Window              *creditRateWindow `json:"window"`
	BillingPeriodWindow *creditRateWindow `json:"billingPeriodWindow"`
}

func displayUsageWindow(raw *creditRateWindow) *AIUsageWindow {
	if raw == nil || raw.Scope != "per_user" || raw.Used == nil || raw.Limit == nil || *raw.Used < 0 || *raw.Limit <= 0 || math.IsNaN(*raw.Used) || math.IsInf(*raw.Used, 0) || math.IsNaN(*raw.Limit) || math.IsInf(*raw.Limit, 0) {
		return nil
	}
	return &AIUsageWindow{Used: *raw.Used, Limit: *raw.Limit, UsedPercent: 100 * (*raw.Used / *raw.Limit), ResetsAtMS: raw.PeriodEndMS, Window: raw.Window}
}

func fetchQuotaDisplay(acc *Account) *QuotaDisplay {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	display := &QuotaDisplay{Source: "getAIUsageEligibilityV2", Scope: "workspace_credits_and_lifetime_free", AIUsage: AIUsageDisplay{Status: "unavailable", Source: "getCreditRateLimitStatus", CheckedAt: time.Now()}, MemberCredits: MemberCreditDisplay{Status: "unavailable"}}
	var usage creditRateStatus
	if agentPostJSON(ctx, acc, "getCreditRateLimitStatus", map[string]string{"spaceId": acc.SpaceID}, &usage) == nil {
		switch usage.Status {
		case "within_limit", "rate_limited", "not_applicable":
			display.AIUsage.Status = usage.Status
			display.AIUsage.Monthly = displayUsageWindow(usage.BillingPeriodWindow)
			display.AIUsage.Rolling = displayUsageWindow(usage.Window)
		}
	}
	var credits struct {
		Type         string          `json:"type"`
		CurrentUsage *float64        `json:"currentUsage"`
		MonthlyLimit json.RawMessage `json:"monthlyLimit"`
	}
	if agentPostJSON(ctx, acc, "getPersonalAgentCreditUsage", map[string]string{"spaceId": acc.SpaceID}, &credits) == nil {
		switch credits.Type {
		case "not_configured", "not_applicable":
			display.MemberCredits.Status = credits.Type
		case "available":
			var limit float64
			if credits.CurrentUsage != nil && *credits.CurrentUsage >= 0 && json.Unmarshal(credits.MonthlyLimit, &limit) == nil && limit >= 0 {
				display.MemberCredits = MemberCreditDisplay{Status: "available", Used: credits.CurrentUsage, Limit: &limit}
			}
		}
	}
	return display
}

func cloneQuotaDisplay(src *QuotaDisplay) *QuotaDisplay {
	if src == nil {
		return nil
	}
	dst := *src
	if src.AIUsage.Monthly != nil {
		window := *src.AIUsage.Monthly
		dst.AIUsage.Monthly = &window
	}
	if src.AIUsage.Rolling != nil {
		window := *src.AIUsage.Rolling
		dst.AIUsage.Rolling = &window
	}
	if src.MemberCredits.Used != nil {
		value := *src.MemberCredits.Used
		dst.MemberCredits.Used = &value
	}
	if src.MemberCredits.Limit != nil {
		value := *src.MemberCredits.Limit
		dst.MemberCredits.Limit = &value
	}
	return &dst
}
