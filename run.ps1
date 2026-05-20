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
    # go-duckdb 는 cgo 필수 — MinGW gcc 가 없으면 명확히 안내 후 종료
    $cc = $null
    foreach ($candidate in @('x86_64-w64-mingw32-gcc', 'gcc')) {
        if (Get-Command $candidate -ErrorAction SilentlyContinue) { $cc = $candidate; break }
    }
    if (-not $cc) {
        Write-Host ''
        Write-Host 'ERROR: MinGW gcc 가 PATH 에 없습니다.' -ForegroundColor Red
        Write-Host '설치: winget install MSYS2.MSYS2'
        Write-Host '      pacman -S --needed mingw-w64-ucrt-x86_64-gcc (MSYS2 UCRT64 셸)'
        Write-Host '      PATH 추가: C:\msys64\ucrt64\bin'
        Write-Host '자세한 안내: docs\11-deployment.md'
        throw 'MinGW gcc 가 필요합니다.'
    }
    $env:CGO_ENABLED = '1'
    $env:CC = $cc
    $env:CXX = $cc -replace 'gcc(\.exe)?$', 'g++$1'
    $env:CGO_LDFLAGS = '-static -static-libgcc -static-libstdc++ -lpthread -lstdc++'
    Write-Host "  CGO 활성 (CC=$cc CXX=$env:CXX)"
    & go build -o $Binary .
    if ($LASTEXITCODE -ne 0) { throw "go build 실패 (exit $LASTEXITCODE)" }
    Remove-Item Env:\CGO_ENABLED, Env:\CC, Env:\CXX, Env:\CGO_LDFLAGS -ErrorAction SilentlyContinue
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
