@echo off
setlocal
cd /d "%~dp0.."
where py >nul 2>&1
if not errorlevel 1 (
    py -3 "scripts/build.py" dev
) else (
    python "scripts/build.py" dev
)
set "build_status=%errorlevel%"
echo.
if not "%build_status%"=="0" echo Build failed. See the error above.
if "%build_status%"=="0" echo Built: dist/ally-native.exe
pause
exit /b %build_status%
