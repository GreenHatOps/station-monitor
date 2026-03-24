# station-monitor

A lightweight, auto-detecting workstation monitoring sidecar for Prometheus. Pairs with [node_exporter](https://github.com/prometheus/node_exporter) and adds what it can't do: **NVMe/SATA SMART health**, **GPU detection**, and a **hardware inventory API**.

```
docker compose up -d
```

## Architecture

```
  HOST (any Linux box)                        Prometheus
  ┌──────────────────────────────┐            ┌──────────┐
  │  node-exporter        :9100  │◄── scrape ─┤          │
  │  (CPU, mem, disk, net, hwmon)│            │          │
  │                              │            │          │──▶ Grafana
  │  station-monitor      :9101  │◄── scrape ─┤          │
  │  (SMART, GPU, inventory)     │            └──────────┘
  └──────────────────────────────┘
```

**station-monitor** collects only what node_exporter can't:

| Collector | What it does | How |
|-----------|-------------|-----|
| SMART | NVMe + SATA drive health (temperature, wear, spare, errors) | `smartctl --scan` auto-detection |
| GPU | NVIDIA/AMD detection, temperature, utilization, memory | `nvidia-smi` / sysfs probing |
| Inventory | JSON hardware inventory via HTTP | `/inventory` endpoint |

## Quick Start

### Docker Compose (recommended)

```bash
git clone https://github.com/GreenHatOps/station-monitor.git
cd station-monitor
docker compose up -d
```

Verify:
```bash
curl http://localhost:9101/metrics    # SMART + GPU metrics
curl http://localhost:9101/inventory  # hardware inventory JSON
curl http://localhost:9100/metrics    # node_exporter metrics
```

### Standalone binary

```bash
go install github.com/GreenHatOps/station-monitor/cmd/station-monitor@latest
station-monitor  # listens on 127.0.0.1:9101
```

Note: `smartctl` must be installed on the host for SMART metrics.

## Metrics

All custom metrics use the `station_` prefix. node_exporter metrics use `node_`.

### SMART (per-drive, auto-detected)

```
station_smart_temperature_celsius{device, model, type}
station_smart_available_spare_ratio{...}          # 0-1, NVMe
station_smart_percentage_used_ratio{...}          # 0-1+, NVMe
station_smart_power_on_hours_total{...}
station_smart_power_cycles_total{...}
station_smart_media_errors_total{...}             # NVMe
station_smart_critical_warning{...}               # NVMe, 0=healthy
station_smart_reallocated_sectors_total{...}      # SATA
station_smart_pending_sectors_total{...}          # SATA
```

### GPU (auto-detected)

```
station_gpu_detected{vendor, device, model, type, driver}
station_gpu_temperature_celsius{vendor, device, model, type}
station_gpu_utilization_ratio{...}                # 0-1
station_gpu_memory_used_bytes{...}
```

### Inventory endpoint

```bash
$ curl -s localhost:9101/inventory | jq .
{
  "hostname": "my-workstation",
  "os": "Ubuntu 24.04 LTS",
  "kernel": "6.8.0-generic",
  "arch": "amd64",
  "cpu": {"model": "AMD Ryzen 9 7950X", "cores": 16, "threads": 32},
  "memory_bytes": 68719476736,
  "drives": [
    {"device": "/dev/nvme0", "model": "Samsung 990 Pro 2TB", "type": "nvme"},
    {"device": "/dev/sda", "model": "WDC WD40EZRZ", "type": "sat"}
  ],
  "gpu": [
    {"vendor": "nvidia", "model": "NVIDIA GeForce RTX 4090", "type": "dgpu", "driver_loaded": true}
  ],
  "station_monitor_version": "0.1.0"
}
```

## Configuration

| Environment Variable | Default | Description |
|---------------------|---------|-------------|
| `LISTEN_ADDR` | `127.0.0.1` | Bind address (`0.0.0.0` in docker-compose) |
| `LISTEN_PORT` | `9101` | HTTP port |
| `SMART_INTERVAL` | `5m` | SMART collection interval (minimum `1m`) |

## Grafana Dashboard

A pre-built dashboard is included in `deploy/grafana-dashboard.json`. For Kubernetes with kube-prometheus-stack:

```bash
kubectl create configmap station-monitor-dashboard \
  --from-file=station-monitor.json=deploy/grafana-dashboard.json \
  -n monitoring
kubectl label configmap station-monitor-dashboard grafana_dashboard=1 -n monitoring
```

The dashboard includes: system overview, thermal monitoring, NVMe/SMART health gauges, GPU status, CPU/memory usage, network traffic, disk I/O, filesystem usage, and load averages.

## Customization

Before deploying the scrape configs and alerting rules, update the placeholder values in `deploy/scrapeconfig.yaml`:

| Placeholder | Replace with | Example |
|-------------|-------------|---------|
| `<STATION_IP>` | IP address of your workstation | `192.168.1.50` |
| `<HOSTNAME>` | A label to identify your station in Prometheus/Grafana | `my-workstation` |

```bash
sed -i 's/<STATION_IP>/192.168.1.50/g; s/<HOSTNAME>/my-workstation/g' deploy/scrapeconfig.yaml
```

## Prometheus Scrape Config

### Kubernetes (kube-prometheus-stack)

```bash
kubectl apply -f deploy/scrapeconfig.yaml
```

### Static Prometheus config

```yaml
scrape_configs:
  - job_name: station-node-exporter
    static_configs:
      - targets: ['<host>:9100']
  - job_name: station-monitor
    static_configs:
      - targets: ['<host>:9101']
```

## Alerting Rules

Pre-built alerting rules for drive health are in `deploy/alerts.yaml`:

- **SmartDriveWearHigh** — NVMe life used > 80%
- **SmartDriveSpareLow** — NVMe spare < 10%
- **SmartMediaErrors** — any media/integrity errors
- **SmartCriticalWarning** — NVMe critical warning active
- **SmartDriveTemperatureHigh** — drive temperature > 70°C
- **SmartReallocatedSectors** — SATA reallocated sectors > 0

```bash
kubectl apply -f deploy/alerts.yaml
```

## GPU Support

| Vendor | Detection | Metrics | Requirement |
|--------|-----------|---------|-------------|
| NVIDIA | `/dev/nvidia*` + sysfs | temp, utilization, memory | `nvidia-container-toolkit` on host |
| AMD | `/dev/kfd` + sysfs | detection only | `rocm-smi` for full metrics |
| None | — | `station_gpu_detected` absent | — |

GPU metrics appear automatically when hardware is detected. No configuration needed.

## Development

```bash
# Run tests
go test ./... -v

# Build
go build -o station-monitor ./cmd/station-monitor

# Lint
golangci-lint run
```

## License

[MIT](LICENSE)
