param([string]$GoExecutable = 'go')
$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
Push-Location $projectRoot
try {
    if (-not (Get-Command $GoExecutable -ErrorAction SilentlyContinue)) {
        $bundledGo = Join-Path $projectRoot '../.tools/go/bin/go.exe'
        if (Test-Path -LiteralPath $bundledGo) { $GoExecutable = $bundledGo }
        else { throw 'Go 1.26 or newer is required.' }
    }
    if (-not (Test-Path -LiteralPath 'web/node_modules')) {
        & npm ci --prefix web
        if ($LASTEXITCODE -ne 0) { throw 'npm ci failed.' }
    }
    & npm run build --prefix web
    if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed.' }
    $embedDirectory = [IO.Path]::GetFullPath((Join-Path $projectRoot 'internal/web/dist'))
    if (-not $embedDirectory.StartsWith($projectRoot + [IO.Path]::DirectorySeparatorChar)) {
        throw 'Embedded output is outside the project.'
    }
    # Replace generated files only, using native PowerShell throughout.
    Get-ChildItem -LiteralPath $embedDirectory -File -Recurse | ForEach-Object {
        Remove-Item -LiteralPath $_.FullName
    }
    Copy-Item -Path 'web/dist/*' -Destination $embedDirectory -Recurse -Force
    New-Item -Path 'dist' -ItemType Directory -Force | Out-Null
    & $GoExecutable build -trimpath -o dist/notion-manager-enhanced.exe ./cmd/notion-manager
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed.' }
    Write-Output (Join-Path $projectRoot 'dist/notion-manager-enhanced.exe')
}
finally { Pop-Location }
