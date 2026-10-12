# Notion Manager Enhanced v0.1.1

原生 Go 集成 Personal Agent V3，保留原有 Workflow、账号池、认证和管理面板。三种兼容 API 支持 JSON/SSE；运行时核对 Notion 实际返回模型，模型替换时返回错误。

本版解决测试账号密集调用时 Responses SSE 和工具请求被上游拒绝的问题：新增每账号的请求起始间隔，等待期间可取消，上次起始时间保存在原账号文件中。间隔可在面板设置，范围 0–300 秒，0 关闭。本地启动脚本和示例配置采用 15 秒。

未指定推理强度时使用上游默认值；只有明确指定强度时才要求实际返回一致。错误增加上游接口名称与 HTTP 状态，不回显上游原文或凭据。失败请求不会自动重复创建线程。

## Windows 快速启动

解压 Windows 压缩包，在解压目录运行：

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\start-local.ps1
```

打开 `http://127.0.0.1:8088/dashboard/`。首次启动生成独立 API Key 与面板密码，保存在 `.local-runtime/local-access.json`。通过面板导入自己的已登录 Notion 账号。Agent 已启用；显式使用 `agent/opus-5.5`，未带前缀的模型默认走 Workflow。关闭程序时可根据 `.local-runtime/process-id.txt` 查找进程。

发布包不含测试账号、Cookie、token_v2、API Key、管理密码或本机运行日志。

旧版升级后保留已有账号与配置，并在面板将“账号请求间隔”设为 **15 秒**。已有配置不会被启动脚本自动改写。

## 验证记录（2026-10-12）

- `go test ./...`、`go test -race ./...`、`go vet ./...` 通过。
- React TypeScript/Vite 构建及 Windows 可执行文件构建通过。
- 离线覆盖三种 API 的 JSON/SSE、工具调用和结果历史、模型身份校验、取消/超时、审批拒绝、认证、并发保存及凭据脱敏。
- 最后一轮使用修复版可执行文件，客户端没有添加延时，网关按账号自动等待。Messages、Chat Completions、Responses 的 JSON/SSE 六项，以及强制工具调用、工具结果历史两项，**8/8 均为 HTTP 200**。
- SSE 校验完整结束事件；工具调用校验名称、参数及 ID，回传合成结果后正确返回 `42`。工具不会实际执行。
- Opus 请求实际返回 ID 为 `albuquerque-quinn`。未重新开放或验证其他模型。
- 排查中观察到 `createAgentThread` 的 HTTP 400 / `UserValidationError`；15 秒间隔是本账号的实测缓解设置，不是 Notion 公布的限额。排查中也曾发生一次普通 Responses JSON 请求被拒绝；上游仍可能出现间歇性错误。

## 已知边界

Agent 使用完整历史重放和 `polling-buffered` SSE，尚未支持原生线程续聊。上游 Token 用量未知；审批需用户在 Notion 内处理。模型目录和厂商名称属于候选映射，不代表所有账号均可使用；本次实际验证的 Opus ID 见上。详细说明见 [docs/AGENT_V3.md](docs/AGENT_V3.md)。
