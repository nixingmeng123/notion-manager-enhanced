# Native Notion image generation

`POST /v1/images/generations` calls Notion's native `generateAiImage` endpoint using an imported account. It is independent of Personal Agent chat model selection and does not create or edit a Notion page.

## Request

Use the existing API base URL and API key. The image model alias is `notion-image`; this alias selects Notion's default image generator, not a particular provider version.

```bash
curl http://127.0.0.1:8088/v1/images/generations \
  -H "Authorization: Bearer <api_key>" \
  -H "Content-Type: application/json" \
  -d '{"model":"notion-image","prompt":"A cheerful pelican riding a bicycle by the sea","n":1,"size":"auto","response_format":"b64_json"}'
```

| Parameter | Supported values |
| --- | --- |
| `prompt` | Required, 1–8192 characters |
| `model` | `notion-image`, or omitted |
| `n` | `1`, or omitted |
| `size` | `auto`, or omitted; native dimensions are returned |
| `quality` | `auto`, or omitted |
| `response_format` | `b64_json`, or omitted |
| `user` | Optional client identifier; not forwarded to Notion |

Unsupported parameters are rejected before calling Notion. Fixed dimensions, provider/model selection, URL responses, streaming and image editing are not implemented. A client that automatically sends `1024x1024` must be set to automatic size. Use the image generation endpoint rather than the Chat Completions or Responses endpoint.

## Response and account discovery

The response follows the `created` / `data[].b64_json` image generation format. Each item also includes the upstream-reported `model` and `provider`, plus `mime_type`, `width` and `height`. Save decoded image bytes using their returned MIME type; a native JPEG is not relabeled as PNG.

Only a successful generation confirms an account's image capability. The result is persisted in its local account file. After that, `/v1/models` includes one `notion-image` entry with `protocol: "image"`, `capabilities: ["image_generation"]` and the supported endpoint. Before confirmation, add the alias manually and make a generation request. The gateway does not spend image allowance during model-list refresh.

The gateway prefers an account with verified image access. Otherwise it prefers a working chat login, then an imported login whose image access is unknown. Image access is independent of legacy Workflow quota. Permission, login and limit failures apply a short image-specific cooldown; they do not disable chat. Failed generations are not automatically retried, since a connection failure may still consume upstream allowance.

Generation uses the existing Notion proxy setting and Agent timeout setting (default 300 seconds). Requests accept PNG/JPEG data returned inline by Notion, up to 20 MiB per image; an unexpected remote URL is rejected rather than fetched. Client keys are never forwarded to Notion, and upstream error bodies are not exposed.

## Live result, 2026-10-13

One imported Business trial account successfully generated a pelican bicycle illustration through the native endpoint: HTTP 200, 18.53 seconds, JPEG, 505,095 bytes, 1440 × 768. The returned fields were `model: "nano-banana"` and `provider: "google"`. These are Notion's reported labels; they do not establish a more specific Google model version or allow selecting it.

The deployed `/v1/images/generations` route also completed a real request with the same account: HTTP 200, 20.00 seconds, JPEG, 606,235 bytes, 1408 × 768, again `nano-banana` / `google`. Both images were saved and visually inspected. `go test ./...`, `go vet ./...` and the Windows build passed; request authentication and unsupported-parameter checks passed against the running service. Windows race-test executables exited with `0xc0000139`, so race detection was not completed for this change.

Workspace image permission and remaining allowance still govern each request. See Notion's [image generation help](https://www.notion.com/help/create-and-edit-images-with-notion-ai) for current availability and workspace controls.
