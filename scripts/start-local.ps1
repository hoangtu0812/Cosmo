$ErrorActionPreference = 'Stop'

$projectRoot = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $projectRoot

if (-not (Test-Path -LiteralPath '.env')) {
    Copy-Item -LiteralPath '.env.example' -Destination '.env'
    Write-Host 'Đã tạo .env từ .env.example. Hãy cấu hình secret trước khi tiếp tục.'
    exit 1
}

docker compose up -d --build
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}

docker compose ps
Write-Host ''
Write-Host 'Cosmo UI:  http://localhost:3100'
Write-Host 'Cosmo API: http://localhost:8080/api/health'
