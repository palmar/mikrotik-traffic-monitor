# Mikrotik WAN Traffic Monitor

Real-time web dashboard for monitoring inbound/outbound traffic on a Mikrotik router via SNMPv3. Supports monitoring multiple interfaces simultaneously.

## Features

- Polls SNMPv3 64-bit counters (ifHCInOctets/ifHCOutOctets) at a configurable interval
- Multiple interface monitoring with tab-based UI switching
- Server-Sent Events for real-time browser updates
- Rolling time-series chart (uPlot) with configurable history depth
- Dark theme, single-page UI
- Peak/average throughput indicators
- Single static Go binary with embedded frontend

## Configuration

All configuration is managed via a YAML file. Copy `config.yaml.example` to `config.yaml` and edit:

```bash
cp config.yaml.example config.yaml
```

See `config.yaml.example` for all available options including router connection, interface list, polling frequency, and buffer size.

## Build & Run

```bash
go build -o traffic-monitor .
./traffic-monitor -config config.yaml
```

## Docker

```bash
docker build -t traffic-monitor .
docker run -v /path/to/config.yaml:/etc/traffic-monitor/config.yaml -p 8080:8080 traffic-monitor
```
