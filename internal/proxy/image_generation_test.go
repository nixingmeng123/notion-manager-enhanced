package proxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func imageGenerationFixture(t *testing.T, transport http.RoundTripper) (*AccountPool, *Account, string) {
	t.Helper()
	oldConfig, oldClient := AppConfig, imageGenerationHTTPClient
	AppConfig = DefaultConfig()
	AppConfig.Server.AccountsDir = ""
	imageGenerationHTTPClient = func(*ProxySession) *http.Client { return &http.Client{Transport: transport} }
	t.Cleanup(func() { AppConfig, imageGenerationHTTPClient = oldConfig, oldClient })
	acc := &Account{TokenV2: "private-notion-token", UserID: "user", SpaceID: "space", ClientVersion: DefaultClientVersion}
	pool := NewAccountPool()
	pool.accounts = []*Account{acc}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 8, 6))); err != nil {
		t.Fatal(err)
	}
	return pool, acc, base64.StdEncoding.EncodeToString(buffer.Bytes())
}

func imageNativeResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestImageGenerationNativeContractAndPersistence(t *testing.T) {
	var encoded string
	calls := 0
	pool, acc, value := imageGenerationFixture(t, reverseProxyRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://app.notion.com/api/v3/generateAiImage" || r.Method != "POST" {
			t.Fatal("wrong native endpoint")
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("x-api-key") != "" {
			t.Fatal("client key forwarded upstream")
		}
		if r.Header.Get("Cookie") != "token_v2=private-notion-token; notion_user_id=user; notion_users=%5B%22user%22%5D" {
			t.Fatal("missing stable account cookie")
		}
		if r.Header.Get("x-notion-active-user-header") != "user" || r.Header.Get("x-notion-space-id") != "space" {
			t.Fatal("missing workspace/user selector")
		}
		var payload map[string]interface{}
		if json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload) != 3 || payload["prompt"] != "pelican" || payload["n"] != float64(1) || payload["spaceId"] != "space" {
			t.Fatalf("unexpected native payload: %v", payload)
		}
		return imageNativeResponse(200, `{"images":[{"url":"data:image/png;base64,`+encoded+`","model":"nano-banana","provider":"google"}]}`), nil
	}))
	encoded = value
	AppConfig.Server.AccountsDir = t.TempDir()
	if _, err := SaveAccountToFile(acc, AppConfig.Server.AccountsDir); err != nil {
		t.Fatal(err)
	}
	if len(buildClientModels(pool)) != 0 {
		t.Fatal("unverified image model advertised")
	}
	req := httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(`{"model":"notion-image","prompt":"pelican","size":"auto","n":1,"response_format":"b64_json"}`))
	req.Header.Set("Authorization", "Bearer client-secret")
	req.Header.Set("Cookie", "hostile-client-cookie")
	w := httptest.NewRecorder()
	HandleImageGenerations(pool)(w, req)
	if w.Code != 200 || calls != 1 {
		t.Fatalf("code=%d calls=%d body=%s", w.Code, calls, w.Body.String())
	}
	var result struct {
		Data []generatedImage `json:"data"`
	}
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Data) != 1 || result.Data[0].Base64 != encoded || result.Data[0].Width != 8 || result.Data[0].Height != 6 {
		t.Fatal("image compatibility response corrupted")
	}
	reloaded := NewAccountPool()
	if err := reloaded.LoadFromDir(AppConfig.Server.AccountsDir); err != nil {
		t.Fatal(err)
	}
	capability := reloaded.AccountsSnapshot()[0].imageSnapshot()
	if capability.Status != "confirmed" || capability.Model != "nano-banana" || capability.Provider != "google" || capability.Generations != 1 {
		t.Fatalf("capability not persisted: %+v", capability)
	}
	models := buildClientModels(reloaded)
	if len(models) != 1 || models[0].ID != "notion-image" || models[0].Protocol != "image" {
		t.Fatal("verified image model missing")
	}
	if _, err := resolveInferenceRoute("notion-image"); err == nil {
		t.Fatal("image model accepted as chat model")
	}
}

func TestImageGenerationRejectsUnsupportedParametersBeforeUpstream(t *testing.T) {
	pool, _, _ := imageGenerationFixture(t, reverseProxyRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid request reached upstream")
		return nil, nil
	}))
	for _, body := range []string{
		`{"prompt":""}`, `{"prompt":"pelican","n":0}`, `{"prompt":"pelican","n":2}`,
		`{"prompt":"pelican","model":"nano-banana"}`, `{"prompt":"pelican","size":"1024x1024"}`,
		`{"prompt":"pelican","response_format":"url"}`, `{"prompt":"pelican","quality":"hd"}`,
		`{"prompt":"pelican","stream":true}`, `{"prompt":"pelican","blockPointer":{}}`,
		`{"prompt":"pelican"} {}`, `{"prompt":"` + strings.Repeat("a", 8193) + `"}`,
	} {
		w := httptest.NewRecorder()
		HandleImageGenerations(pool)(w, httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("invalid body status=%d", w.Code)
		}
	}
}

func TestImageGenerationFailuresAreSanitizedAndNeverRetried(t *testing.T) {
	for _, tc := range []struct {
		name, body     string
		upstream, want int
	}{
		{"permission", `{"message":"token_v2=private-notion-token https://storage.invalid/?signature=secret"}`, 403, 403},
		{"semantic quota", `{"error":{"code":"CREDIT_LIMIT_EXCEEDED","message":"Bearer private-client-key"}}`, 200, 429},
		{"policy", `{"code":"CONTENT_POLICY_VIOLATION"}`, 400, 400},
		{"empty", `{"images":[]}`, 200, 502},
		{"malformed", `{not-json`, 200, 502},
		{"remote URL", `{"images":[{"url":"https://127.0.0.1/secret"}]}`, 200, 502},
		{"fake image", `{"images":[{"url":"data:image/png;base64,PGh0bWw+PC9odG1sPg=="}]}`, 200, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			pool, acc, _ := imageGenerationFixture(t, reverseProxyRoundTripper(func(*http.Request) (*http.Response, error) {
				calls++
				return imageNativeResponse(tc.upstream, tc.body), nil
			}))
			w := httptest.NewRecorder()
			HandleImageGenerations(pool)(w, httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(`{"prompt":"pelican"}`)))
			if w.Code != tc.want || calls != 1 {
				t.Fatalf("code=%d calls=%d body=%s", w.Code, calls, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "private-") || strings.Contains(w.Body.String(), "signature=") {
				t.Fatal("upstream secret leaked")
			}
			if acc.imageSnapshot().Status == "confirmed" || len(buildClientModels(pool)) != 0 {
				t.Fatal("failed image marked available")
			}
		})
	}
}

func TestImageGenerationCancellationAndAccountSelection(t *testing.T) {
	pool, acc, _ := imageGenerationFixture(t, reverseProxyRoundTripper(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() }))
	acc.QuotaExhaustedAt = new(time.Time) // Workflow quota must not disable images.
	other := &Account{TokenV2: "other", UserID: "other", SpaceID: "other", ImageCapability: &ImageCapability{Status: "confirmed"}}
	pool.accounts = append(pool.accounts, other)
	if pickImageAccount(pool) != other {
		t.Fatal("confirmed image account not preferred")
	}
	other.ImageCapability.CooldownUntil = time.Now().Add(time.Minute)
	if pickImageAccount(pool) != acc {
		t.Fatal("image cooldown or independent quota ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := generateNotionImage(ctx, acc, "pelican")
	if err == nil || asGatewayError(err).Status != 499 {
		t.Fatalf("cancellation not respected: %v", err)
	}
}
