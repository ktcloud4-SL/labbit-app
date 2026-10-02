@echo off
setlocal

cd /d "%~dp0web"
if errorlevel 1 (
  echo [Labbit] web 폴더로 이동하지 못했습니다.
  exit /b 1
)

echo [Labbit] Figma 캡처 자동화를 시작합니다...
call npm.cmd run capture:figma
set "exitCode=%errorlevel%"

if not "%exitCode%"=="0" (
  echo.
  echo [Labbit] 캡처가 실패했습니다. 위 오류 메시지를 확인해 주세요.
  exit /b %exitCode%
)

echo.
echo [Labbit] 완료되었습니다.
echo 결과 폴더: %CD%\ui-captures
exit /b 0
