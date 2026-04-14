$ErrorActionPreference = "Continue" # Change to continue to see multiple build results
$PWD_PATH = Get-Location

# 1. Clean old builds
Write-Host "Cleaning old builds..." -ForegroundColor Cyan
if (Test-Path "vad_test.exe") { Remove-Item "vad_test.exe" -Force }
if (Test-Path "rtp-agent.exe") { Remove-Item "rtp-agent.exe" -Force }

# 2. Set strict environment
Write-Host "Setting environment (AMD64)..." -ForegroundColor Cyan
$env:GOARCH = "amd64"
$env:GOOS = "windows"
$env:CGO_ENABLED = "1"
if (-not $env:VAD_TYPE) { $env:VAD_TYPE = "silero" }
if (-not $env:VAD_THRESHOLD) { $env:VAD_THRESHOLD = "0.5" }

# Ensure DLLs and Headers are in the current path
$env:PATH = "$PWD_PATH;$env:PATH"
# Point CGO to our local include directory for Opus/PortAudio stubs
$env:CGO_CFLAGS = "-I$PWD_PATH/include -I$PWD_PATH/include/opus"
$env:CGO_LDFLAGS = "-L$PWD_PATH -L$PWD_PATH/bin"

# 3. Build VAD Test Tool
Write-Host "Rebuilding VAD Test Tool (vad_test.exe)..." -ForegroundColor Cyan
go build -ldflags="-s -w" -o vad_test.exe ./cmd/vad_test/main.go

# 4. Build Main Agent
Write-Host "Rebuilding Main Agent (rtp-agent.exe)..." -ForegroundColor Cyan
go build -ldflags="-s -w" -o rtp-agent.exe ./cmd/main.go

# 5. Final check
if (Test-Path "rtp-agent.exe") {
    Write-Host "Main Agent Build Successful!" -ForegroundColor Green
}

if (Test-Path "vad_test.exe") {
    Write-Host "VAD Test Tool Launching..." -ForegroundColor Yellow
    .\vad_test.exe
} else {
    Write-Host "VAD Test Tool Build failed." -ForegroundColor Red
}
