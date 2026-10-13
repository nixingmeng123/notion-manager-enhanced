# Notion Manager Enhanced

English | [简体中文](README_CN.md)

A local Go client for Notion Personal Agent V3. Connect your own signed-in Notion account to inspect runtime model identity, handle conversation history and streamed responses, and manage local settings through a dashboard.

Built on [notion_manager](https://github.com/SleepingBag945/notion_manager), with native Personal Agent V3 support and retained Workflow compatibility. Model access depends on your Notion account permissions and the model actually returned by the upstream.

## Windows quick start

1. Download and extract the Windows archive from [Releases](https://github.com/nixingmeng123/notion-manager-enhanced/releases).
2. Run from the extracted directory:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\start-local.ps1
```

3. Open `http://127.0.0.1:8088/dashboard/`.
4. Sign in with the generated dashboard password. The password and API key are saved locally in `.local-runtime/local-access.json`.
5. Import your existing signed-in Notion session through **Add account**.
6. Select a verified `agent/` model returned by `/v1/models` for Personal Agent V3 requests.

The startup script uses a 15-second request start interval per account. After upgrading an existing configuration, set the interval to 15 seconds in the dashboard; the startup script does not overwrite an existing configuration.

## Features

- **Existing account access and local management**: import an account, view its workspace and model status, and sign in with a separate dashboard password.
- **Protocol routing**: `agent/` selects Personal Agent V3; `workflow/` selects Workflow. Unprefixed names use Workflow by default.
- **Runtime model verification**: check the model ID returned by Notion and return an error if the upstream selects a different model.
- **Client model discovery**: return account catalog Workflow models and identity-verified Agent models; omit static candidates and duplicate names.
- **Conversation and response handling**: full history replay, JSON responses, polling with buffered SSE, and conversion of tool calls and tool-result history.
- **Request controls**: timeout, cancellation and per-account request spacing. Explicit reasoning effort is checked against the upstream response.
- **Web search**: Agent uses Notion's native browsing tools when allowed by the dashboard setting or `X-Web-Search` header. Tool results establish whether a search actually occurred.
- **Image input**: Messages image blocks, Chat Completions `image_url`, and Responses `input_image` are uploaded through Agent Service and attached by file ID. Opus 5.5 image recognition has been verified.
- **Image generation**: native Notion image generation through `/v1/images/generations`, with inline image bytes and the actual returned provider/model. See [supported parameters](docs/IMAGE_GENERATION.md).
- **Usage display**: personal AI monthly usage and reset dates are separate from member credit spending caps, shared workspace Credits and historical free usage. Shared balances are counted once per workspace.
- **Dashboard settings**: Agent enablement, default protocol, timeout, polling interval and request interval.

## Client requests

| Path | Purpose |
| --- | --- |
| `POST /v1/messages` | Messages request format |
| `POST /v1/chat/completions` | Chat Completions request format |
| `POST /v1/responses` | Responses request format |
| `POST /v1/images/generations` | Native image generation (`notion-image`) |
| `GET /v1/models` | Model status list |

Authenticate with `Authorization: Bearer <api_key>` or `x-api-key: <api_key>`.

```json
{
  "model": "agent/opus-5.5",
  "messages": [{"role": "user", "content": "Hello"}]
}
```

Opus 5.5 previously returned the Notion model ID `albuquerque-quinn` when enabled for the test workspace. The same workspace's later Business trial tests returned `omniberry-sundae` automatically, so currently only `agent/gpt-6.1-sol` is identity-verified for chat there. Always use your own account's verified model list. Static mappings do not establish permission or successful inference.

For image generation, use `notion-image` at `/v1/images/generations`, with `n: 1`, `size: "auto"` and `response_format: "b64_json"`. On 2026-10-13, the native generator reported `nano-banana` / `google`. The alias appears in model discovery after a successful generation; the upstream selects its provider/model automatically. See [the image generation example](docs/IMAGE_GENERATION.md).

## Run from source

Go 1.26 or later is required. Node.js/npm is also required when changing the frontend.

```bash
git clone https://github.com/nixingmeng123/notion-manager-enhanced.git
cd notion-manager-enhanced
go run ./cmd/notion-manager
```

The Go entry point uses the port in `config.yaml`, defaulting to `8081`. The Windows local startup script uses `8088`. Dashboard resources are embedded. See `scripts/build.ps1` when rebuilding.

## Validation and limitations

v0.1.1 passed Go tests, race detection, static analysis and the frontend build. With the test account restricted to Opus 5.5, all 8 live probes passed: JSON/SSE for Messages, Chat Completions and Responses, plus a synthetic tool call and tool-result history.

Agent uses full history replay rather than native thread continuation, and SSE is buffered after polling. Upstream token usage is unknown. Tool format conversion does not execute client tools. Unsupported capabilities return explicit errors.

Intermittent upstream HTTP 400 errors remain possible. A 15-second interval mitigated the observed errors for this account; it is not a published Notion limit. See [release notes](RELEASE_NOTES.md) and [Agent V3 documentation](docs/AGENT_V3.md) for evidence and limitations.

## Documentation

- [Agent V3](docs/AGENT_V3.md): mappings, authorization, reasoning effort and live results.
- [Account access](docs/accounts.md): import an existing Notion session.
- [Image generation](docs/IMAGE_GENERATION.md): request format, automatic model selection and live results.
- [Dashboard](docs/dashboard.md): authentication, account status and settings.
- [Configuration](docs/configuration.md): runtime configuration and environment variables.
- [Client requests](docs/api.md): request formats and Workflow compatibility; the Agent V3 documentation defines Agent capability limits.

## License

Retains the upstream [CC BY-NC-SA 4.0](LICENSE) license. Non-commercial use only.
