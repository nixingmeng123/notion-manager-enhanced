$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$runtimeDirectory = Join-Path $projectRoot '.local-runtime'
$executable = Join-Path $projectRoot 'dist/notion-manager-enhanced.exe'
if (-not (Test-Path -LiteralPath $executable)) { throw 'Run scripts/build.ps1 first.' }
New-Item -Path $runtimeDirectory -ItemType Directory -Force | Out-Null
$configPath = Join-Path $runtimeDirectory 'config.yaml'
if (-not (Test-Path -LiteralPath $configPath)) {
    $apiKey = [Guid]::NewGuid().ToString('N') + [Guid]::NewGuid().ToString('N')
    $adminPassword = [Guid]::NewGuid().ToString('N')
    $credentialsPath = Join-Path $runtimeDirectory 'local-access.json'
    @{ dashboard_password = $adminPassword; api_key = $apiKey; base_url = 'http://127.0.0.1:8088' } |
        ConvertTo-Json | Set-Content -LiteralPath $credentialsPath -Encoding utf8
    @"
server:
  host: "127.0.0.1"
  port: "8088"
  accounts_dir: "accounts"
  api_key: "$apiKey"
  admin_password: "$adminPassword"
  log_file: "server.log"
agent_v3:
  enabled: true
  timeout_seconds: 300
  poll_interval_ms: 3000
  model_cache_seconds: 300
  min_request_interval_seconds: 15
proxy:
  default_protocol: "workflow"
"@ | Set-Content -LiteralPath $configPath -Encoding utf8
    # Windows chmod does not apply POSIX 0600 semantics. Limit this local test
    # directory to the current Windows user instead of relying on mode bits.
    $currentIdentity = [Security.Principal.WindowsIdentity]::GetCurrent().Name
    & icacls $runtimeDirectory /inheritance:r /grant:r "${currentIdentity}:(OI)(CI)F" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Could not restrict local runtime permissions.' }
}
$existing = Get-NetTCPConnection -State Listen -LocalPort 8088 -ErrorAction SilentlyContinue
if ($existing) { throw 'Port 8088 is already in use; the existing process was left running.' }
$process = Start-Process -FilePath $executable -WorkingDirectory $runtimeDirectory -WindowStyle Hidden -PassThru `
    -RedirectStandardOutput (Join-Path $runtimeDirectory 'stdout.log') `
    -RedirectStandardError (Join-Path $runtimeDirectory 'stderr.log')
$process.Id | Set-Content -LiteralPath (Join-Path $runtimeDirectory 'process-id.txt')
$startupDeadline = (Get-Date).AddSeconds(15)
$ready = $false
do {
    try { $null = Invoke-RestMethod -Uri 'http://127.0.0.1:8088/health' -TimeoutSec 1; $ready = $true }
    catch { Start-Sleep -Milliseconds 300 }
    $process.Refresh()
} while (-not $ready -and -not $process.HasExited -and (Get-Date) -lt $startupDeadline)
if (-not $ready) { throw 'Local server did not become ready; see .local-runtime/stderr.log.' }
Write-Output 'Dashboard: http://127.0.0.1:8088/dashboard/'
Write-Output "Local credentials: $(Join-Path $runtimeDirectory 'local-access.json')"
