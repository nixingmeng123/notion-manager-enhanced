package proxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"notion-manager/internal/netutil"
)

// Object storage is a separate origin and may not negotiate HTTP/2. Notion's
// API transport deliberately uses HTTP/2; never reuse it for a signed upload.
var agentImageUploadHTTPClient = func(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: &http.Transport{
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return netutil.DialThroughProxy(ctx, network, address, AppConfig.NotionProxyURL())
		},
	}}
}

const (
	agentMaxImages          = 19 // Notion accepts one text block and at most 20 blocks total.
	agentMaxImageBytes      = 10 << 20
	agentMaxTotalImageBytes = 20 << 20
)

// Keep the filename/reference in the original turn, including image-only turns.
// Staged files are attached in the same order to the fresh replay thread.
func prepareAgentImages(ctx context.Context, messages []AnthropicMessage) ([]AnthropicMessage, []FileAttachment, error) {
	result := append([]AnthropicMessage(nil), messages...)
	var images []FileAttachment
	total := 0
	for mi, message := range result {
		blocks, ok := message.Content.([]interface{})
		if !ok {
			continue
		}
		content := append([]interface{}(nil), blocks...)
		for bi, value := range content {
			block, ok := value.(map[string]interface{})
			if !ok || block["type"] != "image" {
				continue
			}
			if message.Role != "user" || len(images) >= agentMaxImages {
				return nil, nil, gatewayError("unsupported_capability", 400, "Agent requests accept at most 19 images in user messages", false)
			}
			source, ok := block["source"].(map[string]interface{})
			if !ok {
				return nil, nil, invalidAgentImage()
			}
			image, err := readAgentImage(ctx, source)
			if err != nil {
				return nil, nil, err
			}
			total += len(image.Data)
			if total > agentMaxTotalImageBytes {
				return nil, nil, gatewayError("unsupported_capability", 400, "Agent image data exceeds the 20 MiB request limit", false)
			}
			image.FileName = fmt.Sprintf("image-%d%s", len(images)+1, mimeToExt(image.ContentType))
			images = append(images, image)
			content[bi] = map[string]interface{}{"type": "text", "text": "\n[Attached image: " + image.FileName + "]\n"}
		}
		result[mi].Content = content
	}
	return result, images, nil
}

func invalidAgentImage() error {
	return gatewayError("unsupported_capability", 400, "invalid image source; supply PNG, JPEG, GIF or WebP as base64 or a public HTTPS URL (maximum 10 MiB per image)", false)
}

func readAgentImage(ctx context.Context, source map[string]interface{}) (FileAttachment, error) {
	var data []byte
	var err error
	mediaType, _ := source["media_type"].(string)
	switch source["type"] {
	case "base64":
		encoded, ok := source["data"].(string)
		if !ok || len(encoded) == 0 || len(encoded) > base64.StdEncoding.EncodedLen(agentMaxImageBytes) {
			return FileAttachment{}, invalidAgentImage()
		}
		data, err = base64.StdEncoding.DecodeString(encoded)
	case "url":
		address, _ := source["url"].(string)
		data, err = downloadAgentImage(ctx, address)
	default:
		return FileAttachment{}, invalidAgentImage()
	}
	if ctx.Err() != nil {
		return FileAttachment{}, asGatewayError(ctx.Err())
	}
	if err != nil || len(data) == 0 || len(data) > agentMaxImageBytes {
		return FileAttachment{}, invalidAgentImage()
	}
	detected := http.DetectContentType(data)
	switch detected {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return FileAttachment{}, invalidAgentImage()
	}
	if mediaType != "" && mediaType != detected {
		return FileAttachment{}, invalidAgentImage()
	}
	return FileAttachment{Data: data, ContentType: detected}, nil
}

func publicAgentImageAddress(address string) (*url.URL, error) {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return nil, invalidAgentImage()
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !publicAgentImageIP(ip) {
		return nil, invalidAgentImage()
	}
	return u, nil
}

func publicAgentImageIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() &&
		!netip.MustParsePrefix("100.64.0.0/10").Contains(ip) && !netip.MustParsePrefix("192.0.0.0/24").Contains(ip)
}

// Resolve and connect to the checked IP in the same dial; redirects are checked
// again. No session cookie, API key, URL credentials or environment proxy is used.
func downloadAgentImage(ctx context.Context, address string) ([]byte, error) {
	u, err := publicAgentImageAddress(address)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, invalidAgentImage()
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil || len(ips) == 0 {
				return nil, invalidAgentImage()
			}
			for _, ip := range ips {
				if !publicAgentImageIP(ip) {
					return nil, invalidAgentImage()
				}
			}
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return invalidAgentImage()
		}
		_, err := publicAgentImageAddress(r.URL.String())
		return err
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, invalidAgentImage()
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, invalidAgentImage()
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.ContentLength > agentMaxImageBytes {
		return nil, invalidAgentImage()
	}
	return io.ReadAll(io.LimitReader(resp.Body, agentMaxImageBytes+1))
}

type agentFileUploadResponse struct {
	File struct {
		ID string `json:"id"`
	} `json:"file"`
	Upload struct {
		Type    string            `json:"type"`
		URL     string            `json:"url"`
		Method  string            `json:"method"`
		Headers map[string]string `json:"headers"`
	} `json:"upload"`
}

// This is the Agent Service staged-file protocol, distinct from Workflow's
// attachment: URI, S3 form and processAgentAttachment task flow.
func uploadAgentImage(ctx context.Context, acc *Account, image FileAttachment) (agentTextBlock, error) {
	target := map[string]string{"type": "user"}
	var created agentFileUploadResponse
	err := agentPostJSON(ctx, acc, "createAgentServiceFileUploadURL", map[string]interface{}{
		"spaceId": acc.SpaceID, "target": target, "filename": image.FileName,
		"mediaType": image.ContentType, "sizeBytes": len(image.Data),
	}, &created)
	if err != nil {
		return agentTextBlock{}, err
	}
	upload := created.Upload
	address, err := url.Parse(upload.URL)
	if err != nil || address.Scheme != "https" || address.Hostname() == "" || address.User != nil ||
		created.File.ID == "" || upload.Type != "single_part" || (upload.Method != "PUT" && upload.Method != "POST") {
		return agentTextBlock{}, gatewayError("image_upload", 502, "Notion returned an unsupported image upload descriptor", false)
	}
	req, err := http.NewRequestWithContext(ctx, upload.Method, upload.URL, bytes.NewReader(image.Data))
	if err != nil {
		return agentTextBlock{}, gatewayError("image_upload", 502, "invalid Notion image upload descriptor", false)
	}
	for key, value := range upload.Headers {
		switch strings.ToLower(key) {
		case "authorization", "cookie", "proxy-authorization", "host":
			return agentTextBlock{}, gatewayError("image_upload", 502, "unsupported Notion image upload headers", false)
		}
		req.Header.Set(key, value)
	}
	client := *agentImageUploadHTTPClient(AppConfig.APITimeoutDuration())
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if ctx.Err() != nil {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return agentTextBlock{}, asGatewayError(ctx.Err())
	}
	if err != nil {
		return agentTextBlock{}, gatewayError("image_upload", 502, "image upload failed", false)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return agentTextBlock{}, gatewayError("image_upload", 502, "image upload was rejected", false)
	}
	var completed struct {
		ID string `json:"id"`
	}
	if err := agentPostJSON(ctx, acc, "completeAgentServiceFileUpload", map[string]interface{}{
		"spaceId": acc.SpaceID, "target": target, "fileId": created.File.ID,
	}, &completed); err != nil {
		return agentTextBlock{}, err
	}
	if completed.ID != created.File.ID {
		return agentTextBlock{}, gatewayError("image_upload", 502, "Notion did not confirm the uploaded image", false)
	}
	return agentTextBlock{Type: "file", FileID: completed.ID}, nil
}
