# Mikrotik Traffic Monitor

Real-time web dashboard for monitoring network interface traffic on Mikrotik routers via SNMPv3. Supports multiple interfaces with a clean, dark-themed UI.

## Features

- **SNMPv3 polling** — reads 64-bit counters (`ifHCInOctets` / `ifHCOutOctets`) for accurate high-speed interface stats
- **Multi-interface monitoring** — configure any number of router interfaces, each shown in a tabbed view
- **Real-time updates** — Server-Sent Events push data to the browser with no polling or page reloads
- **Rolling time-series chart** — powered by uPlot, with configurable history depth
- **Peak and average indicators** — per-interface throughput statistics
- **Dark theme** — single-page UI designed for NOC screens and dashboards
- **Single binary** — Go binary with the frontend embedded, no external dependencies at runtime

## Requirements

- **Go 1.24+** (build only)
- **Mikrotik RouterOS 7.x** with SNMPv3 enabled
- An SNMPv3 user configured on the router with `authPriv` security level (SHA + AES)

## Quick Start

```bash
# 1. Clone the repository
git clone https://github.com/palmar/mikrotik-traffic-monitor.git
cd mikrotik-traffic-monitor

# 2. Copy and edit the configuration
cp config.yaml.example config.yaml
# Edit config.yaml with your router details (see Configuration below)

# 3. Build
go build -o traffic-monitor .

# 4. Run
./traffic-monitor -config config.yaml
```

The dashboard will be available at `http://localhost:8080` (or whatever `listen_addr` you configured).

## Configuration

All settings live in a single YAML file. Copy `config.yaml.example` to `config.yaml` and adjust:

```yaml
# SNMP connection to the Mikrotik router
router:
  host: "192.168.88.1"       # Router IP or hostname
  port: 161                   # SNMP port (default: 161)
  username: "monitor"         # SNMPv3 username
  auth_pass: "your-auth-password"  # SNMPv3 authentication passphrase (SHA)
  priv_pass: "your-priv-password"  # SNMPv3 privacy passphrase (AES)

# Interfaces to monitor — use Mikrotik interface names
interfaces:
  - ether1
  - sfp-sfpplus1

# Polling frequency (Go duration string: "1s", "5s", "10s", etc.)
# Lower values give more granular data but increase SNMP load on the router.
poll_interval: "5s"

# Number of data points kept per interface.
# At 5s polling, 240 samples = 20 minutes of history.
ring_buffer_size: 240

# HTTP listen address for the web dashboard.
listen_addr: ":8080"
```

### Configuration Reference

| Field | Required | Default | Description |
|---|---|---|---|
| `router.host` | Yes | — | Router IP address or hostname |
| `router.port` | No | `161` | SNMP UDP port |
| `router.username` | Yes | — | SNMPv3 username |
| `router.auth_pass` | No | — | SNMPv3 auth passphrase (SHA) |
| `router.priv_pass` | No | — | SNMPv3 privacy passphrase (AES) |
| `interfaces` | Yes | — | List of Mikrotik interface names to monitor |
| `poll_interval` | No | `5s` | How often to read SNMP counters |
| `ring_buffer_size` | No | `240` | Data points to retain per interface |
| `listen_addr` | No | `:8080` | `host:port` the HTTP server binds to |

### Mikrotik SNMPv3 Setup

On your Mikrotik router, enable SNMP and create an SNMPv3 user:

```
/snmp set enabled=yes
/snmp community set [ find default=yes ] disabled=yes
/snmp set trap-community="" trap-version=3
```

Create an SNMPv3 user with authentication and privacy:

```
/snmp community add name=monitor security=private \
    authentication-protocol=SHA1 authentication-password=your-auth-password \
    encryption-protocol=AES encryption-password=your-priv-password \
    read-access=yes write-access=no
```

### Finding Interface Names

To list available interfaces on your Mikrotik:

```
/interface print
```

Use the exact name from the "NAME" column (e.g., `ether1`, `sfp-sfpplus1`, `bridge1`).

## Docker

Build and run with Docker:

```bash
docker build -t traffic-monitor .
docker run -d \
  --name traffic-monitor \
  -v /path/to/config.yaml:/etc/traffic-monitor/config.yaml \
  -p 8080:8080 \
  traffic-monitor
```

## Deployment

### Running as a systemd Service

Create `/etc/systemd/system/traffic-monitor.service`:

```ini
[Unit]
Description=Mikrotik Traffic Monitor
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/traffic-monitor -config /etc/traffic-monitor/config.yaml
Restart=on-failure
RestartSec=5
User=traffic-monitor
Group=traffic-monitor

[Install]
WantedBy=multi-user.target
```

Then:

```bash
# Copy the binary
sudo cp traffic-monitor /usr/local/bin/

# Create config directory and copy config
sudo mkdir -p /etc/traffic-monitor
sudo cp config.yaml /etc/traffic-monitor/config.yaml

# Create a dedicated user (no login shell, no home directory)
sudo useradd -r -s /usr/sbin/nologin traffic-monitor

# Enable and start
sudo systemctl daemon-reload
sudo systemctl enable traffic-monitor
sudo systemctl start traffic-monitor
```

### Recommended Setup

- **Reverse proxy**: Place behind a reverse proxy (Caddy, nginx, haproxy) for TLS termination and access control.
- **DNS**: Point a hostname at the server running the dashboard (e.g., `traffic.example.com`).
- **Firewall**: The dashboard does not include authentication. Restrict access by IP or place behind a VPN if exposed externally.

### Polling Frequency and Router Load

The default 5-second poll interval is lightweight — each poll is a single SNMP GET for two OIDs per interface. For routers handling heavy workloads or with limited CPU, you can safely increase this to `10s` or `30s`. The ring buffer size should be adjusted proportionally to maintain the desired history window:

| Poll Interval | Buffer Size | History |
|---|---|---|
| `1s` | `600` | 10 min |
| `5s` | `240` | 20 min |
| `10s` | `180` | 30 min |
| `30s` | `120` | 60 min |

## Project Structure

```
├── main.go                  # Entry point — wires config, poller, and HTTP server
├── config.yaml.example      # Example configuration
├── Dockerfile               # Multi-stage Docker build
├── internal/
│   ├── config/              # YAML config loading and validation
│   ├── ringbuf/             # Fixed-size ring buffer for time-series data
│   ├── server/              # HTTP server, SSE broadcasting, embedded static files
│   │   └── static/          # Frontend (HTML, CSS, JS with uPlot)
│   └── snmp/                # SNMPv3 poller
└── web/                     # (reserved)
```

## License

See [LICENSE](LICENSE) for details.
