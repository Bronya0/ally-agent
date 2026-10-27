@echo off
rem Build Ally Native for Windows. Run from the mygo directory:
rem   build.bat   -> dist\ally-native.exe
rem Plain `go build` only: no packaging, no signing.
setlocal
cd /d "%~dp0"
if not "%~1"=="" (
  echo Usage: build.bat 1>&2
  exit /b 2
)
if not exist dist mkdir dist
set GOMAXPROCS=2
set GOGC=30
set CGO_ENABLED=0
go build -p 1 -trimpath -tags mygo_noinspector -ldflags "-H windowsgui -s -w" -o "dist\ally-native.exe" .
if errorlevel 1 exit /b 1
echo Built: dist\ally-native.exe
