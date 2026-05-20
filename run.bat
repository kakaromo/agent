@echo off
REM run.bat — agent 빌드 + 실행 (CMD/더블클릭).
REM 내부적으로 run.ps1 호출. 인자는 그대로 전달.
REM
REM 사용:
REM   run.bat                                       — 기본 (사무실 모드, 0.0.0.0)
REM   run.bat -Standalone                           — standalone 모드 (127.0.0.1, UI 임베드)
REM   run.bat -Standalone -Bind 0.0.0.0             — standalone + LAN 공유
REM   run.bat -SkipBuild                            — agent.exe 만 실행
REM   run.bat -Config config\my.toml                — 다른 config
REM   run.bat -ExtraArgs --archive-base,D:\archive  — 추가 플래그
REM
REM 실행 정책 우회: -ExecutionPolicy Bypass

setlocal
set "SCRIPT_DIR=%~dp0"
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%SCRIPT_DIR%run.ps1" %*
set "RC=%ERRORLEVEL%"
endlocal & exit /b %RC%
