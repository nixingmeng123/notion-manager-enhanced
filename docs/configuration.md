# Configuration

[← Back to README](../README.md)

## Priority

```text
environment variables > config.yaml > code defaults
```

## Server

| Setting | Repo sample | Code default | Notes |
| --- | --- | --- | --- |
| `server.port` | `3000` | `8081` | Listening port |
| `server.accounts_dir` | `accounts` | `accounts` | Directory for existing account JSON files |
| `server.token_file` | `token.txt` | `token.txt` | Fallback single-account file |
| `server.api_key` | empty | auto-generated | Used by `/v1/messages` |
| `server.admin_password` | empty | auto-generated | Hashed on first startup |
| `server.log_file` | `server.stderr.log` | empty (stderr) | All `log.Printf` output is appended here |
| `server.debug_logging` | `true` | `true` | High-volume debug logs |
| `server.api_log_input` | `true` | `false` | Log incoming `/v1/messages` request bodies |
| `server.api_log_output` | `true` | `false` | Log responses returned to API clients |
| `server.notion_log_request` | `true` | `false` | Log `runInferenceTranscript` request bodies |
| `server.notion_log_response` | `true` | `false` | Log raw inference responses |
| `server.dump_api_input` | `false` | `false` | Dump system prompts + tool schemas to disk per request (Claude Code debugging) |

## Proxy / runtime behaviour

| Setting | Repo sample | Code default | Notes |
| --- | --- | --- | --- |
| `proxy.notion_api_base` | `https://www.notion.so/api/v3` | same | Notion API base URL |
| `proxy.client_version` | `23.13.20260313.1423` | same | `x-notion-client-version` header |
| `proxy.default_model` | `opus-4.6` | `opus-4.6` | Fallback model when request omits one |
| `proxy.disable_notion_prompt` | `true` | `false` | Removes Notion's ~33k system prompt for leaner API usage |
| `proxy.enable_web_search` | `true` | `true` | Global web-search toggle (overridable per-request) |
| `proxy.enable_workspace_search` | `false` | `false` | Global workspace-search toggle (overridable per-request) |
| `proxy.ask_mode_default` | unset | `false` | When `true`, every chat runs with `useReadOnlyMode=true` (Notion's "Ask" toggle) — the per-request `-ask` suffix overrides this |
| `proxy.notion_proxy` | empty | empty | Upstream proxy (`http`/`https`/`socks5`/`socks5h`) used for **all** Notion-bound dials, including `/ai` and conversation requests |

## Timeouts / refresh

| Setting | Repo sample | Code default | Notes |
| --- | --- | --- | --- |
| `timeouts.inference_timeout` | `300` | `300` | Standard inference timeout (s) |
| `timeouts.research_timeout` | unset | `360` | Research-mode timeout (s) — applies to `researcher` / `fast-researcher` |
| `timeouts.api_timeout` | `30` | `30` | Quota / models REST timeout (s) |
| `timeouts.tls_dial_timeout` | `30` | `30` | TLS dial timeout (s) |
| `refresh.interval_minutes` | `30` | `30` | Background refresh interval |
| `refresh.quota_recheck_minutes` | `30` | `30` | Min interval between rechecks of an exhausted account |
| `refresh.concurrency` | `10` | `10` | Concurrent refresh workers |
| `refresh.live_check_seconds` | unset | `5` | Min interval between live per-request quota checks for the same account; `0` = check on every request |

## Environment variables

Every `server.*`, `proxy.*`, `timeouts.*`, `refresh.*`, `browser.*` field above has an equivalent env var (uppercased, dotted segments joined by `_`). The most useful ones:

```bash
export PORT=3000
export API_KEY=sk-your-api-key
export ENABLE_WEB_SEARCH=true
export ENABLE_WORKSPACE_SEARCH=false
export ASK_MODE_DEFAULT=false
export NOTION_PROXY=socks5h://127.0.0.1:1080
export QUOTA_LIVE_CHECK_SECONDS=5
export REFRESH_CONCURRENCY=10
```

An invalid `NOTION_PROXY` is logged once at startup and dropped — runtime falls back to direct dial rather than leaking the misconfigured URL into every Notion request.

## Endpoints

| Path | Purpose | Auth |
| --- | --- | --- |
| `GET /health` | Local service health check | None |
| `POST /v1/messages` | Anthropic Messages API | API key |
| `GET /dashboard/` | Dashboard UI | Dashboard login |
| `GET /proxy/start` | Create a targeted proxy session | Dashboard login |
| `GET /ai` | Local Notion Web proxy entry | `np_session` |
| `GET /admin/accounts` | Pool list (supports `q`, `page`, `page_size`) | Dashboard session |
| `DELETE /admin/accounts/{email}` | Remove account file + pool entry | Dashboard session |
| `GET /admin/models` | Model mapping + pool-discovered models | Dashboard session |
| `GET/POST /admin/refresh` | Refresh status / trigger refresh | Dashboard session |
| `GET/PUT /admin/settings` | Read / update runtime settings (`enable_web_search`, `enable_workspace_search`, `ask_mode_default`, `debug_logging`, `notion_proxy`) | Dashboard session |
| `GET /admin/stats` | Token usage statistics (lifetime + today + 24h + 30-day series + top-N) | Dashboard session |

## Project layout

```text
notion-manager/
├── cmd/notion-manager/        # Server entrypoint
├── internal/proxy/            # Account pool, API handler, reverse proxy, uploads, config, stats
├── internal/netutil/          # Proxy dialer helpers
├── internal/web/dist/         # Embedded Dashboard static assets
├── web/                       # Dashboard frontend source (React + Vite)
├── chrome-extension/          # Chrome extension that extracts Notion sessions
├── accounts/                  # Existing account JSON files and runtime state
├── docs/                      # English + Chinese documentation
├── example.config.yaml
├── README.md
└── README_CN.md
```

## Notes

- `admin_password` left empty auto-generates a random password printed to the console, then hashes and writes it back. Save the plaintext shown on first startup — it cannot be recovered.
- The browser proxy uses existing session information and prefers `full_cookie` when present.
- Free accounts can stay exhausted indefinitely; paid accounts make a more stable pool.
- If you change the dashboard source under `web/`, run `npm run build` inside `web/` and copy the output into `internal/web/dist/`.
- `accounts/.register_inputs/<job_id>.json` contains plaintext credentials and **must not be checked into version control** — `.gitignore` already excludes the directory.
