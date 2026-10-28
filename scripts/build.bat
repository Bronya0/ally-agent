@echo off
setlocal
bash "%~dp0build.sh" %*
exit /b %errorlevel%
