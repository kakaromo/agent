@echo off
REM build.bat — Windows agent + UI 빌드 (CMD/더블클릭).
REM 내부적으로 build.ps1 호출. 인자는 그대로 전달.
REM
REM 사용:
REM   build.bat                 — UI + agent.exe (기본)
REM   build.bat -SkipUI         — UI 생략
REM   build.bat -NoCGO          — CGO OFF (DuckDB 미포함)
REM   build.bat -All            — macOS/Linux 까지 cross-build
REM
REM 실행 정책 우회: -ExecutionPolicy Bypass

setlocal
set "SCRIPT_DIR=%~dp0"
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%SCRIPT_DIR%build.ps1" %*
set "RC=%ERRORLEVEL%"
endlocal & exit /b %RC%
