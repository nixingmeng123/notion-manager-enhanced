package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"notion-manager/internal/proxy"
)

func TestEnhancedRoutesRetainAPIAuthentication(t *testing.T) {
	old := proxy.AppConfig
	proxy.AppConfig = proxy.DefaultConfig()
	proxy.AppConfig.AgentV3.Enabled = true
	t.Cleanup(func() { proxy.AppConfig = old })
	pool := proxy.NewAccountPool()
	auth := proxy.NewDashboardAuth(proxy.HashAdminPassword("admin-secret"), "api-secret")
	mux := newMux(pool, t.TempDir(), "api-secret", auth, nil, nil)
	handler := apiKeyAuthMiddleware("api-secret", mux)
	for _, path := range []string{"/v1/messages", "/v1/chat/completions", "/v1/responses", "/v1/models", "/models"} {
		method := "POST"
		if strings.HasSuffix(path, "models") {
			method = "GET"
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(`{"model":"agent/opus-5.5","messages":[{"role":"user","content":"hi"}],"input":"hi"}`)))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous %s status=%d", path, rec.Code)
		}
	}
	for _, path := range []string{"/admin/accounts", "/admin/models", "/admin/settings", "/dashboard/"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Accept", "application/json")
		handler.ServeHTTP(rec, req)
		if rec.Code == 200 {
			t.Fatalf("anonymous admin access allowed: %s", path)
		}
	}
}
