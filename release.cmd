@echo off
setlocal

cd /d "%~dp0"

if "%~1"=="" (
    echo Usage: release.cmd ^<tag^>
    echo Example: release.cmd v1.3.0
    exit /b 2
)

set "TAG=%~1"
set "ASSET=bin\gmod_mcp.exe"
set "REPO=Shaar-games/gmod_mcp"
set "NOTES=Full Changelog: https://github.com/%REPO%/commits/%TAG%"

where git >nul 2>nul
if errorlevel 1 (
    echo git was not found in PATH.
    exit /b 1
)

where gh >nul 2>nul
if errorlevel 1 (
    echo GitHub CLI ^(gh^) was not found in PATH.
    echo Install it from https://cli.github.com/ and run: gh auth login
    exit /b 1
)

git rev-parse --is-inside-work-tree >nul 2>nul
if errorlevel 1 (
    echo This script must be run from inside a git repository.
    exit /b 1
)

for /f %%i in ('git status --porcelain --untracked-files=no') do (
    echo Refusing to release with tracked working tree changes.
    echo Commit or revert tracked changes first.
    git status --short
    exit /b 1
)

git rev-parse --verify "refs/tags/%TAG%" >nul 2>nul
if not errorlevel 1 (
    echo Tag "%TAG%" already exists locally.
    exit /b 1
)

git ls-remote --exit-code --tags origin "refs/tags/%TAG%" >nul 2>nul
if not errorlevel 1 (
    echo Tag "%TAG%" already exists on origin.
    exit /b 1
)

call "%~dp0build.cmd" clean
if errorlevel 1 (
    exit /b 1
)

if not exist "%ASSET%" (
    echo Missing release asset: %ASSET%
    exit /b 1
)

git tag "%TAG%"
if errorlevel 1 (
    echo Failed to create tag "%TAG%".
    exit /b 1
)

git push origin "%TAG%"
if errorlevel 1 (
    echo Failed to push tag "%TAG%".
    exit /b 1
)

gh release create "%TAG%" "%ASSET%" --repo "%REPO%" --title "%TAG%" --notes "%NOTES%"
if errorlevel 1 (
    echo Failed to create GitHub release.
    exit /b 1
)

echo Release created: https://github.com/%REPO%/releases/tag/%TAG%
