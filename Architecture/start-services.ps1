# Start-Services.ps1
# Starts core Go microservices locally in separate PowerShell windows.
# Core: Auth, User, Post, Feed

Write-Host "Starting Postbook Core Services Locally..." -ForegroundColor Cyan

# 1. Define Common Environment Variables
$Env:POSTGRES_DSN = "postgres://postgres:postgres@localhost:5432/identity_db?sslmode=disable"
$Env:REDIS_ADDR = "localhost:6379"
$Env:KAFKA_BROKERS = "localhost:9092"
$Env:SCYLLA_HOSTS = "localhost"
$Env:MINIO_ENDPOINT = "localhost:9000"
$Env:MINIO_ACCESS_KEY = "minio"
$Env:MINIO_SECRET_KEY = "local_dev_minio_password_change_me"
$Env:MINIO_BUCKET = "media"
$Env:OPENSEARCH_URL = "http://localhost:9200"
$Env:JWT_SECRET = "local_dev_jwt_change_me"
$Env:INTERNAL_SERVICE_KEY = "local_dev_internal_service_key_change_me"

# Function to start a service in a new window
function Start-ServiceWindow {
    param (
        [string]$ServiceName,
        [string]$ServicePath,
        [string]$Port
    )

    Write-Host "Launching $ServiceName on port $Port..." -ForegroundColor Green
    
    $Command = @"
        `$Host.UI.RawUI.WindowTitle = '$ServiceName (Port $Port)';
        Write-Host 'Starting $ServiceName...';
        `$Env:HTTP_PORT = '$Port';
        `$Env:POSTGRES_DSN = '$Env:POSTGRES_DSN';
        `$Env:REDIS_ADDR = '$Env:REDIS_ADDR';
        `$Env:KAFKA_BROKERS = '$Env:KAFKA_BROKERS';
        `$Env:SCYLLA_HOSTS = '$Env:SCYLLA_HOSTS';
        `$Env:MINIO_ENDPOINT = '$Env:MINIO_ENDPOINT';
        `$Env:MINIO_ACCESS_KEY = '$Env:MINIO_ACCESS_KEY';
        `$Env:MINIO_SECRET_KEY = '$Env:MINIO_SECRET_KEY';
        `$Env:MINIO_BUCKET = '$Env:MINIO_BUCKET';
        `$Env:OPENSEARCH_URL = '$Env:OPENSEARCH_URL';
        `$Env:GOWORK = 'off';
        
        Set-Location '$ServicePath';
        Write-Host 'Ensuring dependencies...';
        go mod tidy;
        go run cmd/server/main.go;
        Read-Host 'Service stopped. Press Enter to close...';
"@

    Start-Process powershell -ArgumentList "-NoExit", "-Command", "$Command"
}

# 2. Check for Infrastructure Ports
$PortsToCheck = @(5432, 6379, 9092)
foreach ($P in $PortsToCheck) {
    if (-not (Test-NetConnection -ComputerName localhost -Port $P -WarningAction SilentlyContinue).TcpTestSucceeded) {
        Write-Warning "Port $P is not open. Is your Docker infrastructure running?"
    }
}

# 3. Launch Core Services
$ServicesRoot = ".\services"

Start-ServiceWindow -ServiceName "Auth Service" -ServicePath "$ServicesRoot\auth-service" -Port "8081"
Start-ServiceWindow -ServiceName "User Service" -ServicePath "$ServicesRoot\user-service" -Port "8082"
Start-ServiceWindow -ServiceName "Post Service" -ServicePath "$ServicesRoot\post-service" -Port "8084"
Start-ServiceWindow -ServiceName "Feed Service" -ServicePath "$ServicesRoot\feed-service" -Port "8086"

Write-Host "Services launched. Check individual windows for logs." -ForegroundColor Cyan
