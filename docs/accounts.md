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

## Existing account files

The dashboard saves an account JSON file after import. Existing account files in the same format can be placed in the configured `server.accounts_dir`.

You can also supply `NOTION_TOKEN_V2` at startup. Set `NOTION_FULL_COOKIE` when full browser-session context is needed. These values should come from your own signed-in session.

Model names and static mappings do not grant account access. Notion permissions and the model actually returned by an inference remain authoritative. See [Agent V3 documentation](AGENT_V3.md).
