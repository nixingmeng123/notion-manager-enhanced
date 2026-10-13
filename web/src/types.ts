export interface Model {
  id: string
  name: string
}

export interface ProtocolModel {
  id: string
  protocol: 'workflow' | 'agent'
  available: boolean
  authorization?: string
  authorization_scope?: string
  internal_slug?: string
  reasoning_efforts?: string[]
  reasoning_source?: string
}

export interface AgentCapability {
  status: string
  quota: string
  checked_at?: string
  cooldown_until?: string
  requests: number
  failures: Record<string, number>
  model_selection_restricted?: boolean
  models?: { internal_slug: string; name: string; authorization: string; reasoning_efforts: string[]; actual_slug?: string; actual_effort?: string; model_selection?: string }[]
}

export interface AccountInfo {
	quota_display?: QuotaDisplay
	workspace_key?: string
	workflow_status?: { available: boolean }
	agent_capability?: AgentCapability
  account_id: string
  email: string
  name: string
  plan: string
  space: string
  space_id_short?: string
  exhausted: boolean
  permanent: boolean
  // no_workspace is true when the backend probed loadUserContent and found
  // that user_root.space_views is empty — the /ai SPA gets stuck on a
  // skeleton screen for these accounts. Dashboard treats them as
  // unusable (no click-through, sorted to the bottom, dedicated badge).
  no_workspace?: boolean
  // space_count is the raw probe result. Absent when the backend never
  // probed (fresh registration before the first refresh tick).
  space_count?: number
  workspace_checked_at?: string
  eligible?: boolean
  usage?: number
  limit?: number
  remaining?: number
  space_usage?: number
  space_limit?: number
  space_remaining?: number
  user_usage?: number
  user_limit?: number
  user_remaining?: number
  checked_at?: string
  exhausted_at?: string
  last_usage_at?: number
  models?: Model[]
  research_usage?: number
  has_premium?: boolean
  premium_balance?: number
  premium_usage?: number
  premium_limit?: number
  token_v2?: string
  registered_via?: string
}

export interface ProviderInfo {
  id: string
  display: string
  format_hint: string
  recommended_concurrency: number
  enabled: boolean
}

export interface AIUsageWindow {
  used: number
  limit: number
  used_percent: number
  resets_at_ms?: number
  window?: string
}

export interface QuotaDisplay {
  source: string
  scope: string
  ai_usage: { status: string; source: string; checked_at: string; monthly?: AIUsageWindow; rolling?: AIUsageWindow }
  member_credits: { status: string; used?: number; limit?: number }
}

export type JobState = 'running' | 'done' | 'cancelled'
export type StepStatus = 'pending' | 'running' | 'ok' | 'fail'

export interface RegisterStep {
  email: string
  status: StepStatus
  message?: string
  space_id?: string
  user_id?: string
  file?: string
  started_at?: number
  ended_at?: number
}

export interface RegisterJob {
  id: string
  created_at: number
  ended_at?: number
  provider?: string
  proxy?: string
  concurrency: number
  total: number
  ok: number
  fail: number
  done: number
  state: JobState
  steps: RegisterStep[]
}

export interface JobStartResponse {
  job_id: string
  provider: string
  total: number
  concurrency: number
  proxy?: string
  retry_of?: string
}

export interface RefreshStatus {
  refreshing: boolean
  done: number
  total: number
  last_refresh_at?: string
  error?: string
}

// AccountSummary mirrors Go's proxy.AccountSummary. The backend computes
// pool-wide aggregates (counts, quota sums) so the dashboard headline
// cards don't need to download the full account list to render. These
// fields reflect ALL accounts regardless of the current ?q= filter, so
// the headline numbers stay stable while the user searches.
export interface AccountSummary {
  workspace_count?: number
  monthly_usage_known?: number
  highest_monthly_used_percent?: number
  exhausted_only: number
  no_workspace: number
  premium_accounts: number
  research_limited: number
  total_research_usage: number
  total_remaining: number
  total_space_usage: number
  total_space_limit: number
  total_space_remaining: number
  total_user_usage: number
  total_user_limit: number
  total_user_remaining: number
  total_premium_balance: number
  total_premium_limit: number
}

export interface DashboardData {
	protocol_models?: ProtocolModel[]
	agent_stats?: { requests: number; failures: Record<string, number> }
  total: number
  available: number
  models: Model[]
  accounts: AccountInfo[]
  refresh?: RefreshStatus
  // Present whenever the request used pagination params. `accounts` is
  // already the page slice; use `filtered_total` to compute pagination
  // controls on the client.
  page?: number
  page_size?: number
  filtered_total?: number
  // Pool-wide aggregates over ALL accounts (independent of ?q=).
  // The backend always emits this; the optional marker keeps older
  // dev builds tolerant.
  summary?: AccountSummary
}

export interface TokenBucket {
  input: number
  output: number
  total: number
  requests?: number
}

export interface TokenDayPoint {
  date: string
  input: number
  output: number
  total: number
}

export interface TokenModelRow {
  model: string
  input: number
  output: number
  total: number
  count: number
}

export interface TokenAccountRow {
  email: string
  input: number
  output: number
  total: number
  count: number
}

export interface TokenStats {
  total: TokenBucket
  today: TokenBucket
  last_24h: TokenBucket
  by_day: TokenDayPoint[]
  top_models: TokenModelRow[]
  top_accounts: TokenAccountRow[]
  last_record_at: number
}
