# build.ps1 — Windows 전용 빌드.
# 기본은 Windows AMD64 만 빌드. 다른 OS/arch 까지 cross-build 하려면 -All 옵션.
#
# 사용:
#   .\build.ps1                  # UI + agent.exe (Windows AMD64) — CGO 자동 판단
#   .\build.ps1 -SkipUI          # UI 빌드 생략 (이미 ui/build 가 최신일 때)
#   .\build.ps1 -NoCGO           # CGO 강제 OFF — DuckDB 비활성 (trace 통계 동작 안 함)
#   .\build.ps1 -All             # dist/ 에 macOS/Linux/Windows 전부
#
# CGO 빌드:
#   - MinGW gcc (x86_64-w64-mingw32-gcc, 또는 msys2 의 gcc) 가 PATH 에 있으면 자동 사용
#   - 없으면 자동으로 CGO_ENABLED=0 으로 fallback (DuckDB 미포함 경고 출력)

param(
    [switch]$SkipUI,
    [switch]$NoCGO,
    [switch]$All
)

$ErrorActionPreference = 'Stop'
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Definition
Set-Location $ScriptDir

# git describe — 실패해도 무관
$Version = & git describe --tags --always 2>$null
if (-not $Version) { $Version = 'dev' }

$DistDir = 'dist'
New-Item -ItemType Directory -Force -Path $DistDir | Out-Null

if (-not $SkipUI) {
    Write-Host '=== Building UI (standalone embed) ==='
    & "$ScriptDir\build-ui.ps1"
} else {
    Write-Host '=== Skipping UI build (-SkipUI) ==='
}

Write-Host "=== Building agent v$Version ==="

# CGO 사용 가능 여부 판단
function Test-CGOCompiler {
    foreach ($cc in @('x86_64-w64-mingw32-gcc', 'gcc')) {
        $found = Get-Command $cc -ErrorAction SilentlyContinue
        if ($found) { return $cc }
    }
    return $null
}

$cc = if ($NoCGO) { $null } else { Test-CGOCompiler }

function Invoke-GoBuild {
    param(
        [string]$GOOS,
        [string]$GOARCH,
        [string]$Output,
        [string]$CompilerOrNull
    )
    $env:GOOS = $GOOS
    $env:GOARCH = $GOARCH
    if ($CompilerOrNull) {
        $env:CGO_ENABLED = '1'
        $env:CC = $CompilerOrNull
    } else {
        $env:CGO_ENABLED = '0'
        Remove-Item Env:\CC -ErrorAction SilentlyContinue
    }
    Write-Host "  → $Output  (GOOS=$GOOS GOARCH=$GOARCH CGO=$($env:CGO_ENABLED))"
    & go build -o $Output .
    if ($LASTEXITCODE -ne 0) { throw "go build 실패 ($Output, exit $LASTEXITCODE)" }
}

if ($All) {
    # macOS / Linux cross-build 는 CGO OFF (MinGW 는 Windows 전용)
    Invoke-GoBuild -GOOS 'darwin'  -GOARCH 'arm64' -Output "$DistDir\agent-darwin-arm64"   -CompilerOrNull $null
    Invoke-GoBuild -GOOS 'darwin'  -GOARCH 'amd64' -Output "$DistDir\agent-darwin-amd64"   -CompilerOrNull $null
    Invoke-GoBuild -GOOS 'linux'   -GOARCH 'amd64' -Output "$DistDir\agent-linux-amd64"    -CompilerOrNull $null
    Invoke-GoBuild -GOOS 'linux'   -GOARCH 'arm64' -Output "$DistDir\agent-linux-arm64"    -CompilerOrNull $null
}

# Windows AMD64 — 항상
$winOut = "$DistDir\agent-windows-amd64.exe"
if ($cc) {
    Write-Host "  (CGO compiler 발견: $cc — DuckDB 포함)"
    Invoke-GoBuild -GOOS 'windows' -GOARCH 'amd64' -Output $winOut -CompilerOrNull $cc
} else {
    if (-not $NoCGO) {
        Write-Warning '  MinGW gcc 미발견 — CGO_ENABLED=0 으로 빌드 (DuckDB 비활성, trace 통계 동작 안 함)'
        Write-Warning '  CGO 빌드를 원하면 https://www.msys2.org 에서 MinGW gcc 설치 후 PATH 등록'
    }
    Invoke-GoBuild -GOOS 'windows' -GOARCH 'amd64' -Output $winOut -CompilerOrNull $null
}

# ── iotest (Android arm64) ── CGO OFF, host MinGW 영향 없음
Write-Host ''
Write-Host '=== Building iotest (Android arm64) ==='
$env:GOOS = 'linux'; $env:GOARCH = 'arm64'; $env:CGO_ENABLED = '0'
Remove-Item Env:\CC -ErrorAction SilentlyContinue
& go build -o 'tools\iotest' .\cmd\iotest\
if ($LASTEXITCODE -ne 0) { throw 'iotest 빌드 실패' }
$iotestSize = (Get-Item 'tools\iotest').Length
Write-Host "  tools\iotest built ($([math]::Round($iotestSize / 1MB, 1)) MB)"

# env 정리 (현재 셸에 남지 않도록)
Remove-Item Env:\GOOS, Env:\GOARCH, Env:\CGO_ENABLED, Env:\CC -ErrorAction SilentlyContinue

Write-Host ''
Write-Host '=== Build complete ==='
Get-ChildItem $DistDir | Format-Table Name, @{N='Size(MB)';E={[math]::Round($_.Length/1MB,1)}}, LastWriteTime
