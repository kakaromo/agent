# run.ps1 — Windows 에서 agent 빌드 + 실행. run.sh 의 Windows 포팅.
#
# 사용:
#   .\run.ps1                                      # standalone 모드, 127.0.0.1 (기본)
#   .\run.ps1 -Standalone -Bind 0.0.0.0            # LAN 공유
#   .\run.ps1 -Config config\my.toml               # 다른 config
#   .\run.ps1 -SkipBuild                           # 이미 빌드된 agent.exe 만 실행
#   .\run.ps1 -ExtraArgs '--archive-base','D:\arc' # 추가 플래그 그대로 전달

param(
    [string]$Config = 'config\devices.toml',
    [switch]$Standalone,
    [string]$Bind = '',
    [string]$DbPath = '',
    [string]$ArchiveBase = '',
    [switch]$SkipBuild,
    [string[]]$ExtraArgs = @()
)

$ErrorActionPreference = 'Stop'
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Definition
Set-Location $ScriptDir

$Binary = '.\agent.exe'

if (-not $SkipBuild) {
    Write-Host '=== Building UI ==='
    & "$ScriptDir\build-ui.ps1"

    Write-Host '=== Building agent ==='
    # CGO 가 있으면 켜고, 없으면 OFF — build.ps1 와 같은 정책을 inline 으로
    $cc = $null
    foreach ($candidate in @('x86_64-w64-mingw32-gcc', 'gcc')) {
        if (Get-Command $candidate -ErrorAction SilentlyContinue) { $cc = $candidate; break }
    }
    if ($cc) {
        $env:CGO_ENABLED = '1'; $env:CC = $cc
        Write-Host "  CGO 활성 ($cc)"
    } else {
        $env:CGO_ENABLED = '0'
        Remove-Item Env:\CC -ErrorAction SilentlyContinue
        Write-Warning '  MinGW 미발견 — CGO OFF (trace 통계 동작 안 함)'
    }
    & go build -o $Binary .
    if ($LASTEXITCODE -ne 0) { throw "go build 실패 (exit $LASTEXITCODE)" }
    Remove-Item Env:\CGO_ENABLED, Env:\CC -ErrorAction SilentlyContinue
}

if (-not (Test-Path $Binary)) {
    throw "$Binary 가 없습니다. -SkipBuild 를 빼고 다시 실행하세요."
}

# 인자 조립
$argList = @('-config', $Config)
if ($Standalone)        { $argList += '--standalone' }
if ($Bind)              { $argList += @('--bind', $Bind) }
if ($DbPath)            { $argList += @('--db-path', $DbPath) }
if ($ArchiveBase)       { $argList += @('--archive-base', $ArchiveBase) }
if ($ExtraArgs)         { $argList += $ExtraArgs }

Write-Host "=== Starting agent (config: $Config) ==="
Write-Host "    $Binary $($argList -join ' ')"
& $Binary @argList
