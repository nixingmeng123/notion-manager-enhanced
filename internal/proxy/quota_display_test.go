package proxy

import (
	"encoding/json"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestQuotaDisplaySeparatesMonthlyAndSharedCredits(t *testing.T) {
	pool, acc := agentFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-notion-active-user-header") != "user" {
			t.Error("wrong member identity")
		}
		switch r.URL.Path {
		case "/api/v3/getCreditRateLimitStatus":
			w.Write([]byte(`{"status":"within_limit","window":{"scope":"per_user","window":"6h","used":0,"limit":100},"billingPeriodWindow":{"scope":"per_user","used":14.88,"limit":100,"periodEndMs":1794189600000}}`))
		case "/api/v3/getPersonalAgentCreditUsage":
			w.Write([]byte(`{"type":"not_configured"}`))
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
	})
	display := fetchQuotaDisplay(acc)
	if display.AIUsage.Monthly == nil || math.Abs(display.AIUsage.Monthly.UsedPercent-14.88) > 1e-8 || display.MemberCredits.Status != "not_configured" || display.MemberCredits.Limit != nil {
		t.Fatalf("incorrect display: %+v", display)
	}
	acc.setQuotaInfo(&QuotaInfo{IsEligible: true, SpaceUsage: 457, SpaceLimit: 550, UserUsage: 453, UserLimit: 50, PremiumBalance: 300, PremiumLimit: 300, Display: display}, nil)
	other := &Account{UserID: "another-user", SpaceID: acc.SpaceID, UserEmail: "other@example.test", QuotaInfo: &QuotaInfo{IsEligible: true, SpaceUsage: 457, SpaceLimit: 550, UserUsage: 4, UserLimit: 50, PremiumBalance: 300, PremiumLimit: 300, Display: cloneQuotaDisplay(display)}}
	other.QuotaInfo.Display.AIUsage.Monthly.Used = 0
	other.QuotaInfo.Display.AIUsage.Monthly.UsedPercent = 0
	pool.accounts = append(pool.accounts, other)
	details := pool.GetAccountDetails()
	summary := summarizeAccounts(details)
	if summary.WorkspaceCount != 1 || summary.TotalPremiumBalance != 300 || summary.TotalPremiumLimit != 300 || summary.TotalSpaceUsage != 457 || summary.MonthlyUsageKnown != 2 || summary.HighestMonthlyUsedPercent == nil || math.Abs(*summary.HighestMonthlyUsedPercent-14.88) > 1e-8 {
		t.Fatalf("shared balance counted per member or monthly percentage lost: %+v", summary)
	}
	if details[0]["user_usage"] != 453 || details[1]["user_usage"] != 4 || display.AIUsage.Monthly.Used != 14.88 {
		t.Fatal("legacy values or independent member observation mutated")
	}
}

func TestQuotaDisplayUnknownIsNotZero(t *testing.T) {
	for _, mode := range []string{"failure", "missing", "workspace_scope", "not_applicable"} {
		t.Run(mode, func(t *testing.T) {
			_, acc := agentFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if mode == "failure" {
					w.WriteHeader(403)
					w.Write([]byte(`{"message":"token_v2=secret-cookie"}`))
					return
				}
				if r.URL.Path == "/api/v3/getPersonalAgentCreditUsage" {
					w.Write([]byte(`{"type":"not_configured"}`))
					return
				}
				switch mode {
				case "missing":
					w.Write([]byte(`{"status":"within_limit","billingPeriodWindow":{"scope":"per_user","limit":100}}`))
				case "workspace_scope":
					w.Write([]byte(`{"status":"within_limit","billingPeriodWindow":{"scope":"per_space","used":10,"limit":100}}`))
				case "not_applicable":
					w.Write([]byte(`{"status":"not_applicable"}`))
				}
			})
			display := fetchQuotaDisplay(acc)
			if display.AIUsage.Monthly != nil || display.AIUsage.Rolling != nil || display.MemberCredits.Used != nil {
				t.Fatal("missing or unrelated usage converted to zero")
			}
			if mode == "failure" && display.AIUsage.Status != "unavailable" {
				t.Fatal("upstream failure hidden")
			}
		})
	}
}

func TestQuotaDisplayPersistsAndSnapshotsAreIndependent(t *testing.T) {
	dir := t.TempDir()
	acc := makeTestAccount("user", "test@example.test", "space", "workspace", 457, 550)
	acc.QuotaInfo.Display = &QuotaDisplay{Source: "getAIUsageEligibilityV2", AIUsage: AIUsageDisplay{Status: "within_limit", Monthly: &AIUsageWindow{Used: 14.88, Limit: 100, UsedPercent: 14.88}}, MemberCredits: MemberCreditDisplay{Status: "not_configured"}}
	name, err := SaveAccountToFile(acc, dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if filepath.IsAbs(name) {
		path = name
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded := loadPersistedQuotaInfo(data)
	if loaded.Display == nil || loaded.Display.AIUsage.Monthly.Used != 14.88 {
		t.Fatal("initial account save lost monthly display")
	}
	acc.QuotaInfo.Display.AIUsage.Monthly.Used = 22.5
	if err := saveAccountFile(dir, acc); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded = loadPersistedQuotaInfo(data)
	if loaded.Display.AIUsage.Monthly.Used != 22.5 {
		t.Fatal("refresh persistence lost monthly display")
	}
	snapshot := acc.quotaSnapshot()
	snapshot.Info.Display.AIUsage.Monthly.Used = 99
	if acc.quotaSnapshot().Info.Display.AIUsage.Monthly.Used != 22.5 {
		t.Fatal("snapshot shares mutable display pointers")
	}
	legacy := loadPersistedQuotaInfo([]byte(`{"quota_info":{"space_usage":1,"user_usage":1}}`))
	if legacy == nil || legacy.Display != nil {
		t.Fatal("legacy account data invented current usage")
	}
}

func TestQuotaDisplayMemberCreditLimitIsSeparate(t *testing.T) {
	_, acc := agentFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/getCreditRateLimitStatus" {
			w.Write([]byte(`{"status":"not_applicable"}`))
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"type": "available", "currentUsage": 12.5, "monthlyLimit": 75})
	})
	display := fetchQuotaDisplay(acc)
	if display.AIUsage.Monthly != nil || display.MemberCredits.Status != "available" || *display.MemberCredits.Used != 12.5 || *display.MemberCredits.Limit != 75 {
		t.Fatal("member spending cap conflated with plan allowance")
	}
}
