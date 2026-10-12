# Notion Manager Enhanced：Workflow + Personal Agent V3

本实现将 Personal Agent V3 原生加入 Go 后端，使用已有账号存储、Chrome TLS 传输和响应格式转换。无需额外 Python 服务。保留 Workflow 协议兼容和本地管理面板。

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
  min_request_interval_seconds: 15
proxy:
  default_protocol: workflow
```

可用环境变量：`HOST`、`PORT`、`AGENT_V3_ENABLED`、`DEFAULT_PROTOCOL`、`AGENT_TIMEOUT_SECONDS`、`AGENT_POLL_INTERVAL_MS`、`AGENT_MODEL_CACHE_SECONDS`、`AGENT_MIN_REQUEST_INTERVAL_SECONDS`。Agent 默认关闭。超时范围 1–1800 秒，轮询范围 100–30000 毫秒，模型缓存范围 1–86400 秒，账号起始间隔范围 0–300 秒。设置默认 Agent 前必须启用 Agent。

原始默认配置的起始间隔为 0；示例配置及本地启动脚本使用 15 秒。网关按同一账号的上次创建请求时间等待，其他账号和 Workflow 不受这个等待影响。取消的等待不会占用未来时段，上次起始时间保存在既有账号 JSON 中。此设置是实测的缓解策略，不代表官方限额，也不替代上游额度、认证或审批检查。

## 路由与模型身份

三个推理端点均支持显式前缀：

| 客户端模型名 | 协议 | 发送给 Notion 的 ID |
|---|---|---|
| `agent/opus-5.5`（或 `agent/opus`） | Personal Agent V3 | `albuquerque-quinn` |
| `agent/gpt-6.1-sol`（或 `agent/sol`） | Personal Agent V3 | `omniberry-sundae` |
| `agent/kimi-k3` | Personal Agent V3 | `fireworks-kimi-k3` |
| `workflow/opus-4.6` | Workflow | 原有映射 `avocado-froyo-medium` |
| `opus-4.6` | 默认 Workflow | 原有映射 |

`POST /v1/messages`、`POST /v1/chat/completions`、`POST /v1/responses` 支持 JSON 与 SSE。`GET /v1/models` 只返回账号实际目录中的 Workflow 模型，以及完成推理且实际 ID 一致的 Agent 模型，不再用静态默认表补充模型。每个 Workflow 模型只列出一个名称；默认协议为 Agent 时使用 `workflow/` 前缀，避免错误路由。Agent 保留 `agent/` 前缀。原有 API Key 与面板会话认证保留。

未知模型返回 404；未知协议返回 400，不做模糊型号降级。请求开始后协议被固定，后台改默认协议不会把已开始的 Workflow 请求改送到 Agent。

Agent 创建后必须检查 `thread.data.agent_provider.model`。上游选了其他模型时返回 403，不以原请求名称伪装成功；无法得到实际模型 ID 时返回 502。明确指定的推理强度若被替换，也返回明确错误；未指定时不强制默认 medium，记录上游实际强度。这里验证的是 Notion 返回的运行时模型 ID，厂商名称仍采用参考项目的候选映射。

## 账号、目录与状态

Agent 使用原有 `AccountPool` 和账号 JSON 文件，新增 `agent_capability`，不创建独立 `pool.json`。Workflow 额度和 Agent 状态分开，某一协议的额度异常不会禁用另一协议。Agent 独立轮换同一账号池，跳过冷却中或确认没有工作区的账号。

`getAvailableModels` 会按账号缓存，并可通过面板刷新失效。它未证明是 Personal Agent 专用授权接口，因此：

- `unknown`：仅有候选映射，或目录没有列出。不会显示为已确认可用。
- `catalog_enabled`：API 目录已列出，尚未完成实际推理验证。
- `confirmed`：实际返回 ID 一致，且推理完成。
- `denied`：API 明确禁用、权限拒绝或实际返回了其他模型。

API 目录中没有出现候选模型，不直接等同于拒绝。客户端明确选择一个未被禁用的候选模型时，可通过正常的创建请求验证；Notion 后端的认证、套餐和模型选择限制继续生效。没有调整上游策略或跳过审批的请求。

管理面板的诊断列表保留候选映射和缓存状态。客户端模型列表不会返回未确认的候选、明确被拒绝的模型或处于冷却期的账号模型。它保留此前完成且 ID 一致的确认记录，避免目录缓存过期导致已确认模型从客户端消失；每次调用仍核对 Notion 权限和实际模型 ID。空或受限目录不撤销此前的实测记录，目录明确拒绝会撤销确认。

首次导入时如果客户端目录为空，可手动添加该账号已开放的模型名并完成一次调用，再重新拉取模型列表。静态候选只用于请求解析，不是账号授权或可用性证据。

## Transcript 与会话模式

流程是 `createAgentThread` → 轮询 `getThreadTranscript`。真实账号验证补充了以下解析规则：

- `committed` 是序号确认，可忽略其文本输出。
- `put` 中的 `assistant_message` 提供文本；按实体 ID 覆盖修订并去重。
- `patch` 的 `ops` 使用 [RFC 6902 JSON Patch](https://www.rfc-editor.org/rfc/rfc6902) 更新已知实体，包括原生工具结果和助手内容修订。工具结果和 thinking 不作为助手正文输出；补丁不会单独标记推理完成。缺少目标、非法路径或未知操作明确报错。
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

普通文本请求会说明客户端无法打开 Notion 的 `session-file://` 附件，要求代码交付包含完整源代码，避免只有内部附件链接。带客户端工具定义的请求继续使用既有 JSON 工具桥接格式。

## 联网搜索

Agent 的 `browserEnabled` 来自管理面板的 `enable_web_search` 设置，也可通过请求头 `X-Web-Search: true/false` 覆盖。它独立于 Workflow 的 `disable_notion_prompt` 设置。启用只表示允许上游使用浏览器或网页检索工具，实际是否检索以 transcript 中的工具结果为准。

2026-10-12 复现 Cherry 请求时，实际观察到原生工具结果通过 `patch` 写入实体；天气请求的工具结果包含公开天气来源链接。此前网关在解析该补丁时返回 502，并不能据此认定账号不支持联网。补充解析后，原样多轮 SVG 请求和天气检索请求均返回 HTTP 200 及完整的 `response.completed` 结束事件。

最终本地程序验证：`/v1/models` 只返回已确认的 `agent/opus-5.5`；原样多轮 SVG 请求返回包含 SVG 的完整代码，约 123 秒完成。通过正在运行的本地 `/v1/responses` 实测“联网查询上海天气并生成 HTML 卡片”，约 30 秒返回 HTTP 200，包含完整 HTML、来源链接和结束事件。未创建或修改 Notion 页面。全量 `go test -race ./...` 与 `go vet ./...` 通过。

## 工具桥接与限制

Agent 使用现有格式注入工具桥接，严格检查模型生成的工具名和原始对象 JSON 参数；不能继承旧解析器为缺失参数补 `{}` 的行为。保留 `tool_use/tool_result` ID、多个连续调用和完整历史；标准工具调用不会被代替执行。`tool_choice=none/any/指定工具` 会校验实际结果。

普通模型文本可以真实返回 `end_turn`。请求强制工具调用时，没有合法调用则返回 502；不编造工具调用。已开始输出的响应不会切换账号重放，也不会把错误收尾为成功的完成事件。

目前显式拒绝 Agent 文档附件、ASK 模式、研究模式、工作区搜索覆盖、采样控制、停止序列、thinking 块、context_management 和强制 JSON Schema。用户图片输入已接入，见下节。`output_config.effort` 使用模型候选/API 支持的强度，并核对实际返回。`max_tokens` 等兼容字段没有经过验证的上游硬限制；不能依赖它们控制 Agent 的实际生成量。

## 图片输入（2026-10-13）

Agent Service 的图片流程与 Workflow 附件不同。根据 Notion 公开前端的消息构建和上传代码，先调用 `createAgentServiceFileUploadURL`，使用 `target: {"type":"user"}` 暂存文件；向返回的签名地址上传原始图片，再调用 `completeAgentServiceFileUpload`。创建 Agent 线程时，文本块之后附加 `{"type":"file","file_id":"已完成上传的文件 ID"}`，不使用 Workflow 的 `attachment:` URI 或 `processAgentAttachment` 任务。

三个客户端接口共用此流程：Messages 接收 `image` 与 `source`，Chat Completions 接收 `image_url`，Responses 接收 `input_image`。支持 base64/data URI 和公开 HTTPS 图片地址。网关限制为每张 10 MiB、整次请求 20 MiB、最多 19 张图片，接受 PNG、JPEG、GIF、WebP；这些是网关限制，不是 Notion 公布的配额。远程图片不携带账号 Cookie 或 API Key，并拒绝 URL 凭据、非 HTTPS、私网地址和指向私网的重定向。历史中的图片位置使用文件名标记保留，包括只发送图片的用户消息；每次完整历史重放会重新上传其图片。

2026-10-13 使用仅开放 Opus 5.5 的账号测试一张合成图，提示语没有透露颜色和形状。Messages JSON、Chat Completions JSON、Responses SSE 三项均返回 HTTP 200，正确识别从左到右的红色圆形、蓝色正方形、绿色三角形，实际模型均为 `albuquerque-quinn`。Responses 收到完整结束事件。本轮实测图片格式为 PNG；其他允许格式没有逐一进行上游实测。未修改 Notion 页面。

替换本地可执行文件并重启后，再通过正在运行的 `http://127.0.0.1:8088/v1/responses` 发送同一张图片，约 7.59 秒返回 HTTP 200 和完整 SSE 结束事件，回答 `Red circle, blue square, green triangle`。本机脱敏结果保存在 Git 忽略的 `.local-runtime/vision-live-report.json`。

Cherry Studio 的视觉图标和是否实际上传图片是两件事。本机 2.0.9 的 OpenAI 兼容模型拉取只转换模型 ID、名称和归属，能力由客户端模型注册表/本地设置识别；它不会直接使用网关模型列表的额外能力字段。自定义 `agent/opus-5.5` 没有图标不能证明 Notion 模型缺少视觉能力；若客户端按能力限制图片上传，需要在该模型的本地设置中启用视觉。

离线测试覆盖三种接口的 JSON/SSE、上传原始字节、上传完成后才创建推理、存储请求不泄露登录凭据、图片历史与图片独立消息、非法来源和大小/数量限制。全量 `go test -race ./...` 与 `go vet ./...` 通过。真实图片测试默认跳过，需设置 `NOTION_LIVE_VISION=1` 及 `NOTION_LIVE_ACCOUNT_FILE`；可选 `NOTION_LIVE_VISION_FIXTURE` 将合成图保存到本机。测试不会保存账号凭据或签名上传地址。

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

2026-10-09 的完整测试曾在 Responses SSE 和工具请求收到上游拒绝。2026-10-12 复查确认拒绝来自 `createAgentThread` 的 HTTP 400 / `UserValidationError`，没有明确的额度或限频标识。配置 15 秒账号起始间隔后，最终修复版在客户端不加延时的情况下通过全部八项：三种 API 的 JSON/SSE、强制工具调用及工具结果历史。客户端收到正确结束事件，工具结果返回 `42`。

排查中也曾出现普通 Responses JSON 的一次上游拒绝，因此本结果仅证明该账号本轮通过，不能承诺上游不再出现间歇性错误。详细记录见 [RELEASE_NOTES.md](../RELEASE_NOTES.md)。错误中保留允许列举的接口名称和 HTTP 状态，省略原始错误内容；不会自动重放创建请求。

## 验证方法

```powershell
go test ./... -count=1 -timeout 90s
go vet ./...
go test -race ./... -count=1 -timeout 120s
npm run build --prefix web
./scripts/test-live.ps1 -Model agent/opus-5.5
```

离线测试覆盖：三种 API 的 JSON/SSE、认证、模型替换拒绝、实际完成标记、文本修订及 Unicode、稳定 idle 不能完成、取消和超时、错误分类、模型缓存失效、强度校验、协议额度隔离、轮换、多工具调用及历史 ID、审批和分页拒绝、账号并发保存、设置持久化和凭据脱敏。

实时 Go 测试需显式设置 `NOTION_LIVE_ACCOUNT_FILE`；默认跳过。模型身份矩阵还需 `NOTION_LIVE_MODEL_MATRIX=1`，可设置 `NOTION_LIVE_REPORT_FILE` 输出脱敏报告。重复推理诊断可设 `NOTION_LIVE_AGENT_REPETITIONS=1..8`；六种 API 诊断可设 `NOTION_LIVE_API_SEQUENCE=1` 及 `NOTION_LIVE_MIN_REQUEST_INTERVAL_SECONDS=15`。测试仅创建合成消息，不批准操作。Windows 的 race detector 需要足够新的 MinGW-w64 运行库；本次使用校验过 SHA-256 的 w64devkit 2.10.0。POSIX 0600 位的测试在 Windows 跳过，不能将其视为 Windows ACL 已验证。
