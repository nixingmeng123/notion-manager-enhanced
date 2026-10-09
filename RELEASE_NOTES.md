# Notion Manager Enhanced v0.1.0

原生 Go 集成 Personal Agent V3，保留原有 Workflow、账号池、认证和管理面板。三种兼容 API 支持 JSON/SSE；运行时核对 Notion 实际返回模型，模型替换时返回错误。

## Windows 快速启动

解压 Windows 压缩包，在解压目录运行：

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\start-local.ps1
```

打开 `http://127.0.0.1:8088/dashboard/`。首次启动生成独立 API Key 与面板密码，保存在 `.local-runtime/local-access.json`。通过面板导入自己的已登录 Notion 账号。Agent 已启用；显式使用 `agent/opus-5.5`，未带前缀的模型默认走 Workflow。关闭程序时可根据 `.local-runtime/process-id.txt` 查找进程。

发布包不含测试账号、Cookie、token_v2、API Key、管理密码或本机运行日志。

## 验证记录（2026-10-09）

- `go test ./...`、`go test -race ./...`、`go vet ./...` 通过。
- React TypeScript/Vite 构建及 Windows 可执行文件构建通过。
- 离线覆盖三种 API 的 JSON/SSE、工具调用和结果历史、模型身份校验、取消/超时、审批拒绝、认证、并发保存及凭据脱敏。
- 真实账号仅开放 Opus 5.5 后，实际模型 ID 为 `albuquerque-quinn`，返回 `AGENT_OK`；发布前再次复查成功。
- 真实 Messages JSON/SSE、Chat Completions JSON/SSE、Responses JSON 请求成功。最近一轮 Responses SSE 和工具请求被 Notion 上游拒绝，尚未取得这两项真实成功的复测证据。离线兼容性测试通过不能替代账号端实测。

## 已知边界

Agent 使用完整历史重放和 `polling-buffered` SSE，尚未支持原生线程续聊。上游 Token 用量未知；审批需用户在 Notion 内处理。模型目录和厂商名称属于候选映射，不代表所有账号均可使用；本次实际验证的 Opus ID 见上。详细说明见 [docs/AGENT_V3.md](docs/AGENT_V3.md)。
