param(
    [Parameter(Position = 0)]
    [ValidateSet("infra", "food")]
    [string]$Target = "food",

    [Parameter(Position = 1)]
    [ValidateSet("up", "down", "status", "logs")]
    [string]$Action = "up",

    [Parameter(Position = 2)]
    [ValidateSet("core", "media", "full", "web")]
    [string]$Mode = "web"
)

$ErrorActionPreference = "Stop"
if (-not $env:COMPOSE_PARALLEL_LIMIT) {
    $env:COMPOSE_PARALLEL_LIMIT = "1"
}

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    throw "Docker is not installed or is not available on PATH. Install/start Docker Desktop, then run this command again."
}

$dockerDir = $PSScriptRoot
$infraFile = Join-Path $dockerDir "docker-compose.infra.yml"
$foodFile = Join-Path $dockerDir "docker-compose.food.yml"

function Invoke-Compose([string[]]$Arguments) {
    & docker compose @Arguments
    if ($LASTEXITCODE -ne 0) { throw "docker compose failed with exit code $LASTEXITCODE" }
}

function Test-Network {
    & docker network inspect atpost_dev *> $null
    return $LASTEXITCODE -eq 0
}

if ($Target -eq "infra") {
    $args = @("-p", "atpost_infra", "-f", $infraFile)
    switch ($Action) {
        "up"     { Invoke-Compose ($args + @("up", "-d")) }
        "down"   { Invoke-Compose ($args + @("down")) }
        "status" { Invoke-Compose ($args + @("ps")) }
        "logs"   { Invoke-Compose ($args + @("logs", "-f", "--tail", "150")) }
    }
    exit 0
}

if ($Action -eq "up" -and -not (Test-Network)) {
    throw "Shared infrastructure is not running. Start it first with: .\focus.ps1 infra up"
}

$profiles = @()
switch ($Mode) {
    "media" { $profiles = @("--profile", "media") }
    "full"  { $profiles = @("--profile", "media", "--profile", "payments") }
    "web"   { $profiles = @("--profile", "media", "--profile", "payments", "--profile", "web") }
}

$foodArgs = @("-p", "atpost_food", "-f", $foodFile, "--profile", "media", "--profile", "payments", "--profile", "web")
$selectedArgs = @("-p", "atpost_food", "-f", $foodFile) + $profiles

switch ($Action) {
    "up" {
        $fullStack = & docker ps -q --filter "label=com.docker.compose.project=atpost_stack"
        if ($fullStack) {
            Write-Warning "The full atpost_stack is running and may already own ports 8080/8081/8113. Stop it before starting the focused stack."
        }
        Invoke-Compose ($selectedArgs + @("up", "-d", "--build"))
        Write-Host ""
        Write-Host "FiGo & Pulse $Mode stack is ready:" -ForegroundColor Green
        Write-Host "  Web (Food):   http://localhost:3011/figo" 
        Write-Host "  Web (Dating): http://localhost:3013/match"
        Write-Host "  Gateway:      http://localhost:8080/healthz"
        Write-Host "  Food:         http://localhost:8113/healthz"
        Write-Host "  Dating:       http://localhost:8112/healthz"
    }
    "down"   { Invoke-Compose ($foodArgs + @("down")) }
    "status" { Invoke-Compose ($foodArgs + @("ps")) }
    "logs"   { Invoke-Compose ($foodArgs + @("logs", "-f", "--tail", "150")) }
}
