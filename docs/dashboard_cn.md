# Dashboard 与代理

[← 返回 README](../README_CN.md)

## 鉴权模型

两套独立的鉴权：

- `/v1/messages` —— **API Key** 鉴权（`Authorization: Bearer <key>` 或 `x-api-key: <key>`），面向外部客户端
- `/admin/*`、`/dashboard/`、`/proxy/start` —— **Dashboard 会话**鉴权（`/dashboard/auth/login` 写入 cookie），仅供 Dashboard 前端使用

通过 `/dashboard/` 登录后，前端凭 `dashboard_session` cookie 访问：

- `/admin/accounts`（含 `DELETE /admin/accounts/{email}`）
- `/admin/models`
- `/admin/refresh`
- `/admin/settings`
- `/admin/stats`
- `/proxy/start`

登录密码使用客户端 SHA256(salt + password)，明文密码永远不离开浏览器，详见 `internal/proxy/dashboard.go`。

## 账号状态

`/dashboard/` 显示已导入账号的状态：

- 邮箱、计划类型、workspace
- 剩余额度（basic / premium 余额）、space / user 用量、研究用量
- 已发现模型、最近一次检查时间
- 单行操作：**打开代理**、**复制 token**、**删除账号**

列表来源于 `GET /admin/accounts?q=&page=&page_size=`，支持搜索、排序与分页。

当 `q`/`page`/`page_size` 都缺省时，响应保持**历史完整列表形态**，老脚本 / curl 流水线照旧能用。

## Token 用量统计

读取 `GET /admin/stats`，独立面板展示：

- **累计** 输入 + 输出 token、累计请求数
- **今日** 与 **最近 24 小时** 滚动窗口（24h 数值会按当前时刻按比例线性融合昨日 bucket）
- **近 30 天** 日序列，折线图
- **Top‑5 模型** 与 **Top‑5 账号**（按总 token 排序）

数据落盘在 `accounts/.token_stats.json`，每 5 秒写入一次，重启不丢。

## 设置面板

可在线编辑、写回 `config.yaml`：

- `enable_web_search`、`enable_workspace_search`
- `ask_mode_default` —— 打开后所有请求等同于 Notion 前端的 “Ask”（read-only）模式；请求级 `-ask` 后缀仍可对单次请求覆盖
- `debug_logging`
- `notion_proxy` —— 填入 `http`/`https`/`socks5`/`socks5h` URL，会代理**所有**指向 Notion 的连接。错误 scheme 会立即 400；清空回退到直连。保存时会丢弃空闲连接，下一次拨号即采用新上游

## 打开 Notion Web 代理

1. 在 Dashboard 中点击“最佳账号”或任一具体账号
2. 浏览器访问 `/proxy/start?best=true` 或 `/proxy/start?email=<邮箱>`
3. 服务端创建 `np_session`，重定向到 `/ai`
4. 后续 HTML、API、资源、WebSocket 都通过当前账号代理
5. 没有 workspace 的账号直接返回 `409`，避免把用户带进无限骨架屏；Dashboard 会提示“无 workspace”

反向代理自动处理：

- 注入 `full_cookie`（或最小必需 cookie）
- 转发 `/_assets/*`、`/api/*`、`/primus-v8/*`、`/_msgproxy/*`
- 改写 Notion 前端的基础地址（`CONFIG.domainBaseUrl` 等）
- 过滤 GTM、customer.io 等分析脚本

## 账号运维

- **删除** —— `DELETE /admin/accounts/{email}` 同时移除 `accounts/` 下的 JSON 文件和池内对象。退役账号不再污染选择器
- **刷新** —— `POST /admin/refresh` 触发整池配额 / 模型检查；如果已经有刷新在跑，端点返回 `started: false`
- **设置** —— `PUT /admin/settings` 是幂等的，并通过 YAML 节点修改持久化到 `config.yaml`，不会破坏注释
