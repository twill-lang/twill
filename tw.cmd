@echo off
rem tw - the pretty twill CLI, written in twill and run on a real twill binary.
rem %~dp0 is this script's own directory, so the launcher works from anywhere.
setlocal
set "here=%~dp0"
set "main=%here%src\cli\main.tw"

rem Prefer twill on PATH, then a locally built twill.exe, else `go run`.
where twill >nul 2>nul
if %errorlevel%==0 (
  twill run "%main%" %*
) else if exist "%here%twill.exe" (
  "%here%twill.exe" run "%main%" %*
) else (
  go run "%here%cmd\twill" run "%main%" %*
)
