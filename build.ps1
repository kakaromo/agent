# build.ps1 — Windows 전용 빌드.
# 기본은 Windows AMD64 만 빌드. 다른 OS/arch 까지 cross-build 하려면 -All 옵션.
#
# 사용:
#   .\build.ps1                  # UI + agent.exe (Windows AMD64)
#   .\build.ps1 -SkipUI          # UI 빌드 생략 (이미 ui/build 가 최신일 때)
#   .\build.ps1 -All             # dist/ 에 macOS/Linux/Windows 전부
#
# 필수: MinGW gcc (CGO 빌드용)
#   go-duckdb 가 cgo 의존이라 MinGW gcc 없이는 컴파일 불가
#   (CGO_ENABLED=0 으로 빌드 시도 시 `undefined: Conn` 류 컴파일 에러 발생).
#
#   설치:
#     winget install MSYS2.MSYS2
#     MSYS2 UCRT64 셸에서:  pacman -S --needed mingw-w64-ucrt-x86_64-gcc
#     PATH 추가:            C:\msys64\ucrt64\bin
#     검증:                 gcc --version

param(
    [switch]$SkipUI,
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

# MinGW gcc 필수 — go-duckdb 가 cgo 의존이라 없으면 컴파일 불가
function Find-CGOCompiler {
    foreach ($cc in @('x86_64-w64-mingw32-gcc', 'gcc')) {
        $found = Get-Command $cc -ErrorAction SilentlyContinue
        if ($found) { return $cc }
    }
    return $null
}

$cc = Find-CGOCompiler
if (-not $cc) {
    Write-Host ''
    Write-Host 'ERROR: MinGW gcc 가 PATH 에 없습니다.' -ForegroundColor Red
    Write-Host ''
    Write-Host 'go-duckdb (trace 통계 의존) 는 cgo 빌드라 MinGW gcc 가 필요합니다.'
    Write-Host '아래 절차로 설치하세요:'
    Write-Host ''
    Write-Host '  1) MSYS2 설치:    winget install MSYS2.MSYS2'
    Write-Host '  2) MSYS2 UCRT64 셸 열고:'
    Write-Host '     pacman -S --needed mingw-w64-ucrt-x86_64-gcc'
    Write-Host '  3) Windows PATH 에 추가:    C:\msys64\ucrt64\bin'
    Write-Host '  4) 새 PowerShell 열고 검증:  gcc --version'
    Write-Host ''
    Write-Host '자세한 안내: docs\11-deployment.md'
    throw 'MinGW gcc 가 필요합니다.'
}

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
        # MSYS2 UCRT64 의 `gcc` → `g++`, mingw-w64 cross 의 `x86_64-w64-mingw32-gcc` → `x86_64-w64-mingw32-g++`
        $env:CXX = $CompilerOrNull -replace 'gcc(\.exe)?$', 'g++$1'
        # DuckDB 는 C++/pthread 의존 — 정적 링크 강제
        $env:CGO_LDFLAGS = '-static -static-libgcc -static-libstdc++ -lpthread -lstdc++'
    } else {
        $env:CGO_ENABLED = '0'
        Remove-Item Env:\CC, Env:\CXX, Env:\CGO_LDFLAGS -ErrorAction SilentlyContinue
    }
    Write-Host "  → $Output  (GOOS=$GOOS GOARCH=$GOARCH CGO=$($env:CGO_ENABLED) CC=$env:CC)"
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

# Windows AMD64 — 항상 cgo (MinGW gcc 발견 확인은 위에서 끝남)
Write-Host "  (CGO compiler: $cc — DuckDB 포함)"
$winOut = "$DistDir\agent-windows-amd64.exe"
Invoke-GoBuild -GOOS 'windows' -GOARCH 'amd64' -Output $winOut -CompilerOrNull $cc

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
