# AI Station Hardware Inventory

**Hostname:** g-console
**OS:** Pop!_OS 24.04 LTS (kernel 6.18.7-76061807-generic)

## CPU

| Field | Value |
|-------|-------|
| Model | AMD Ryzen 9 8945HX with Radeon Graphics |
| Architecture | x86_64 |
| Cores | 16 (32 threads) |
| Max Clock | 5462 MHz |
| Socket | 1 |

## Memory

| Field | Value |
|-------|-------|
| Total | 92 GiB (~96 GB) |
| Swap | 19 GiB (zram) |

## Storage

| Device | Size | Model | Interface |
|--------|------|-------|-----------|
| nvme0n1 | 1.8 TB | Samsung SSD 990 EVO Plus 2TB | NVMe |

## GPU

| Field | Value |
|-------|-------|
| Integrated | AMD Raphael (iGPU, rev d8) |
| Discrete | **Not installed** |

## Motherboard

| Field | Value |
|-------|-------|
| Manufacturer | Meigao Innovation Technology (Shen Zhen) Co., Ltd |
| Model | DRFXL |
| Version | 1.0 |
| BIOS | AMI 1.14 (2025-11-13) |

## Network

| Interface | Status | Address |
|-----------|--------|---------|
| enp3s0 (Ethernet) | UP | 192.168.1.90/24 |
| wlp2s0 (Wi-Fi) | DOWN | — |

## Wireless / Bluetooth

- Intel AX210 Bluetooth (Wi-Fi adapter present but down)

## Case

- Fractal Design Mood

## Notes

- No discrete GPU installed yet — only the Ryzen 9 8945HX integrated Raphael iGPU
- `lm-sensors` not installed — no temperature readings available
- `nvme-cli` not installed — no detailed NVMe health/SMART data
- Motherboard is a mini-PC style board (Meigao/MINISFORUM DRFXL)
