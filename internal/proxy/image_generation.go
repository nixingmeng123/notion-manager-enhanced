package proxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const notionImageModel = "notion-image"
const maxGeneratedImageBytes = 20 << 20

// The native endpoint selects its own provider/model. It does not take a
// model ID, and its image permissions are independent of the chat catalog.
type ImageCapability struct {
	Status        string    `json:"status"`
	Model         string    `json:"model,omitempty"`
	Provider      string    `json:"provider,omitempty"`
	CheckedAt     time.Time `json:"checked_at"`
	CooldownUntil time.Time `json:"cooldown_until,omitempty"`
	Generations   int       `json:"generations"`
}

func (acc *Account) imageSnapshot() ImageCapability {
	acc.mu.RLock()
	defer acc.mu.RUnlock()
	if acc.ImageCapability == nil {
		return ImageCapability{Status: "unknown"}
	}
	return *acc.ImageCapability
}

func imageAccountUsable(pool *AccountPool, acc *Account) bool {
	return acc.SpaceID != "" && acc.UserID != "" && (acc.TokenV2 != "" || acc.FullCookie != "") &&
		!pool.hasNoWorkspace(acc) && !time.Now().Before(acc.imageSnapshot().CooldownUntil)
}

func pickImageAccount(pool *AccountPool) *Account {
	var selected *Account
	best := -1
	for _, acc := range pool.AccountsSnapshot() {
		if !imageAccountUsable(pool, acc) {
			continue
		}
		score := 0
		if acc.agentSnapshot().Status == "available" {
			score = 1 // Prefer a working login while image permission is unknown.
		}
		if acc.imageSnapshot().Status == "confirmed" {
			score = 2
		}
		if score > best {
			selected, best = acc, score
		}
	}
	return selected
}

type imageGenerationRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              *int   `json:"n,omitempty"`
	Size           string `json:"size,omitempty"`
	Quality        string `json:"quality,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
	User           string `json:"user,omitempty"`
}

type generatedImage struct {
	Base64   string `json:"b64_json"`
	Model    string `json:"model,omitempty"`
	Provider string `json:"provider,omitempty"`
	MIME     string `json:"mime_type"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

var imageGenerationHTTPClient = func(sess *ProxySession) *http.Client {
	return reverseProxyHTTPClient(0, sess)
}
var imageIdentity = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,79}$`)

func imageError(kind string, status int, message string) *GatewayError {
	return gatewayError(kind, status, message, false)
}

// Do not copy upstream messages, URLs or headers into errors. A generation is
// never retried automatically: a failed connection may still have consumed a
// generation upstream.
func classifyImageError(status int, data []byte) *GatewayError {
	var detail struct {
		Code      string `json:"code"`
		ErrorCode string `json:"errorCode"`
		Error     struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(data, &detail)
	code := strings.ToUpper(detail.Code + " " + detail.ErrorCode + " " + detail.Error.Code)
	switch {
	case strings.Contains(code, "CONTENT_POLICY_VIOLATION"):
		return imageError("content_policy", 400, "Notion rejected the image prompt under its content policy")
	case strings.Contains(code, "RATE_LIMIT_EXCEEDED") || status == 429:
		return imageError("rate_limit", 429, "Notion image generation limit reached")
	case strings.Contains(code, "CREDIT_LIMIT_EXCEEDED"):
		return imageError("quota", 429, "Notion image generation credits are exhausted")
	case status == 401:
		return imageError("authentication", 401, "Notion rejected the image generation login")
	case status == 403:
		return imageError("model_permission", 403, "Notion denied image generation for this account or workspace")
	case status == 408 || status == 504:
		return imageError("timeout", 504, "Notion image generation timed out")
	default:
		return imageError("upstream_error", 502, "Notion image generation failed (generateAiImage)")
	}
}

func generateNotionImage(ctx context.Context, acc *Account, prompt string) (generatedImage, error) {
	body, _ := json.Marshal(map[string]interface{}{"spaceId": acc.SpaceID, "prompt": prompt, "n": 1})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, notionOrigin+"/api/v3/generateAiImage", bytes.NewReader(body))
	if err != nil {
		return generatedImage{}, imageError("configuration", 500, "Image generation request could not be constructed")
	}
	sess := newProxySession(acc)
	setProxySessionCookies(req, sess)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Origin", notionOrigin)
	req.Header.Set("Referer", notionReferer)
	req.Header.Set("x-notion-active-user-header", acc.UserID)
	req.Header.Set("x-notion-space-id", acc.SpaceID)
	req.Header.Set("notion-client-version", acc.ClientVersion)
	if AppConfig != nil {
		req.Header.Set("User-Agent", AppConfig.Browser.UserAgent)
	}
	resp, err := imageGenerationHTTPClient(sess).Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return generatedImage{}, imageError("cancelled", 499, "Image generation cancelled")
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return generatedImage{}, imageError("timeout", 504, "Notion image generation timed out")
		}
		return generatedImage{}, imageError("upstream_error", 502, "Notion image generation connection failed")
	}
	defer resp.Body.Close()
	reader, cleanup, err := decompressBody(resp)
	if err != nil {
		return generatedImage{}, imageError("upstream_error", 502, "Unsupported image generation response encoding")
	}
	if cleanup != nil {
		defer cleanup()
	}
	limit := int64(base64.StdEncoding.EncodedLen(maxGeneratedImageBytes) + 65536)
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return generatedImage{}, imageError("cancelled", 499, "Image generation cancelled")
		}
		return generatedImage{}, imageError("timeout", 504, "Notion image generation timed out")
	}
	if err != nil || int64(len(data)) > limit {
		return generatedImage{}, imageError("upstream_error", 502, "Image generation response exceeds the size limit or could not be read")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return generatedImage{}, classifyImageError(resp.StatusCode, data)
	}
	var result struct {
		Images []struct {
			URL      string `json:"url"`
			Model    string `json:"model"`
			Provider string `json:"provider"`
		} `json:"images"`
	}
	if json.Unmarshal(data, &result) != nil || len(result.Images) != 1 {
		return generatedImage{}, classifyImageError(resp.StatusCode, data)
	}
	im := result.Images[0]
	prefix, encoded, found := strings.Cut(im.URL, ",")
	if !found || (prefix != "data:image/jpeg;base64" && prefix != "data:image/png;base64") ||
		len(encoded) > base64.StdEncoding.EncodedLen(maxGeneratedImageBytes) {
		return generatedImage{}, imageError("upstream_error", 502, "Notion returned an unsupported image representation")
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(decoded) == 0 || len(decoded) > maxGeneratedImageBytes {
		return generatedImage{}, imageError("upstream_error", 502, "Notion returned invalid image data")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(decoded))
	if err != nil || (format != "jpeg" && format != "png") || config.Width < 1 || config.Height < 1 ||
		int64(config.Width)*int64(config.Height) > 32_000_000 || "data:image/"+format+";base64" != prefix {
		return generatedImage{}, imageError("upstream_error", 502, "Notion returned an invalid image format or dimensions")
	}
	if !imageIdentity.MatchString(im.Model) {
		im.Model = ""
	}
	if !imageIdentity.MatchString(im.Provider) {
		im.Provider = ""
	}
	return generatedImage{Base64: base64.StdEncoding.EncodeToString(decoded), Model: im.Model, Provider: im.Provider,
		MIME: "image/" + format, Width: config.Width, Height: config.Height}, nil
}

func HandleImageGenerations(pool *AccountPool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Notion-Protocol", "image")
		w.Header().Set("X-Notion-Usage", "unavailable")
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeOpenAIError(w, 405, "method not allowed", "invalid_request_error", "")
			return
		}
		var request imageGenerationRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&request) != nil || decoder.Decode(new(interface{})) != io.EOF {
			writeOpenAIError(w, 400, "invalid image request JSON or unsupported parameter", "invalid_request_error", "")
			return
		}
		if strings.TrimSpace(request.Prompt) == "" || !utf8.ValidString(request.Prompt) || utf8.RuneCountInString(request.Prompt) > 8192 {
			writeOpenAIError(w, 400, "prompt must contain 1 to 8192 characters", "invalid_request_error", "prompt")
			return
		}
		checks := []struct {
			invalid        bool
			param, message string
		}{
			{request.Model != "" && request.Model != notionImageModel, "model", "Use notion-image; Notion selects the image provider/model automatically"},
			{request.N != nil && *request.N != 1, "n", "Only n=1 is supported"},
			{request.Size != "" && request.Size != "auto", "size", "Only size=auto is verified; omit size to use Notion's native dimensions"},
			{request.Quality != "" && request.Quality != "auto", "quality", "Only quality=auto is supported"},
			{request.ResponseFormat != "" && request.ResponseFormat != "b64_json", "response_format", "Only response_format=b64_json is supported"},
		}
		for _, check := range checks {
			if check.invalid {
				writeOpenAIError(w, 400, check.message, "invalid_request_error", check.param)
				return
			}
		}
		acc := pickImageAccount(pool)
		if acc == nil {
			writeOpenAIError(w, 503, "No image generation account is available; check workspace permission or wait for its cooldown", "overloaded_error", "")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout())
		defer cancel()
		result, err := generateNotionImage(ctx, acc, request.Prompt)
		acc.mu.Lock()
		if acc.ImageCapability == nil {
			acc.ImageCapability = &ImageCapability{Status: "unknown"}
		}
		capability := acc.ImageCapability
		if err == nil {
			capability.Status, capability.Model, capability.Provider = "confirmed", result.Model, result.Provider
			capability.CheckedAt, capability.CooldownUntil = time.Now(), time.Time{}
			capability.Generations++
		} else {
			failure := asGatewayError(err)
			if failure.Kind == "authentication" || failure.Kind == "model_permission" || failure.Kind == "quota" || failure.Kind == "rate_limit" {
				capability.Status, capability.CooldownUntil = failure.Kind, time.Now().Add(time.Minute)
			}
		}
		acc.mu.Unlock()
		if AppConfig != nil && AppConfig.Server.AccountsDir != "" {
			_ = saveAccountFile(AppConfig.Server.AccountsDir, acc)
		}
		if err != nil {
			failure := asGatewayError(err)
			errType := agentErrorType(failure)
			if failure.Kind == "content_policy" {
				errType = "invalid_request_error"
			}
			writeOpenAIError(w, failure.Status, failure.Message, errType, "")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Created int64            `json:"created"`
			Data    []generatedImage `json:"data"`
		}{Created: time.Now().Unix(), Data: []generatedImage{result}})
	}
}
