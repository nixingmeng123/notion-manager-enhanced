package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestClientModelsOnlyExposeVerifiedAccountModels(t *testing.T) {
	pool, acc := agentFixture(t, func(w http.ResponseWriter, r *http.Request) { writeAgentCatalog(w, true) })
	acc.setModels([]ModelEntry{{ID: "avocado-froyo-medium", Name: "Opus 4.6"}, {ID: "avocado-froyo-medium", Name: "Opus 4.6"}})
	acc.updateAgentCapability(func(c *AgentCapability) {
		c.CheckedAt = time.Now().Add(-time.Hour)
		c.Models = []AgentModelAuthorization{
			{Slug: "albuquerque-quinn", Authorization: "confirmed", ActualSlug: "albuquerque-quinn"},
			{Slug: "acai-budino-high", Authorization: "catalog_enabled"},
			{Slug: "omniberry-sundae", Authorization: "confirmed", ActualSlug: "albuquerque-quinn"},
			{Slug: "fireworks-kimi-k3", Authorization: "denied"},
		}
	})
	for _, defaultProtocol := range []string{"workflow", "agent"} {
		AppConfig.Proxy.DefaultProtocol = defaultProtocol
		rec := httptest.NewRecorder()
		HandlePublicModels(pool)(rec, httptest.NewRequest("GET", "/v1/models", nil))
		var response publicModelResponse
		if json.Unmarshal(rec.Body.Bytes(), &response) != nil {
			t.Fatal("invalid models response")
		}
		var ids []string
		for _, model := range response.Data {
			ids = append(ids, model.ID)
			if !model.Available {
				t.Fatalf("unavailable candidate exposed to a client picker: %+v", model)
			}
		}
		workflowID := "opus-4.6"
		if defaultProtocol == "agent" {
			workflowID = "workflow/" + workflowID
		}
		if !reflect.DeepEqual(ids, []string{"agent/opus-5.5", workflowID}) {
			t.Fatalf("unexpected client models for default=%s: %v", defaultProtocol, ids)
		}
	}
	acc.updateAgentCapability(func(c *AgentCapability) { c.CooldownUntil = time.Now().Add(time.Minute) })
	if models := buildClientModels(pool); len(models) != 1 || models[0].Protocol != "workflow" {
		t.Fatal("Agent account cooldown was ignored")
	}
}

func TestClientModelsKeepConfirmedIdentityAcrossRestrictedCatalogRefresh(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(map[bool]string{false: "restricted_catalog", true: "explicit_denial"}[denied], func(t *testing.T) {
			pool, acc := agentFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if denied {
					w.Write([]byte(`{"models":[{"model":"albuquerque-quinn","isDisabled":true}],"modelSelectionRestricted":true}`))
				} else {
					w.Write([]byte(`{"models":[],"modelSelectionRestricted":true}`))
				}
			})
			acc.setModels(nil)
			acc.updateAgentCapability(func(c *AgentCapability) {
				c.Models = []AgentModelAuthorization{{Slug: "albuquerque-quinn", Authorization: "confirmed", ActualSlug: "albuquerque-quinn", ActualEffort: "medium"}}
			})
			if err := FetchAgentModels(context.Background(), acc); err != nil {
				t.Fatal(err)
			}
			models := buildClientModels(pool)
			if denied {
				if len(models) != 0 || acc.agentSnapshot().Models[0].Authorization != "denied" {
					t.Fatal("explicit denial did not revoke client discovery")
				}
			} else if len(models) != 1 || models[0].ID != "agent/opus-5.5" || acc.agentSnapshot().Models[0].ActualEffort != "medium" {
				t.Fatal("restricted catalog erased verified model identity")
			}
		})
	}
}
