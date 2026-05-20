# build-ui.ps1 — Svelte UI 빌드 (Windows).
# Go 빌드 전에 실행해야 //go:embed all:ui/build 가 산출물을 임베드한다.
# build-ui.sh 의 Windows 포팅.

$ErrorActionPreference = 'Stop'

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Definition
Set-Location (Join-Path $ScriptDir 'ui')

if (-not (Test-Path 'node_modules')) {
    Write-Host '=== ui/ npm install ==='
    npm ci --no-audit --no-fund
    if ($LASTEXITCODE -ne 0) { throw "npm ci 실패 (exit $LASTEXITCODE)" }
}

Write-Host '=== ui/ build ==='
npm run build
if ($LASTEXITCODE -ne 0) { throw "npm run build 실패 (exit $LASTEXITCODE)" }

if (-not (Test-Path 'build/index.html')) {
    throw 'ERROR: ui/build/index.html not generated'
}

$size = (Get-ChildItem -Recurse build | Measure-Object -Property Length -Sum).Sum
$sizeMB = [math]::Round($size / 1MB, 1)
Write-Host "  ui build OK (${sizeMB} MB)"
