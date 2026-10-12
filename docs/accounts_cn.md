# 账号接入

[返回 README](../README_CN.md)

使用你自己的现有 Notion 账号登录，并确认该账号可以访问所需工作区和模型。

## 管理面板导入

1. 在浏览器登录 Notion。
2. 从自己的已登录会话中取得 `token_v2`。
3. 打开本地 `/dashboard/`，使用管理密码登录。
4. 点击“添加账号”，粘贴会话值并完成导入。
5. 查看账号的工作区状态，以及 Agent 面板中的实际模型 ID。

Windows 本地启动脚本生成的管理密码在 `.local-runtime/local-access.json`。请保留在本机；会话值、API Key 和管理密码均不能提交到 Git。

## 使用已有账号文件

通过管理面板导入后，程序会保存账号 JSON。已有的同格式账号文件可以放入配置中的 `server.accounts_dir`。

也可通过 `NOTION_TOKEN_V2` 在启动时接入现有会话；需要完整浏览器会话信息时，可设置 `NOTION_FULL_COOKIE`。这些值应来自你自己的登录会话。

模型名称及静态映射不代表账号授权。以 Notion 权限和请求实际返回的模型为准，详见 [Agent V3 文档](AGENT_V3.md)。
