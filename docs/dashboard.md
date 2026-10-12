# Dashboard & Proxy

[← Back to README](../README.md)

## Auth model

Two distinct auth surfaces:

- `/v1/messages` — **API key** auth (`Authorization: Bearer <key>` or `x-api-key: <key>`), for external clients
- `/admin/*`, `/dashboard/`, `/proxy/start` — **dashboard session** auth (cookie set by `/dashboard/auth/login`)

After signing in through `/dashboard/`, the frontend uses the `dashboard_session` cookie to access:

- `/admin/accounts` (incl. `DELETE /admin/accounts/{email}`)
- `/admin/models`
- `/admin/refresh`
- `/admin/settings`
- `/admin/stats`
- `/proxy/start`

Login uses client-side SHA256(salt + password) so the plaintext password never traverses the network — see `internal/proxy/dashboard.go`.

## Account status

`/dashboard/` shows imported account details:

- Email, plan type, workspace
- Remaining quota (basic / premium balance), space + user usage, research usage
- Discovered models, last-checked timestamp
- Per-row actions: **Open proxy**, **Copy token**, **Delete account**

The account list is fetched from `GET /admin/accounts?q=&page=&page_size=` and supports search, sorting and pagination.

When `q`/`page`/`page_size` are all absent, the response keeps its historical shape (full unsorted list) so older scripts and curl pipelines stay happy.

## Token usage statistics

A dedicated panel reading `GET /admin/stats`. Shows:

- **Total** lifetime input + output tokens, total request count
- **Today** and **last 24h** rolling windows (the 24h figure linearly interpolates yesterday's bucket against the current time-of-day)
- **30-day daily series** rendered as a line chart
- **Top-5 models** and **top-5 accounts** by total tokens

Counters survive restarts via `accounts/.token_stats.json`. The flush loop runs every 5 s.

## Settings panel

Editable knobs persisted into `config.yaml`:

- `enable_web_search`, `enable_workspace_search`
- `ask_mode_default` — when ON, every request behaves as if the user toggled Notion's "Ask" (read-only). The per-request `-ask` model suffix overrides this for one call
- `debug_logging`
- `notion_proxy` — paste an `http`/`https`/`socks5`/`socks5h` URL to tunnel **all** Notion-bound traffic. Bad schemes are rejected with a 400; clearing the field reverts to direct dial. Idle pooled connections are dropped on save so the next dial picks up the new upstream

## Opening the local Notion proxy

1. Click the best account or a specific account in the dashboard
2. The browser hits `/proxy/start?best=true` or `/proxy/start?email=<email>`
3. The server creates `np_session`, then redirects to `/ai`
4. Notion HTML, API requests, assets, and realtime connections all flow through that account
5. Accounts whose Notion workspace is missing return `409` instead of redirecting (the dashboard surfaces a "no workspace" badge so the user picks another account)

The reverse proxy auto-handles:

- Injecting `full_cookie` (or the minimal cookie set when `full_cookie` isn't present)
- Forwarding `/_assets/*`, `/api/*`, `/primus-v8/*`, `/_msgproxy/*`
- Rewriting Notion frontend base URLs (`CONFIG.domainBaseUrl`, etc.)
- Stripping analytics scripts (GTM, customer.io, …)

## Account ops

- **Delete** — `DELETE /admin/accounts/{email}` removes the matching JSON file from `accounts/` and drops the live pool entry. Useful for retired accounts so they don't poison the picker
- **Refresh** — `POST /admin/refresh` runs the quota / models check across the whole pool. The endpoint returns `started: false` if a refresh is already in flight
- **Settings** — `PUT /admin/settings` is idempotent and persists to `config.yaml` via YAML node manipulation (so comments survive)
