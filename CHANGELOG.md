# Changelog

## v0.1.0 — 2026-03-15

Initial release of Mikrotik Traffic Monitor.

### Features

- Real-time web dashboard for monitoring network interface traffic on Mikrotik routers via SNMP
- Multi-device support with automatic interface discovery
- SNMPv2c and SNMPv3 support for mixed-device environments
- Configurable SNMPv3 auth and privacy protocols (default SHA1/AES)
- YAML-based configuration with multi-interface support
- Clean, dark-themed UI with per-device interface selector dropdown
- Skip unreachable devices at startup instead of crashing
- Dockerfile for containerized deployment
- Comprehensive README with setup, configuration, and deployment instructions
