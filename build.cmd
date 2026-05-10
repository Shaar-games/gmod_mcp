@echo off
setlocal

cd /d "%~dp0"

set "OUT_DIR=bin"
set "OUT_EXE=%OUT_DIR%\gmod_mcp.exe"

if /i "%~1"=="clean" (
    if exist "%OUT_EXE%" del /f /q "%OUT_EXE%"
)

set "GOOS=windows"
set "GOARCH=386"

if not exist "%OUT_DIR%" mkdir "%OUT_DIR%"

echo Building gmod_mcp.exe for %GOOS%/%GOARCH%...
go build -o "%OUT_EXE%" .
if errorlevel 1 (
    echo Build failed.
    exit /b 1
)

echo Build complete: %CD%\%OUT_EXE%
