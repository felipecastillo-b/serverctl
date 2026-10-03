# serverctl

**Local-first, single-binary TUI for Linux server administration and monitoring over SSH.**

[![CI](https://github.com/felipecastillo-b/serverctl/actions/workflows/ci.yml/badge.svg)](https://github.com/felipecastillo-b/serverctl/actions/workflows/ci.yml)
[![Go 1.24+](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

## What is serverctl?

serverctl is a local-first, single-binary TUI for administering and monitoring Linux
servers over SSH. Every screen is fully operable by keyboard and mouse. It is
security-first by design: unprivileged and read-only by default, with every mutating
action whitelisted and gated behind a confirmation prompt.

## Status

> **Early development.** The interactive TUI shell arrives in milestone M1. The MVP
> (milestones M0–M4) covers the dashboard, processes, systemd services, and journal
> logs. See [ROADMAP.md](ROADMAP.md) for the full milestone plan.

## Planned features

- Dashboard overview: CPU, memory, load, uptime, kernel, hardware summary
- Process list with sort/filter and confirmed signal actions
- systemd services (start/stop/restart via D-Bus) and journal log viewer
- Storage: disks, partitions, usage
- Network interfaces and live traffic
- Listening ports and active connections
- Packages and available updates via pacman
- Users and login sessions
- SSH login attempt audit
- Hardware sensors via hwmon

## Requirements

- Linux — Arch Linux is the first target platform
- Go 1.24+ to build from source

## Install

Release binaries arrive with v1.0.0 (milestone M8). Today, build from source:

```bash
git clone https://github.com/felipecastillo-b/serverctl.git
cd serverctl
make build        # produces bin/serverctl
```

## Usage

```bash
bin/serverctl     # prints version placeholder — the TUI arrives in milestone M1
```

## Project docs

| Document | Contents |
|----------|----------|
| [ARCHITECTURE.md](ARCHITECTURE.md) | Layers, modules, data sources, security model, testing strategy |
| [ROADMAP.md](ROADMAP.md) | Milestones M0–M8, MVP and v1.0.0 cuts |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Workflow, commit conventions, quality and security gates |

## Development

```bash
make build   # build bin/serverctl
make test    # run all tests
make lint    # run golangci-lint
make help    # list every target
```

## License

[MIT](LICENSE) © Felipe Castillo
