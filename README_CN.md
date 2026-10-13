# Notion Manager Enhanced

[English](README.md) | 简体中文

使用 Go 实现的 Notion Personal Agent V3 本地客户端。通过用户自己的已登录账号连接 Notion，提供模型身份校验、会话处理、流式响应和本地管理界面。

项目基于 [notion_manager](https://github.com/SleepingBag945/notion_manager) 扩展，原生接入 Personal Agent V3，并保留 Workflow 协议兼容。模型可用性由 Notion 账号权限及上游实际返回结果决定。

## Windows 快速开始

1. 从 [Releases](https://github.com/nixingmeng123/notion-manager-enhanced/releases) 下载 Windows 压缩包并解压。
2. 在解压目录运行：

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\start-local.ps1
```

3. 打开 `http://127.0.0.1:8088/dashboard/`。
4. 使用首次启动生成的管理密码登录。管理密码和 API Key 保存在本机 `.local-runtime/local-access.json`。
5. 在“添加账号”中导入自己的已登录 Notion 会话。
6. 从 `/v1/models` 返回的已验证列表中选择 `agent/` 模型，调用 Personal Agent V3。

启动脚本采用每账号 15 秒的请求起始间隔。升级已有配置后，可在管理面板将“账号请求间隔”设为 15 秒；旧配置不会被启动脚本自动改写。

## 当前功能

- **账号接入与本地管理**：导入现有账号，查看账号、工作区和模型状态，使用独立管理密码登录。
- **双协议路由**：`agent/` 使用 Personal Agent V3，`workflow/` 使用 Workflow；未带前缀的模型默认采用 Workflow。
- **模型身份校验**：核对 Notion 实际返回的模型 ID；上游选择了其他模型时明确报错。
- **账号路由**：优先使用当前 Agent 模型已完成身份验证的账号，在已验证账号之间轮询；一个账号调用成功不代表其他导入账号也有权限。
- **客户端模型列表**：只列账号真实目录中的 Workflow 模型及已实测确认的 Agent 模型，去掉静态候选与重复名称。
- **会话与响应处理**：支持完整历史重放、JSON 响应、轮询后缓冲输出 SSE，以及工具调用和工具结果历史的格式转换。
- **请求控制**：支持超时、取消、每账号请求间隔；明确指定思考强度时核对实际返回值。
- **联网搜索**：Agent 采用 Notion 原生网页检索，允许与否由管理面板或 `X-Web-Search` 请求头控制；实际检索以工具结果为准。
- **图片输入**：Messages 的图片块、Chat Completions 的 `image_url` 和 Responses 的 `input_image` 通过 Agent Service 上传并以文件 ID 附加。Opus 5.5 已实测能够识图。
- **图片生成**：通过 `/v1/images/generations` 调用 Notion 原生生图，返回图片数据及实际模型、提供方。详见[参数与实测记录](docs/IMAGE_GENERATION.md)。
- **用量显示**：读取个人 AI 月度用量和重置日期，区分成员 credit 限额、工作区共享 Credits 与旧版历史免费用量。同一工作区的共享余额只汇总一次。
- **运行设置**：管理面板可调整 Agent 开关、默认协议、超时、轮询间隔和请求间隔。

## 客户端接入

| 路径 | 用途 |
| --- | --- |
| `POST /v1/messages` | Messages 请求格式 |
| `POST /v1/chat/completions` | Chat Completions 请求格式 |
| `POST /v1/responses` | Responses 请求格式 |
| `POST /v1/images/generations` | 原生生图（`notion-image`） |
| `GET /v1/models` | 模型状态列表 |

请求使用 `Authorization: Bearer <api_key>` 或 `x-api-key: <api_key>` 鉴权。

```json
{
  "model": "agent/opus-5.5",
  "messages": [{"role": "user", "content": "你好"}]
}
```

此前测试工作区开放 Opus 5.5 时，曾实测返回 `albuquerque-quinn`。同一工作区后来在商业试用测试中自动返回 `omniberry-sundae`，目前该账号只确认了聊天模型 `agent/gpt-6.1-sol` 的身份一致。使用时以自己账号的已验证模型列表为准；静态映射不代表权限或调用成功。

生图使用模型名 `notion-image`，调用 `/v1/images/generations`，设置 `n: 1`、`size: "auto"` 和 `response_format: "b64_json"`。2026-10-13 实测返回 `nano-banana` / `google`。首次成功生图后模型列表才会出现此项；Notion 自动选择提供方和图像模型。具体示例见[生图文档](docs/IMAGE_GENERATION.md)。

## 源码运行

源码要求 Go 1.26 或更高版本；修改前端时还需要 Node.js/npm。

```bash
git clone https://github.com/nixingmeng123/notion-manager-enhanced.git
cd notion-manager-enhanced
go run ./cmd/notion-manager
```

直接运行 Go 入口时，端口取自 `config.yaml`，缺省为 `8081`；Windows 本地启动脚本使用 `8088`。前端资源已嵌入源码。需要重新构建时，参见 `scripts/build.ps1`。

## 验证与边界

v0.1.1 已通过 Go 测试、竞态检测、静态检查和前端构建。使用仅开放 Opus 5.5 的测试账号，Messages、Chat Completions、Responses 的 JSON/SSE，以及合成工具调用和结果历史，共 8 项实测通过。

Agent 尚未支持原生线程续聊，SSE 采用轮询后缓冲输出。上游 Token 用量未知，工具格式兼容不代表网关会替客户端执行工具。明确不支持的请求能力会报错。

上游仍可能返回间歇性 HTTP 400。15 秒间隔是本账号的实测缓解设置，并非 Notion 公布的限额。完整证据和限制见 [发布说明](RELEASE_NOTES.md)及 [Agent V3 文档](docs/AGENT_V3.md)。

## 文档

- [Agent V3](docs/AGENT_V3.md)：模型映射、权限、思考强度和实测记录。
- [账号接入](docs/accounts_cn.md)：导入现有 Notion 会话。
- [管理面板](docs/dashboard_cn.md)：登录、账号状态和运行设置。
- [配置说明](docs/configuration_cn.md)：运行配置及环境变量。
- [客户端接入](docs/api_cn.md)：请求格式与 Workflow 兼容说明；Agent 能力边界以 Agent V3 文档为准。

## 许可证

沿用原项目的 [CC BY-NC-SA 4.0](LICENSE) 许可证，仅限非商业用途。
