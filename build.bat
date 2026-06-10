@echo off
setlocal DisableDelayedExpansion

REM Build reds and its SWIM/Solace target reader on Windows, then run the GUI.
REM
REM Usage: build.bat [options]
REM
REM Options:
REM   --check       Run gofmt check before building
REM   --test        Run go test ./... after building
REM   --all         Run checks and tests, then build/run
REM   --build-only  Build build\reds.exe and the SMES jar, but do not run the GUI
REM   --no-run      Alias for --build-only
REM   --help        Show this help message
REM
REM Prerequisites:
REM   - Go installed and in PATH, matching go.mod
REM   - JDK 21 installed and in PATH
REM   - Maven installed and in PATH
REM   - MSYS2 UCRT64 toolchain with GCC, pkgconf, and GLFW:
REM       C:\msys64\usr\bin\pacman.exe -S --needed --noconfirm ^
REM         base-devel ^
REM         mingw-w64-ucrt-x86_64-gcc ^
REM         mingw-w64-ucrt-x86_64-pkgconf ^
REM         mingw-w64-ucrt-x86_64-glfw

set "DO_CHECK=0"
set "DO_TEST=0"
set "DO_RUN=1"

cd /d "%~dp0"

:parse_args
if "%~1"=="" goto done_parsing
if "%~1"=="--check" set "DO_CHECK=1"
if "%~1"=="--test" set "DO_TEST=1"
if "%~1"=="--all" (
    set "DO_CHECK=1"
    set "DO_TEST=1"
)
if "%~1"=="--build-only" set "DO_RUN=0"
if "%~1"=="--no-run" set "DO_RUN=0"
if "%~1"=="--help" goto help
shift
goto parse_args

:help
echo Build script for REDS ^(Windows^)
echo.
echo Usage: build.bat [options]
echo.
echo Options:
echo   --check       Run gofmt check before building
echo   --test        Run go test ./... after building
echo   --all         Run checks and tests, then build/run
echo   --build-only  Build build\reds.exe and the SMES jar, but do not run the GUI
echo   --no-run      Alias for --build-only
echo   --help        Show this help message
echo.
exit /b 0

:done_parsing

REM Load local environment variables, matching build.sh behavior.
if exist ".env" (
    echo [env] Loading .env
    for /f "usebackq eol=# tokens=1,* delims==" %%A in (".env") do (
        if not "%%A"=="" set "%%A=%%B"
    )
)

if not defined WS_PORT set "WS_PORT=8080"
set "CGO_ENABLED=1"

call :configure_msys2
if errorlevel 1 exit /b 1

call :check_tools
if errorlevel 1 exit /b 1

if "%DO_CHECK%"=="1" (
    call :run_checks
    if errorlevel 1 exit /b 1
)

if not exist "build" mkdir build

REM Build order mirrors build.sh semantically: frontend + SMES reader before run.
echo [build] Building reds ^(Go frontend^)...
go build -v -o build\reds.exe .\cmd\reds
if errorlevel 1 exit /b 1

echo [build] Building SMES reader...
mvn -B -f swim\smes\pom.xml -DskipTests package
if errorlevel 1 exit /b 1

if "%DO_TEST%"=="1" (
    echo [test] Running Go tests...
    go test -v ./...
    if errorlevel 1 exit /b 1
)

if "%DO_RUN%"=="0" (
    echo [done] Build complete: build\reds.exe
    exit /b 0
)

call :kill_stale_listener
if errorlevel 1 exit /b 1

echo [run] Launching reds...
build\reds.exe
exit /b %ERRORLEVEL%

:configure_msys2
REM Prefer the same UCRT64 MSYS2 layout used by CI, but respect user overrides.
if not defined MSYS2_UCRT64_BIN (
    if exist "C:\msys64\ucrt64\bin\gcc.exe" set "MSYS2_UCRT64_BIN=C:\msys64\ucrt64\bin"
)
if not defined MSYS2_UCRT64_BIN (
    if exist "C:\tools\msys64\ucrt64\bin\gcc.exe" set "MSYS2_UCRT64_BIN=C:\tools\msys64\ucrt64\bin"
)

if defined MSYS2_UCRT64_BIN (
    set "PATH=%MSYS2_UCRT64_BIN%;%PATH%"
    if not defined CC set "CC=%MSYS2_UCRT64_BIN%\gcc.exe"
    if not defined CXX set "CXX=%MSYS2_UCRT64_BIN%\g++.exe"
    if not defined PKG_CONFIG set "PKG_CONFIG=%MSYS2_UCRT64_BIN%\pkg-config.exe"
)

if not defined CC set "CC=gcc"
if not defined CXX set "CXX=g++"
if not defined PKG_CONFIG set "PKG_CONFIG=pkg-config"

"%CC%" --version >nul 2>nul
if errorlevel 1 (
    echo Error: GCC was not found. Install MSYS2 UCRT64 GCC and make sure it is on PATH.
    echo.
    echo Suggested commands from an elevated PowerShell:
    echo   choco install msys2 -y --no-progress
    echo   C:\msys64\usr\bin\pacman.exe -Syu --noconfirm
    echo   C:\msys64\usr\bin\pacman.exe -S --needed --noconfirm base-devel mingw-w64-ucrt-x86_64-gcc mingw-w64-ucrt-x86_64-pkgconf mingw-w64-ucrt-x86_64-glfw
    exit /b 1
)

"%PKG_CONFIG%" --modversion glfw3 >nul 2>nul
if errorlevel 1 (
    echo Error: pkg-config could not find glfw3.
    echo Install mingw-w64-ucrt-x86_64-glfw and make sure UCRT64 bin is on PATH.
    echo Current PKG_CONFIG=%PKG_CONFIG%
    exit /b 1
)

exit /b 0

:check_tools
where go >nul 2>nul
if errorlevel 1 (
    echo Error: Go was not found in PATH.
    exit /b 1
)
where java >nul 2>nul
if errorlevel 1 (
    echo Error: Java was not found in PATH. Install JDK 21.
    exit /b 1
)
where mvn >nul 2>nul
if errorlevel 1 (
    echo Error: Maven was not found in PATH.
    exit /b 1
)

echo [tools] Go:
go version
echo [tools] Java:
java -version
echo [tools] Maven:
mvn -version
echo [tools] GCC:
"%CC%" --version
echo [tools] GLFW:
"%PKG_CONFIG%" --modversion glfw3
exit /b 0

:run_checks
echo [check] Checking gofmt...
for /f "delims=" %%F in ('gofmt -l . 2^>^&1') do (
    echo The following Go files need gofmt:
    gofmt -l .
    exit /b 1
)
echo [check] gofmt: OK
exit /b 0

:kill_stale_listener
echo [run] Checking for stale listener on :%WS_PORT%...
for /f "tokens=5" %%P in ('netstat -ano -p tcp ^| findstr /R /C:":%WS_PORT% .*LISTENING"') do (
    echo [run] Killing stale listener PID %%P on :%WS_PORT%
    taskkill /PID %%P /F >nul 2>nul
)
exit /b 0
