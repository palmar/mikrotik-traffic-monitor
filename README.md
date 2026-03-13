# Mikrotik WAN Traffic Monitor

Real-time web dashboard for monitoring inbound/outbound traffic on a Mikrotik router's WAN interface via SNMPv3.

## Features

- Polls SNMPv3 64-bit counters (ifHCInOctets/ifHCOutOctets) every 5 seconds
- Server-Sent Events for real-time browser updates
- Rolling time-series chart (uPlot) with ~20 minutes of history
- Dark theme, single-page UI
- Peak/average throughput indicators
- Single static Go binary with embedded frontend

## Configuration

All configuration via environment variables (see `.env.example`):

| Variable | Required | Default | Description |
|---|---|---|---|
| `SNMP_HOST` | Yes | — | Mikrotik router address |
| `SNMP_PORT` | No | 161 | SNMP port |
| `SNMP_USERNAME` | Yes | — | SNMPv3 username |
| `SNMP_AUTH_PASS` | Yes | — | SNMPv3 auth passphrase (SHA-256) |
| `SNMP_PRIV_PASS` | Yes | — | SNMPv3 privacy passphrase (AES-128) |
| `SNMP_INTERFACE` | No | sfp12_wan | Interface name to monitor |
| `POLL_INTERVAL_S` | No | 5 | Poll interval in seconds |
| `LISTEN_ADDR` | No | :8080 | HTTP listen address |
| `RING_BUFFER_SIZE` | No | 240 | Number of samples to keep in memory |

## Build & Run

```bash
go build -o traffic-monitor .
SNMP_HOST=... SNMP_USERNAME=... SNMP_AUTH_PASS=... SNMP_PRIV_PASS=... ./traffic-monitor
```

## Docker

```bash
docker build -t traffic-monitor .
docker run -e SNMP_HOST=... -e SNMP_USERNAME=... -e SNMP_AUTH_PASS=... -e SNMP_PRIV_PASS=... -p 8080:8080 traffic-monitor
```
