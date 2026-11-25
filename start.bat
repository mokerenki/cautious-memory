@echo off
rem Switch the console to UTF-8 to ensure this file displays correctly.
chcp 65001 >nul 2>&1
rem cautiousmemory supervisor startup script (Windows)
rem
rem Usage:
rem   start.bat                  run in the foreground (Ctrl-C to stop)
rem   start.bat -addr :9000      pass additional arguments through to cautiousmemory unchanged
rem
rem Its only job is to start cautiousmemory.exe and decide whether to relaunch it based on its exit code.
rem
rem   0      user-requested stop     -> exit the loop
rem   75     program-requested restart     -> relaunch immediately (the page requested "one-click update" or "rollback")
rem   other  crash             -> relaunch after backoff (1->2->4â€¦up to 60 seconds)
rem
rem Downloads, SHA256 verification, and binary replacement are handled by cautiousmemory itself at startup
rem (selfupdate package). This script stays simple; see the notes at the top of start.sh.

setlocal enabledelayedexpansion
cd /d "%~dp0"

set "BIN=cautiousmemory.exe"
if not exist "%BIN%" (
	echo [cautiousmemory] executable not found %BIN% 1>&2
	exit /b 1
)

set "RESTART_CODE=75"
set "MAX_DELAY=60"
set /a delay=1

:loop
"%BIN%" %*
set "code=!ERRORLEVEL!"

if "!code!"=="0" (
	echo [cautiousmemory] exited normally
	exit /b 0
)

if "!code!"=="%RESTART_CODE%" (
	rem Update/rollback is ready: relaunch cautiousmemory to complete the replacement at startup.
	echo [cautiousmemory] restart requested (applying the new version)â€¦
	set /a delay=1
	goto loop
)

echo [cautiousmemory] exited unexpectedly ^(code=!code!^); restarting in !delay!s 1>&2
rem timeout fails in a redirected console, so use ping as a fallback (an N-second delay requires N+1 pings).
set /a pings=!delay!+1
ping -n !pings! 127.0.0.1 >nul 2>&1
set /a delay=!delay!*2
if !delay! gtr %MAX_DELAY% set /a delay=%MAX_DELAY%
goto loop

