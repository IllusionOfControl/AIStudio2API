#!/usr/bin/env bash
set -e

echo "Starting Prometheus and Grafana for AIStudio2API..."
docker compose -f docker-compose.metrics.yml up -d

echo ""
echo "======================================================="
echo "Prometheus: http://localhost:9090"
echo "Grafana:    http://localhost:3000 (admin / admin)"
echo "AIStudio2API Metrics: http://localhost:8080/metrics"
echo "======================================================="
