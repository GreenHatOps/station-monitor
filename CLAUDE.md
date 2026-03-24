# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**station-monitor** — a lightweight, auto-detecting workstation monitoring sidecar for Prometheus. Pairs with `prom/node-exporter` (which handles CPU, memory, disk, network, hwmon) and adds what node_exporter can't: NVMe/SATA SMART health, GPU detection, and a hardware inventory API.

## Build & Test

```bash
# Build locally (requires Go 1.22+)
go build -o station-monitor ./cmd/station-monitor

# Run tests
go test ./...

# Build Docker image
docker build -t station-monitor .

# Run full stack (node_exporter + station-monitor)
docker compose up -d

# Verify metrics
curl http://localhost:9101/metrics
curl http://localhost:9101/inventory
curl http://localhost:9100/metrics  # node_exporter
```

## Architecture

Sidecar pattern: two containers, two scrape targets.

- **node_exporter** (:9100) — commodity OS metrics (CPU, mem, disk I/O, network, hwmon temps/fans)
- **station-monitor** (:9101) — SMART health, GPU detection, `/inventory` JSON endpoint

Collectors use a background goroutine pattern:
- SMART: `smartctl --scan` + `smartctl -a` every `SMART_INTERVAL` (default 5m, min 1m)
- GPU: `nvidia-smi` / sysfs probe every 15s
- Scrape-time reads from cache only (fast, no I/O)

All external commands go through `CommandRunner` interface for testability.

## Metric Prefix

All custom metrics use `station_` prefix. node_exporter metrics use `node_` prefix.

## Environment Variables

| Var | Default | Description |
|-----|---------|-------------|
| `LISTEN_ADDR` | `127.0.0.1` | Bind address (docker-compose sets `0.0.0.0`) |
| `LISTEN_PORT` | `9101` | HTTP port |
| `SMART_INTERVAL` | `5m` | SMART collection interval (minimum 1m) |

## Deploy to k3s

```bash
kubectl apply -f deploy/scrapeconfig.yaml  # Prometheus targets
kubectl apply -f deploy/alerts.yaml        # Alerting rules
```
