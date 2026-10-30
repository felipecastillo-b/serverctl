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

> **v0.2.1 is released — milestones M0–M8 shipped in v0.1.0, plus the v0.2.0
> sensor fix and the v0.2.1 hardening batch.** The binary launches the full
> TUI: dashboard, processes, services and journal logs, storage, network,
> packages, and access audit, with a command palette, keyboard and mouse
> control, and dark/light themes. Grab it from the
> [releases page](https://github.com/felipecastillo-b/serverctl/releases) —
> [ROADMAP.md](ROADMAP.md) tracks what is next.

## Features

- Dashboard overview: CPU, memory, load, uptime, kernel, hardware summary
- Process list with sort/filter and confirmed signal actions
- systemd services (start/stop/restart via D-Bus) and journal log viewer
- Storage: disks, partitions, usage
- Network interfaces and live traffic
- Listening ports and active connections
- Installed package count via pacman, dpkg or rpm
- Users and login sessions (logind D-Bus, utmp fallback)
- SSH login attempt audit (journal, auth log and wtmp fallbacks)
- Hardware sensors via hwmon, ordered by dashboard usefulness

## Requirements

- Linux. serverctl targets systemd-based distributions — the services and
  journal screens read systemd over D-Bus and sdjournal.
- Arch Linux is the verified platform today; the dpkg and rpm package-count
  adapters are fixture-tested.
- Containers are optional: the module reads a local Docker or Podman socket
  when one exists. No container daemon is required for the rest of the app.
- Go 1.24+ (plus the systemd headers) only if you build from source.

## Install

Two ways to get serverctl today, straight from GitHub:

**Prebuilt binary (recommended)** — download the tarball for your architecture
from the [releases page](https://github.com/felipecastillo-b/serverctl/releases)
(`serverctl_*_linux_amd64.tar.gz` or `serverctl_*_linux_arm64.tar.gz`), unpack
it, and put `serverctl` on your `PATH`.

**From source** — requires Go 1.24+ and the systemd headers (`systemd-libs` on
Arch, `libsystemd-dev` on Debian/Ubuntu), because the journal reader is cgo:

```bash
git clone https://github.com/felipecastillo-b/serverctl.git
cd serverctl
make build        # produces bin/serverctl
```

**Other distributions.** Arch Linux is the verified platform. The packages
screen adapts to the host's package manager: pacman's local database on Arch,
`/var/lib/dpkg/status` on Debian/Ubuntu, and `rpm -qa` on the Fedora/RHEL
family. Only Arch is exercised end to end today; the dpkg and rpm adapters are
fixture-tested.

## Usage

```bash
bin/serverctl           # launch the TUI (q or ctrl+c quits, ? help, ctrl+p palette)
bin/serverctl -version  # print the build version and exit
```

## Privileges

serverctl itself never escalates — no sudo, no setuid, no password prompts — and
every read it performs is read-only. The one privilege it can inherit rather
than grant is the container socket: reading a system-wide Docker socket carries
the same power as the `docker` group, which is effectively root. Rootless Podman
sockets belong to your user and need no extra group. Mutations ask systemd over
the system D-Bus, where PolicyKit decides whether the caller may run them.

- **Reads** work as a regular user and never write: the Containers module only
  issues GETs to the local daemon and never starts, stops, removes or execs
  anything. Journal entries are richer when your user is in the
  `systemd-journal` group, but nothing requires it.
- **Containers** are optional and prefer rootless: a Podman or rootless Docker
  socket needs no group membership, while a system-wide Docker socket is
  root-equivalent — joining the `docker` group is a real privilege grant, not a
  read convenience.
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
| [ROADMAP.md](ROADMAP.md) | Delivered milestones, release policy, known limitations and proposed roadmap |
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
