@echo off
echo Starting Prometheus and Grafana for AIStudio2API...
docker compose -f docker-compose.metrics.yml up -d
if %ERRORLEVEL% equ 0 (
    echo.
    echo =======================================================
    echo Prometheus: http://localhost:9090
    echo Grafana:    http://localhost:3000 (admin / admin)
    echo AIStudio2API Metrics: http://localhost:8080/metrics
    echo =======================================================
) else (
    echo Failed to start monitoring containers. Please check if Docker is running.
)
