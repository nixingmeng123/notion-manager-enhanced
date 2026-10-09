param([string]$BaseUrl = 'http://127.0.0.1:8088', [string]$Model = 'agent/opus-5.5', [switch]$CheckOtherModels,
    [ValidateSet('all','responses_stream','tools')][string]$Probe = 'all')
$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$runtimeDirectory = Join-Path $projectRoot '.local-runtime'
$access = Get-Content -LiteralPath (Join-Path $runtimeDirectory 'local-access.json') -Raw | ConvertFrom-Json
$headers = @{ Authorization = 'Bearer ' + $access.api_key }
$results = [Collections.Generic.List[object]]::new()
# Refresh through the existing dashboard authentication, without logging the
# password, hash, API key or session cookie.
$salt = (Invoke-RestMethod ($BaseUrl + '/dashboard/auth/salt')).salt
$clientHash = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData([Text.Encoding]::UTF8.GetBytes($salt + $access.dashboard_password))).ToLowerInvariant()
$null = Invoke-RestMethod -Method POST -Uri ($BaseUrl + '/dashboard/auth/login') -ContentType application/json `
    -Body (@{hash=$clientHash} | ConvertTo-Json -Compress) -SessionVariable adminSession
$null = Invoke-RestMethod -Method POST -Uri ($BaseUrl + '/admin/refresh') -WebSession $adminSession
$deadline=(Get-Date).AddSeconds(65)
do {
    Start-Sleep -Milliseconds 750
    $refresh=Invoke-RestMethod ($BaseUrl + '/admin/refresh') -WebSession $adminSession
} while ($refresh.refreshing -and (Get-Date) -lt $deadline)
function Invoke-LiveProbe([string]$Name, [string]$Endpoint, [hashtable]$Body) {
    $started = Get-Date
    $response = Invoke-WebRequest -Method POST -Uri ($BaseUrl + $Endpoint) -Headers $headers -ContentType application/json `
        -Body ($Body | ConvertTo-Json -Depth 15 -Compress) -SkipHttpErrorCheck -TimeoutSec 120
    $entry = @{ name = $Name; status = [int]$response.StatusCode; seconds = [Math]::Round(((Get-Date)-$started).TotalSeconds,2); body = $response.Content }
    $results.Add($entry)
    Write-Host "$Name HTTP $($entry.status) ($($entry.seconds)s)"
    return $response
}
# Each request is explicit; tools are synthetic and are never executed.
foreach ($stream in @($false,$true)) {
    if ($Probe -eq 'tools') { continue }
    if ($Probe -eq 'all') {
    $null = Invoke-LiveProbe "messages_stream=$stream" '/v1/messages' @{
        model=$Model; stream=$stream; messages=@(@{role='user';content='Reply with exactly API_OK. Do not use tools or modify workspace content.'})
    }
    $null = Invoke-LiveProbe "chat_stream=$stream" '/v1/chat/completions' @{
        model=$Model; stream=$stream; messages=@(@{role='user';content='Reply with exactly API_OK. Do not use tools or modify workspace content.'})
    }
    }
    if ($Probe -eq 'responses_stream' -and -not $stream) { continue }
    $null = Invoke-LiveProbe "responses_stream=$stream" '/v1/responses' @{
        model=$Model; stream=$stream; input='Reply with exactly API_OK. Do not use tools or modify workspace content.'
    }
}
foreach ($candidate in $(if ($CheckOtherModels) {@('agent/gpt-6.1-sol','agent/kimi-k3')} else {@()})) {
    $null = Invoke-LiveProbe $candidate '/v1/chat/completions' @{
        model=$candidate; messages=@(@{role='user';content='Model identity check. Reply with OK. Do not use tools or modify pages.'})
    }
}
$tools = @(
    @{name='lookup_test_value';description='Return a synthetic test value for the named key.';input_schema=@{type='object';properties=@{key=@{type='string'}};required=@('key')}}
)
$body = @{
    model=$Model; stream=$false; tools=$tools; tool_choice=@{type='tool';name='lookup_test_value'}
    messages=@(@{role='user';content='Call lookup_test_value with key alpha. This is a synthetic API tool-call test; do not use built-in tools or modify pages.'})
}
if ($Probe -in @('all','tools')) {
$first = Invoke-LiveProbe 'tool_use' '/v1/messages' $body
if ($first -is [array]) { $first = $first[-1] }
if ($first.StatusCode -eq 200) {
    $reply = $first.Content | ConvertFrom-Json
    $calls = @($reply.content | Where-Object {$_.type -eq 'tool_use'})
    if ($calls.Count -gt 0) {
        $body.tool_choice=@{type='auto'}
        $body.messages=@(
            @{role='user';content='Call lookup_test_value with key alpha. This is a synthetic tool test.'}
            @{role='assistant';content=$reply.content}
            @{role='user';content=@($calls | ForEach-Object {@{type='tool_result';tool_use_id=$_.id;content='synthetic result: 42'}})}
            @{role='user';content='Based on the returned tool result, answer exactly 42. Do not call further tools.'}
        )
        $null = Invoke-LiveProbe 'tool_result_history' '/v1/messages' $body
    }
}
}
$results | ConvertTo-Json -Depth 15 | Set-Content -LiteralPath (Join-Path $runtimeDirectory 'live-api-results.json') -Encoding utf8
Write-Output "Sanitized report: $(Join-Path $runtimeDirectory 'live-api-results.json')"
$failures = @($results | Where-Object {$_.status -ne 200})
foreach ($entry in $results) {
    if ($entry.status -ne 200) { continue }
    $valid = switch -Wildcard ($entry.name) {
        '*stream=True' {
            $entry.body.Contains('API_OK') -and (
                ($entry.name.StartsWith('messages_') -and $entry.body.Contains('message_stop')) -or
                ($entry.name.StartsWith('chat_') -and $entry.body.Contains('[DONE]')) -or
                ($entry.name.StartsWith('responses_') -and $entry.body.Contains('response.completed')))
        }
        '*stream=False' { $entry.body.Contains('API_OK') }
        'tool_use' {
            $parsed = $entry.body | ConvertFrom-Json
            $call = @($parsed.content | Where-Object {$_.type -eq 'tool_use' -and $_.name -eq 'lookup_test_value' -and $_.id -and $_.input.key -eq 'alpha'})
            $parsed.stop_reason -eq 'tool_use' -and $call.Count -gt 0
        }
        'tool_result_history' {
            $parsed = $entry.body | ConvertFrom-Json
            $text = ($parsed.content | Where-Object {$_.type -eq 'text'} | ForEach-Object {$_.text}) -join ''
            $parsed.stop_reason -eq 'end_turn' -and $text.Trim() -eq '42'
        }
        default { $true }
    }
    if (-not $valid) { $failures += $entry }
}
if ($Probe -in @('all','tools') -and -not ($results | Where-Object {$_.name -eq 'tool_result_history'})) {
    throw 'Tool history probe was not completed; see the sanitized report.'
}
if ($failures.Count -gt 0) { throw "Live verification failed: $($failures.name -join ', '). See the sanitized report." }
Write-Output "PASS: $($results.Count) live probes."
