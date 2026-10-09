# Notion Manager Enhanced：Workflow + Personal Agent V3

本实现将 Personal Agent V3 原生加入原有 Go 后端，复用账号池、Chrome TLS 传输、网络代理和三种 API 的响应转换。无需额外 Python 服务。原有 Workflow、管理面板、研究模式和会话恢复路径保留。

## 源码依据

实际克隆并阅读的提交（2026-10-09）：

| 项目 | 提交 | 用途 |
|---|---|---|
| [notion_manager](https://github.com/SleepingBag945/notion_manager/tree/3ce40ea57f304d3195d0a18205fbf2949003683a) | `3ce40ea57f304d3195d0a18205fbf2949003683a` | 主项目架构、账号、工具桥接和 API |
| [notion-agent-gateway](https://github.com/ArthurMonteiro08586/notion-agent-gateway/tree/b4cc30c14e94da41cd19433b10b8d25d409d3797) | `b4cc30c14e94da41cd19433b10b8d25d409d3797` | Agent 请求、候选模型信息 |
| [GALIAIS/Notion2API](https://github.com/GALIAIS/Notion2API/tree/d76748421c72df13710c05b23fad429b1187108e) | `d76748421c72df13710c05b23fad429b1187108e` | 协议参考 |
| [maverickxone/notion2api](https://github.com/maverickxone/notion2api/tree/5882102d019a9c8f2075fa357fe6a0c573c71af0) | `5882102d019a9c8f2075fa357fe6a0c573c71af0` | 协议参考 |

候选映射来自 `model_registry.json` 的模型名称、内部 ID 和强度字段。静态表不会赋予账号权限，不应把候选数量视为实际可用模型数量。

## 启动和配置

Windows PowerShell：

```powershell
./scripts/build.ps1
./scripts/start-local.ps1
```

需要 Go 1.26 或更新版本，以及 Node/npm。构建脚本先编译原有 React 面板，再同步 `internal/web/dist`，最后生成 `dist/notion-manager-enhanced.exe`。本次环境采用 Go 1.27.2。

本地启动脚本使用 `127.0.0.1:8088`，启用 Agent，但保留默认 Workflow 路由。配置、账号和访问凭据保存在被 Git 忽略的 `.local-runtime`，该目录的 Windows ACL 仅授予当前用户。面板地址为 `http://127.0.0.1:8088/dashboard/`，登录密码和 API Key 位于 `.local-runtime/local-access.json`。该文件含凭据，应留在本机。

普通部署仍使用原有 `config.yaml`：

```yaml
server:
  host: "127.0.0.1" # 留空可保留原有监听行为
  port: "8088"
agent_v3:
  enabled: true
  timeout_seconds: 300
  poll_interval_ms: 3000
  model_cache_seconds: 300
proxy:
  default_protocol: workflow
```

可用环境变量：`HOST`、`PORT`、`AGENT_V3_ENABLED`、`DEFAULT_PROTOCOL`、`AGENT_TIMEOUT_SECONDS`、`AGENT_POLL_INTERVAL_MS`、`AGENT_MODEL_CACHE_SECONDS`。Agent 默认关闭。超时范围 1–1800 秒，轮询范围 100–30000 毫秒，模型缓存范围 1–86400 秒。设置默认 Agent 前必须启用 Agent。

## 路由与模型身份

三个推理端点均支持显式前缀：

| 客户端模型名 | 协议 | 发送给 Notion 的 ID |
|---|---|---|
| `agent/opus-5.5`（或 `agent/opus`） | Personal Agent V3 | `albuquerque-quinn` |
| `agent/gpt-6.1-sol`（或 `agent/sol`） | Personal Agent V3 | `omniberry-sundae` |
| `agent/kimi-k3` | Personal Agent V3 | `fireworks-kimi-k3` |
| `workflow/opus-4.6` | Workflow | 原有映射 `avocado-froyo-medium` |
| `opus-4.6` | 默认 Workflow | 原有映射 |

`POST /v1/messages`、`POST /v1/chat/completions`、`POST /v1/responses` 支持 JSON 与 SSE。`GET /v1/models` 保留原有模型 ID，并提供协议、内部 ID、推理强度来源及权限字段。原有 API Key 与面板会话认证保留。

未知模型返回 404；未知协议返回 400，不做模糊型号降级。请求开始后协议被固定，后台改默认协议不会把已开始的 Workflow 请求改送到 Agent。

Agent 创建后必须检查 `thread.data.agent_provider.model`。上游选了其他模型时返回 403，不以原请求名称伪装成功；无法得到实际模型 ID 时返回 502。推理强度若被上游替换，也返回明确错误。这里验证的是 Notion 返回的运行时模型 ID，厂商名称仍采用参考项目的候选映射。

## 账号、目录与状态

Agent 使用原有 `AccountPool` 和账号 JSON 文件，新增 `agent_capability`，不创建独立 `pool.json`。Workflow 额度和 Agent 状态分开，某一协议的额度异常不会禁用另一协议。Agent 独立轮换同一账号池，跳过冷却中或确认没有工作区的账号。

`getAvailableModels` 会按账号缓存，并可通过面板刷新失效。它未证明是 Personal Agent 专用授权接口，因此：

- `unknown`：仅有候选映射，或目录没有列出。不会显示为已确认可用。
- `catalog_enabled`：API 目录已列出，尚未完成实际推理验证。
- `confirmed`：实际返回 ID 一致，且推理完成。
- `denied`：API 明确禁用、权限拒绝或实际返回了其他模型。

API 目录中没有出现候选模型，不直接等同于拒绝。客户端明确选择一个未被禁用的候选模型时，可通过正常的创建请求验证；Notion 后端的认证、套餐和模型选择限制继续生效。没有调整上游策略或跳过审批的请求。

管理面板沿用原有设计，增加启用开关、默认协议、超时、轮询间隔、失败统计，以及每账号的实际返回模型。`available=true` 仅用于有效缓存内实际推理确认的 Agent 模型；刷新后需重新确认。

## Transcript 与会话模式

流程是 `createAgentThread` → 轮询 `getThreadTranscript`。真实账号验证补充了以下解析规则：

- `committed` 是序号确认，可忽略其文本输出。
- `put` 中的 `assistant_message` 提供文本；按实体 ID 覆盖修订并去重。
- `remove/delete` 删除相应实体。
- `source=committed` 的 `turn_completed` 且 `stop_reason=end_turn` 是真实完成标记。
- `idle`、文本暂时不变或空轮询，单独都不是完成证据。
- 未知补丁、无效 JSON、响应过大或存在未支持的分页会返回明确错误。

对于可修改的累计文本，以 UTF-8 安全的公共前缀检测修订，并缓冲到确定完成后再发 SSE 文本块。模式公开标记为 `polling-buffered`，不是上游 Token Streaming。该选择避免客户端收到后来无法撤销的错误文本。请求取消和超时会停止 HTTP 请求、轮询及本地输出，不宣称已取消 Notion 上游任务。

Agent 每次创建新线程，将完整客户端历史及工具 ID 重放，标记为 `history-replay`。尚未实现原生线程续聊。真实返回中出现了 `sendEventToAgentThread` 的诊断链接，但没有得到足够的请求契约证据，不据此猜测实现。Agent 不复用 Workflow Session 的 Thread ID。

返回头：

```text
X-Notion-Protocol: agent
X-Notion-Session-Mode: history-replay
X-Notion-Streaming-Mode: polling-buffered
X-Notion-Usage: unavailable
```

没有验证到上游 Token 计数；不将估算值写入实际 Token 统计。兼容 API 中的零值用量字段是占位，不能解释为已知的零消耗。

## 工具桥接与限制

Agent 使用现有格式注入工具桥接，严格检查模型生成的工具名和原始对象 JSON 参数；不能继承旧解析器为缺失参数补 `{}` 的行为。保留 `tool_use/tool_result` ID、多个连续调用和完整历史；标准工具调用不会被代替执行。`tool_choice=none/any/指定工具` 会校验实际结果。

普通模型文本可以真实返回 `end_turn`。请求强制工具调用时，没有合法调用则返回 502；不编造工具调用。已开始输出的响应不会切换账号重放，也不会把错误收尾为成功的完成事件。

目前显式拒绝 Agent 附件、ASK 模式、研究模式、工作区搜索覆盖、采样控制、停止序列、thinking 块、context_management 和强制 JSON Schema。`output_config.effort` 使用模型候选/API 支持的强度，并核对实际返回。`max_tokens` 等兼容字段没有经过验证的上游硬限制；不能依赖它们控制 Agent 的实际生成量。

审批策略保留 `review_for_me`，网关不调用任何批准接口，并关闭建议编辑工具。观察到审批等待时返回 409，需要用户在 Notion 内处理。

## 真实测试结果（2026-10-09，单个测试账号）

第一轮目录返回 `models=[]`、`modelSelectionRestricted=true`。逐个显式请求的结果如下，三次均完成并返回固定测试文本：

| 请求 | 实际返回 ID | 返回的选择方式 | 身份一致 |
|---|---|---|---|
| Opus 5.5 / `albuquerque-quinn` | `omniberry-sundae` | `auto` | 否 |
| GPT-6.1 Sol / `omniberry-sundae` | `omniberry-sundae` | `auto` | 是 |
| Kimi K3 / `fireworks-kimi-k3` | `omniberry-sundae` | `auto` | 否 |

用户随后在 Notion 中仅开放 Opus 5.5。再次请求 `albuquerque-quinn`，实际返回 **`albuquerque-quinn`**，强度 `medium`、选择方式仍为 `auto`，推理返回 `AGENT_OK`。这说明模型选择设置会影响上游实际运行模型；不能只凭请求里的 slug 判定调用了哪个模型，也不能把第一次的结果当成所有账号的永久结论。

本机详细报告在 Git 忽略的 `.local-runtime/live-model-identities.json`。公开文档仅保留模型、策略和测试结果，不包含账号标识或凭据。

三种 API 的 JSON，以及 Messages、Chat Completions 的 SSE 已在该账号成功返回 `API_OK`。最近一轮 Responses SSE 和工具请求被上游拒绝；随后单独复查 Opus 推理成功。这两项尚无真实成功复测证据，不能据此宣称全部真实 API/工具链已通过。发布测试记录见 [RELEASE_NOTES.md](../RELEASE_NOTES.md)。

## 验证方法

```powershell
go test ./... -count=1 -timeout 90s
go vet ./...
go test -race ./... -count=1 -timeout 120s
npm run build --prefix web
./scripts/test-live.ps1 -Model agent/opus-5.5
```

离线测试覆盖：三种 API 的 JSON/SSE、认证、模型替换拒绝、实际完成标记、文本修订及 Unicode、稳定 idle 不能完成、取消和超时、错误分类、模型缓存失效、强度校验、协议额度隔离、轮换、多工具调用及历史 ID、审批和分页拒绝、账号并发保存、设置持久化和凭据脱敏。

实时 Go 测试需显式设置 `NOTION_LIVE_ACCOUNT_FILE`；默认跳过。模型身份矩阵还需 `NOTION_LIVE_MODEL_MATRIX=1`，可设置 `NOTION_LIVE_REPORT_FILE` 输出脱敏报告。测试仅创建合成消息，不批准操作。Windows 的 race detector 需要足够新的 MinGW-w64 运行库；本次使用校验过 SHA-256 的 w64devkit 2.10.0。POSIX 0600 位的测试在 Windows 跳过，不能将其视为 Windows ACL 已验证。
