# Account access

[Back to README](../README.md)

Sign in with your own existing Notion account and confirm access to the required workspace and model.

## Import through the dashboard

1. Sign in to Notion in your browser.
2. Obtain `token_v2` from your own signed-in session.
3. Open the local `/dashboard/` and sign in with the dashboard password.
4. Select **Add account**, paste the session value and complete the import.
5. Inspect workspace status and the actual model ID shown in the Agent panel.

The Windows local startup script saves the dashboard password in `.local-runtime/local-access.json`. Keep this file local. Never commit session values, API keys or dashboard passwords.

An account can belong to several workspaces, including workspaces with identical names. Automatic import prefers AI-enabled workspaces and then a paid plan; equally ranked workspaces retain Notion's first linked workspace. This does not establish that all workspaces have the same model or image permissions.

For an exact workspace, send `notion_space_id` with `token_v2` to `POST /admin/accounts/add` using a dashboard session. `notion_user_id` is also optional when a browser session contains multiple users. The response includes `account.space_id` and `account.account_id` so you can verify the binding. An inaccessible workspace is rejected instead of falling back to a different workspace. Account identity includes both user and workspace, so importing another workspace creates a separate entry; remove an unintended entry explicitly after verifying the replacement.

## Existing account files

The dashboard saves an account JSON file after import. Existing account files in the same format can be placed in the configured `server.accounts_dir`.

You can also supply `NOTION_TOKEN_V2` at startup. Set `NOTION_FULL_COOKIE` when full browser-session context is needed. These values should come from your own signed-in session.

Model names and static mappings do not grant account access. Notion permissions and the model actually returned by an inference remain authoritative. See [Agent V3 documentation](AGENT_V3.md).
