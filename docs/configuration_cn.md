# 配置说明

[← 返回 README](../README_CN.md)

## 配置优先级

```text
环境变量 > config.yaml > 代码默认值
```

## Server

| 配置项 | 仓库样例 | 代码默认 | 说明 |
| --- | --- | --- | --- |
| `server.port` | `3000` | `8081` | 监听端口 |
| `server.accounts_dir` | `accounts` | `accounts` | 已有账号 JSON 文件目录 |
| `server.token_file` | `token.txt` | `token.txt` | 单账号回退文件 |
| `server.api_key` | 空 | 自动生成 | `/v1/messages` 鉴权 |
| `server.admin_password` | 空 | 自动生成 | 首次启动后哈希回写 |
| `server.log_file` | `server.stderr.log` | 空（stderr） | `log.Printf` 全部追加到该文件 |
| `server.debug_logging` | `true` | `true` | 高频调试日志 |
| `server.api_log_input` | `true` | `false` | 记录入站 `/v1/messages` 请求体 |
| `server.api_log_output` | `true` | `false` | 记录返回给 API 客户端的响应 |
| `server.notion_log_request` | `true` | `false` | 记录发送给 Notion 的 `runInferenceTranscript` 请求体 |
| `server.notion_log_response` | `true` | `false` | 记录 Notion 原始推理响应 |
| `server.dump_api_input` | `false` | `false` | 每个请求把系统提示词 + 工具 schema 落盘（用于 Claude Code 调试） |

## Proxy / 运行行为

| 配置项 | 仓库样例 | 代码默认 | 说明 |
| --- | --- | --- | --- |
| `proxy.notion_api_base` | `https://www.notion.so/api/v3` | 同左 | Notion API 入口 |
| `proxy.client_version` | `23.13.20260313.1423` | 同左 | `x-notion-client-version` 请求头 |
| `proxy.default_model` | `opus-4.6` | `opus-4.6` | 请求未传 `model` 时的回退 |
| `proxy.disable_notion_prompt` | `true` | `false` | 关闭 Notion 内置 ~33k 系统提示，节省输入 token |
| `proxy.enable_web_search` | `true` | `true` | 联网搜索全局开关（可被请求头覆盖） |
| `proxy.enable_workspace_search` | `false` | `false` | 工作区搜索全局开关（可被请求头覆盖） |
| `proxy.ask_mode_default` | 未设置 | `false` | 为 `true` 时所有对话默认走 `useReadOnlyMode=true`（即 Notion 前端的 “Ask” 开关）；请求级 `-ask` 后缀仍可覆盖 |
| `proxy.notion_proxy` | 空 | 空 | 上游代理（`http`/`https`/`socks5`/`socks5h`），作用于**全部** Notion 出站连接，包括 `/ai` 和会话请求 |

## Timeouts / Refresh

| 配置项 | 仓库样例 | 代码默认 | 说明 |
| --- | --- | --- | --- |
| `timeouts.inference_timeout` | `300` | `300` | 普通推理超时（秒） |
| `timeouts.research_timeout` | 未设置 | `360` | 研究模式超时（秒），作用于 `researcher` / `fast-researcher` |
| `timeouts.api_timeout` | `30` | `30` | 配额 / 模型 REST 超时（秒） |
| `timeouts.tls_dial_timeout` | `30` | `30` | TLS 拨号超时（秒） |
| `refresh.interval_minutes` | `30` | `30` | 后台刷新间隔（分钟） |
| `refresh.quota_recheck_minutes` | `30` | `30` | 已耗尽账号的重新检查间隔 |
| `refresh.concurrency` | `10` | `10` | 后台刷新并发数 |
| `refresh.live_check_seconds` | 未设置 | `5` | 同一账号请求级实时额度校验的最小间隔；`0` = 每次请求都查 |

## 环境变量

`server.*` / `proxy.*` / `timeouts.*` / `refresh.*` / `browser.*` 都有对应大写下划线命名的环境变量。常用：

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

`NOTION_PROXY` 写错（不支持的 scheme 等）时，启动会打印一次日志并清空，避免错误的 URL 渗透到每个 Notion 请求里。

## 端点

| 路径 | 说明 | 鉴权 |
| --- | --- | --- |
| `GET /health` | 本地服务健康检查 | 无 |
| `POST /v1/messages` | Anthropic Messages API | API Key |
| `GET /dashboard/` | 管理面板 | Dashboard 登录 |
| `GET /proxy/start` | 创建账号代理会话 | Dashboard 登录 |
| `GET /ai` | Notion Web 代理入口 | `np_session` |
| `GET /admin/accounts` | 账号列表（支持 `q` / `page` / `page_size`） | Dashboard 会话 |
| `DELETE /admin/accounts/{email}` | 删除账号 JSON + 池中条目 | Dashboard 会话 |
| `GET /admin/models` | 模型映射 + 池内可用模型 | Dashboard 会话 |
| `GET/POST /admin/refresh` | 查询 / 触发刷新 | Dashboard 会话 |
| `GET/PUT /admin/settings` | 读 / 写运行时设置（`enable_web_search` / `enable_workspace_search` / `ask_mode_default` / `debug_logging` / `notion_proxy`） | Dashboard 会话 |
| `GET /admin/stats` | Token 用量统计（累计 + 今日 + 24h + 30 天序列 + Top‑N） | Dashboard 会话 |

## 项目结构

```text
notion-manager/
├── cmd/notion-manager/        # 服务入口
├── internal/proxy/            # 账号池、API handler、反向代理、上传、配置、统计
├── internal/netutil/          # 代理拨号工具
├── internal/web/dist/         # 嵌入式 Dashboard 静态资源
├── web/                       # Dashboard 前端源码（React + Vite）
├── chrome-extension/          # 提取 Notion 会话的 Chrome 扩展
├── accounts/                  # 已有账号 JSON 文件与运行状态
├── docs/                      # 中英文档
├── example.config.yaml
├── README.md
└── README_CN.md
```

## 使用建议与限制

- `admin_password` 留空会自动生成随机密码并打印到控制台，哈希后写回 `config.yaml`。**首次启动一定要保存控制台显示的明文密码**，事后无法恢复
- 浏览器代理使用已有会话信息，优先读取账号文件中的 `full_cookie`
- 免费账号一旦额度耗尽可能长期不可用，付费账号更适合稳定池
- 修改 `web/` 前端源码后需要在 `web/` 目录执行 `npm run build`，并把产物同步到 `internal/web/dist/`，否则运行时仍走旧资源
- `accounts/.register_inputs/<job_id>.json` 里有明文凭据，**不要**纳入版本控制；`.gitignore` 已排除该目录
