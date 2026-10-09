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
- Installed package count via pacman's local database (other distros planned)
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

## Privileges

serverctl runs unprivileged. It never escalates — no sudo, no setuid, no password
prompts. Reads need no special rights, and mutations ask systemd over the system
D-Bus, where PolicyKit decides whether the caller may run them.

- **Reads** work as a regular user. Journal entries are richer when your user is
  in the `systemd-journal` group, but nothing requires it.
- **Mutations** (start/stop/restart) are polkit-gated, whitelisted, confirmed in
  a modal, and audited locally — there is no generic command runner.

From an active local session most distros let administrative users manage units
without a password. An SSH session is not "active local" to polkit, so managing
units over SSH typically fails with an interactive-authentication error —
serverctl deliberately ships no polkit authentication agent. Grant the right
explicitly instead, e.g. `/etc/polkit-1/rules.d/49-serverctl.rules`:

```javascript
// Allow members of the "serverctl" group to manage systemd units.
polkit.addRule(function (action, subject) {
    if (subject.isInGroup("serverctl") &&
        action.id == "org.freedesktop.systemd1.manage-units") {
        return polkit.Result.YES;
    }
});
```

Then add your user to the group: `usermod -aG serverctl $USER`.

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
