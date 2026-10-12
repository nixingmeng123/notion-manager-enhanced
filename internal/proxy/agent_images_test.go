package proxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func agentVisionFixture(t *testing.T) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 640, 320))
	draw.Draw(im, im.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	for y := 0; y < 320; y++ {
		for x := 0; x < 640; x++ {
			switch {
			case (x-110)*(x-110)+(y-160)*(y-160) <= 60*60:
				im.Set(x, y, color.RGBA{R: 235, G: 30, B: 30, A: 255})
			case x >= 260 && x < 380 && y >= 100 && y < 220:
				im.Set(x, y, color.RGBA{R: 30, G: 60, B: 235, A: 255})
			case y >= 80 && y <= 220 && x >= 530-(y-80)*80/140 && x <= 530+(y-80)*80/140:
				im.Set(x, y, color.RGBA{R: 25, G: 165, B: 45, A: 255})
			}
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func agentVisionAPIRequest(format string, pngData []byte, stream bool) map[string]interface{} {
	// The prompt deliberately does not contain the image's colors or shapes.
	prompt := "Describe the shapes and their colors in the attached image from left to right. Reply with only three comma-separated English color + shape pairs. Do not modify pages."
	encoded := base64.StdEncoding.EncodeToString(pngData)
	dataURI := "data:image/png;base64," + encoded
	body := map[string]interface{}{"model": "agent/opus-5.5", "stream": stream}
	switch format {
	case "messages":
		body["messages"] = []interface{}{map[string]interface{}{"role": "user", "content": []interface{}{
			map[string]interface{}{"type": "text", "text": prompt},
			map[string]interface{}{"type": "image", "source": map[string]interface{}{"type": "base64", "media_type": "image/png", "data": encoded}},
		}}}
	case "chat":
		body["messages"] = []interface{}{map[string]interface{}{"role": "user", "content": []interface{}{
			map[string]interface{}{"type": "text", "text": prompt},
			map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": dataURI}},
		}}}
	case "responses":
		body["input"] = []interface{}{map[string]interface{}{"role": "user", "content": []interface{}{
			map[string]interface{}{"type": "input_text", "text": prompt},
			map[string]interface{}{"type": "input_image", "image_url": dataURI},
		}}}
	}
	return body
}

func agentVisionHandler(pool *AccountPool, format string) http.HandlerFunc {
	switch format {
	case "messages":
		return HandleAnthropicMessages(pool)
	case "chat":
		return HandleOpenAIChatCompletions(pool)
	default:
		return HandleOpenAIResponses(pool)
	}
}

func TestAgentImageAPIAdapters(t *testing.T) {
	data := agentVisionFixture(t)
	for _, format := range []string{"messages", "chat", "responses"} {
		for _, stream := range []bool{false, true} {
			t.Run(format+map[bool]string{true: "_SSE", false: "_JSON"}[stream], func(t *testing.T) {
				var uploaded, completed, inference atomic.Bool
				storage := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "PUT" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
						t.Error("incorrect storage upload or credential forwarding")
					}
					got, _ := io.ReadAll(r.Body)
					if !bytes.Equal(got, data) || r.Header.Get("Content-Type") != "image/png" {
						t.Error("image bytes or type changed")
					}
					uploaded.Store(true)
					w.WriteHeader(200)
				}))
				defer storage.Close()
				pool, _ := agentFixture(t, func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/api/v3/getAvailableModels":
						writeAgentCatalog(w, true)
					case "/api/v3/createAgentServiceFileUploadURL":
						var payload struct {
							Target   map[string]string `json:"target"`
							Filename string            `json:"filename"`
							Size     int               `json:"sizeBytes"`
						}
						json.NewDecoder(r.Body).Decode(&payload)
						if payload.Target["type"] != "user" || payload.Filename != "image-1.png" || payload.Size != len(data) {
							t.Error("incorrect staged upload request")
						}
						json.NewEncoder(w).Encode(map[string]interface{}{"file": map[string]string{"id": "file-1"}, "upload": map[string]interface{}{"type": "single_part", "url": storage.URL, "method": "PUT", "headers": map[string]string{"Content-Type": "image/png"}}})
					case "/api/v3/completeAgentServiceFileUpload":
						if !uploaded.Load() {
							t.Error("completion before upload")
						}
						completed.Store(true)
						w.Write([]byte(`{"id":"file-1"}`))
					case "/api/v3/createAgentThread":
						var req agentCreateRequest
						json.NewDecoder(r.Body).Decode(&req)
						if !completed.Load() || len(req.Content) != 2 || req.Content[1].Type != "file" || req.Content[1].FileID != "file-1" || req.Content[1].Text != nil {
							t.Error("image not attached to inference")
						}
						text, _ := agentPlainText(req.Content[0].Text)
						if !strings.Contains(text, "[Attached image: image-1.png]") || strings.Contains(text, "data:image") {
							t.Error("image history reference missing or raw bytes leaked into prompt")
						}
						inference.Store(true)
						writeAgentCreated(w)
					case "/api/v3/getThreadTranscript":
						writeAgentTranscript(w, "red circle, blue square, green triangle", "finished")
					default:
						t.Errorf("unexpected endpoint %s", r.URL.Path)
					}
				})
				agentHTTPClient = func(time.Duration) *http.Client { return storage.Client() }
				previousUpload := agentImageUploadHTTPClient
				agentImageUploadHTTPClient = func(time.Duration) *http.Client { return storage.Client() }
				t.Cleanup(func() { agentImageUploadHTTPClient = previousUpload })
				body := agentVisionAPIRequest(format, data, stream)
				payload, _ := json.Marshal(body)
				rec := httptest.NewRecorder()
				agentVisionHandler(pool, format)(rec, httptest.NewRequest("POST", "/v1/"+format, bytes.NewReader(payload)))
				if rec.Code != 200 || !inference.Load() || !strings.Contains(rec.Body.String(), "red circle") {
					t.Fatalf("status=%d response=%s", rec.Code, rec.Body.String())
				}
				for _, secret := range []string{"private-cookie-value", storage.URL} {
					if strings.Contains(rec.Body.String(), secret) {
						t.Fatal("credential or storage URL leaked")
					}
				}
			})
		}
	}
}

func TestAgentImagesValidationAndHistory(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString(agentVisionFixture(t))
	block := map[string]interface{}{"type": "image", "source": map[string]interface{}{"type": "base64", "media_type": "image/png", "data": encoded}}
	original := []AnthropicMessage{{Role: "user", Content: []interface{}{block}}, {Role: "assistant", Content: "Earlier reply"}, {Role: "user", Content: []interface{}{map[string]interface{}{"type": "text", "text": "Compare with earlier"}, block}}}
	prepared, images, err := prepareAgentImages(context.Background(), original)
	if err != nil || len(images) != 2 || images[0].FileName != "image-1.png" || images[1].FileName != "image-2.png" {
		t.Fatalf("images=%v err=%v", len(images), err)
	}
	messages, files := convertAnthropicMessages(nil, prepared)
	if len(files) != 0 || len(messages) != 3 || !strings.Contains(messages[0].Content, "image-1.png") || !strings.Contains(messages[2].Content, "image-2.png") {
		t.Fatal("lost image-only turn or historical association")
	}
	if original[0].Content.([]interface{})[0].(map[string]interface{})["type"] != "image" {
		t.Fatal("mutated original request")
	}
	for _, source := range []map[string]interface{}{
		{"type": "base64", "media_type": "image/png", "data": "invalid"},
		{"type": "base64", "media_type": "image/jpeg", "data": encoded},
		{"type": "base64", "media_type": "image/png", "data": base64.StdEncoding.EncodeToString([]byte("not an image"))},
		{"type": "url", "url": "http://example.com/image.png"},
		{"type": "url", "url": "https://127.0.0.1/image.png"},
		{"type": "url", "url": "https://[::1]/image.png"},
		{"type": "url", "url": "https://user:secret@example.com/image.png"},
	} {
		if _, err := readAgentImage(context.Background(), source); err == nil || asGatewayError(err).Status != 400 {
			t.Fatal("invalid source accepted")
		}
	}
	for _, address := range []string{"127.0.0.1", "10.1.2.3", "169.254.169.254", "100.64.1.1", "::1", "fc00::1", "::ffff:127.0.0.1"} {
		if publicAgentImageIP(netip.MustParseAddr(address)) {
			t.Fatal("non-public address accepted")
		}
	}
	if !publicAgentImageIP(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("public address rejected")
	}
	tooMany := make([]interface{}, agentMaxImages+1)
	for i := range tooMany {
		tooMany[i] = block
	}
	if _, _, err := prepareAgentImages(context.Background(), []AnthropicMessage{{Role: "user", Content: tooMany}}); err == nil {
		t.Fatal("image block limit ignored")
	}
}

func TestAgentImageSizeLimitsAndCancellation(t *testing.T) {
	fixture := agentVisionFixture(t)
	oversized := make([]byte, agentMaxImageBytes+1)
	copy(oversized, fixture)
	if _, err := readAgentImage(context.Background(), map[string]interface{}{"type": "base64", "data": base64.StdEncoding.EncodeToString(oversized)}); err == nil {
		t.Fatal("per-image size limit ignored")
	}
	data := make([]byte, 7<<20)
	copy(data, fixture)
	block := map[string]interface{}{"type": "image", "source": map[string]interface{}{"type": "base64", "data": base64.StdEncoding.EncodeToString(data)}}
	if _, _, err := prepareAgentImages(context.Background(), []AnthropicMessage{{Role: "user", Content: []interface{}{block, block, block}}}); err == nil {
		t.Fatal("aggregate image size limit ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readAgentImage(ctx, map[string]interface{}{"type": "base64", "data": base64.StdEncoding.EncodeToString(fixture)}); err == nil || asGatewayError(err).Kind != "cancelled" {
		t.Fatal("cancelled image request accepted")
	}
}

func TestAgentImageUploadFailureStopsInference(t *testing.T) {
	for _, failure := range []string{"storage", "headers", "multipart", "completion"} {
		t.Run(failure, func(t *testing.T) {
			storage := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if failure == "storage" {
					w.WriteHeader(403)
					w.Write([]byte("signed-secret-url token_v2=private-cookie-value"))
					return
				}
				w.WriteHeader(200)
			}))
			defer storage.Close()
			var completion, inference atomic.Bool
			pool, _ := agentFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v3/getAvailableModels":
					writeAgentCatalog(w, true)
				case "/api/v3/createAgentServiceFileUploadURL":
					kind := "single_part"
					headers := map[string]string{"Content-Type": "image/png"}
					if failure == "multipart" {
						kind = "multipart"
					}
					if failure == "headers" {
						headers["Cookie"] = "private-cookie-value"
					}
					json.NewEncoder(w).Encode(map[string]interface{}{"file": map[string]string{"id": "file-1"}, "upload": map[string]interface{}{"type": kind, "url": storage.URL + "?signature=signed-secret-url", "method": "PUT", "headers": headers}})
				case "/api/v3/completeAgentServiceFileUpload":
					completion.Store(true)
					w.Write([]byte(`{"id":"different-file"}`))
				case "/api/v3/createAgentThread":
					inference.Store(true)
					writeAgentCreated(w)
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
				}
			})
			previous := agentImageUploadHTTPClient
			agentImageUploadHTTPClient = func(time.Duration) *http.Client { return storage.Client() }
			t.Cleanup(func() { agentImageUploadHTTPClient = previous })
			body := agentVisionAPIRequest("messages", agentVisionFixture(t), false)
			payload, _ := json.Marshal(body)
			rec := httptest.NewRecorder()
			agentVisionHandler(pool, "messages")(rec, httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(payload)))
			if rec.Code != 502 || inference.Load() || (failure != "completion" && completion.Load()) {
				t.Fatal("incomplete upload reached inference")
			}
			for _, secret := range []string{"private-cookie-value", "signed-secret-url", storage.URL} {
				if strings.Contains(rec.Body.String(), secret) {
					t.Fatal("upload error leaked secret data")
				}
			}
		})
	}
}

// Optional diagnostic through the real API adapters, with a synthetic image
// whose answer is absent from the prompt. Credentials and upload URLs are never
// written to a trace, a test log or the repository.
func TestAgentV3LiveVision(t *testing.T) {
	if os.Getenv("NOTION_LIVE_VISION") != "1" {
		t.Skip("live vision not enabled")
	}
	data, err := os.ReadFile(os.Getenv("NOTION_LIVE_ACCOUNT_FILE"))
	var acc Account
	if err != nil || json.Unmarshal(data, &acc) != nil || acc.TokenV2 == "" || acc.SpaceID == "" {
		t.Fatal("incomplete live account")
	}
	previous := AppConfig
	AppConfig = DefaultConfig()
	AppConfig.AgentV3.Enabled = true
	AppConfig.AgentV3.TimeoutSeconds = 180
	AppConfig.AgentV3.MinRequestIntervalSeconds = 15
	AppConfig.Server.AccountsDir = ""
	AppConfig.Proxy.EnableWebSearch = boolPtr(false)
	t.Cleanup(func() { AppConfig = previous })
	pool := NewAccountPool()
	pool.accounts = []*Account{&acc}
	fixture := agentVisionFixture(t)
	if output := os.Getenv("NOTION_LIVE_VISION_FIXTURE"); output != "" {
		if err := os.WriteFile(output, fixture, 0600); err != nil {
			t.Fatal("cannot save synthetic vision fixture")
		}
	}
	for _, format := range []string{"messages", "chat", "responses"} {
		stream := format == "responses"
		body := agentVisionAPIRequest(format, fixture, stream)
		payload, _ := json.Marshal(body)
		rec := httptest.NewRecorder()
		start := time.Now()
		agentVisionHandler(pool, format)(rec, httptest.NewRequest("POST", "/v1/"+format, bytes.NewReader(payload)))
		answer := strings.ToLower(rec.Body.String())
		t.Logf("vision API=%s stream=%v status=%d elapsed=%s", format, stream, rec.Code, time.Since(start).Round(time.Millisecond))
		if rec.Code != 200 {
			t.Fatalf("vision error: %s", redactSensitiveText(strings.ReplaceAll(rec.Body.String(), acc.TokenV2, "[REDACTED]")))
		}
		for _, word := range []string{"red", "circle", "blue", "square", "green", "triangle"} {
			if !strings.Contains(answer, word) {
				t.Fatalf("image recognition missing %s", word)
			}
		}
		if stream && !strings.Contains(answer, "response.completed") {
			t.Fatal("missing vision SSE completion")
		}
		t.Logf("vision passed, model=%s", acc.agentSnapshot().Models[0].ActualSlug)
	}
}
