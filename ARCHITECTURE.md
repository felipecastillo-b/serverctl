# serverctl Architecture

**serverctl is a local-first, single-binary TUI for monitoring and administering Linux servers over SSH, built in Go with Bubble Tea.** It runs unprivileged and read-only by default, prefers kernel interfaces (`/proc`, `/sys`, D-Bus) over shelling out, and gates every mutating operation behind a whitelist and a confirmation prompt. First target platform: Arch Linux.

## 1. Overview

serverctl answers two questions in one terminal process: *what is this server doing right now*, and *what may I safely change*. A dashboard-first shell organizes monitoring modules behind a sidebar; all collection is asynchronous, and every screen is fully operable by keyboard or mouse over a plain SSH session.

Design principles:

| Principle | Commitment |
|-----------|------------|
| Local-first single binary | One self-contained executable; no daemon, no database, no runtime dependencies. |
| Low resource usage | Bounded goroutines, configurable refresh intervals, no hidden background polling. |
| Security-first, minimal privilege | Read-only and unprivileged by default; privileged work only via D-Bus/PolicyKit. |
| No arbitrary shell execution | Whitelisted fixed-argv commands only; never `sh -c` with user input. |
| Keyboard/mouse parity | Every mouse action has a keyboard equivalent; the keymap is configurable. |
| Modular and extensible | Modules register behind interfaces; adding one never touches the UI shell. |

## 2. Layered architecture

Dependencies point downward only: `ui` consumes ports defined in `core`; `collectors` and `actions` implement them. The UI never touches `/proc` directly, collectors never mutate, and `actions` is the **only** layer allowed to change system state.

```
┌────────────────────────────────────────────────────────┐
│ cmd/serverctl        entrypoint · flag/config wiring   │
├────────────────────────────────────────────────────────┤
│ internal/ui          root model · router · components  │
├────────────────────────────────────────────────────────┤
│ internal/config      YAML: theme · keymap · interval   │
├────────────────────────────────────────────────────────┤
│ internal/core        domain models · registry · ports  │
├──────────────────────────────┬─────────────────────────┤
│ internal/collectors          │ read-only adapters      │
│ internal/actions             │ whitelisted mutations   │
├──────────────────────────────┴─────────────────────────┤
│ Linux: /proc · /sys · D-Bus · journal · wtmp · pacman  │
└────────────────────────────────────────────────────────┘
```

| Layer | Responsibility | Package |
|-------|----------------|---------|
| Entrypoint | CLI flags, config load, process wiring | `cmd/serverctl` |
| Config | YAML config: theme (dark/light), keybindings, refresh interval | `internal/config` |
| Core | Domain models, module registry, collector/action interfaces (ports) | `internal/core` |
| Collectors | Read-only adapters: procfs (`/proc`), sysfs/hwmon (`/sys`), systemd via D-Bus (`go-systemd`), journal (`sdjournal`), pacman local database (package count), login records (`/var/log/wtmp`, `lastlog`) | `internal/collectors` |
| Actions | **Only layer allowed to mutate**: whitelisted service start/stop/restart via D-Bus and process signals, each gated by confirmation | `internal/actions` |
| UI | Bubble Tea root model, screen router, sidebar, reusable components (table, modal, palette, help overlay), themes, keymap | `internal/ui` |

## 3. Modules

| Module | Data source | Refresh cadence |
|--------|-------------|-----------------|
| Dashboard / overview | Aggregated CPU, memory, load, uptime, kernel, hardware | 2 s |
| Processes | `/proc/<pid>/` | 2 s |
| Services | systemd D-Bus API (`ListUnits`) | 5 s + manual refresh |
| Logs | journal via `sdjournal` | follow, ~1 s |
| Storage | `/proc/mounts`, `statfs`, `/proc/diskstats` | 30 s |
| Network | `/proc/net/dev`, sysfs | 2 s |
| Ports / connections | `/proc/net/{tcp,tcp6,udp,udp6,unix}` | 5 s |
| Packages | pacman local database (installed count; other distros post-MVP) | on demand |
| Users / sessions | utmp, logind D-Bus | 10 s |
| SSH audit | `/var/log/wtmp`, journal of `sshd` | 30 s |
| Sensors | `/sys/class/hwmon` | 3 s |
| System info | `/proc/cpuinfo`, `uname`, DMI sysfs | once at startup |

## 4. Data models

```go
type ServerSnapshot struct {
    Hostname string
    Kernel   KernelInfo
    CPU      CPUInfo
    Memory   MemoryInfo
    Load     LoadAvg
    Uptime   time.Duration
    At       time.Time
}

type KernelInfo struct{ Release, Version, Arch, HardwareModel string }
type LoadAvg    struct{ Load1, Load5, Load15 float64 }
type CPUInfo struct {
    Model  string
    Cores  int
    Usage  float64   // aggregate percent
    PerCPU []float64
}
type MemoryInfo struct {
    Total, Used, Available uint64
    SwapTotal, SwapUsed    uint64
}
type Process struct {
    PID, PPID int
    User      string
    Name      string
    State     string
    CPUPct    float64
    RSSBytes  uint64
    StartedAt time.Time
    Cmdline   string
}
type Service struct { // systemd unit
    Name, Description string
    Load, Active, Sub string
}
type JournalEntry struct {
    Time     time.Time
    Unit     string
    Priority int
    Message  string
}
type Partition struct {
    Device, Mount, FSType string
    Total, Used           uint64
}
type Disk struct {
    Name, Model string
    SizeBytes   uint64
    Partitions  []Partition
}
type NetworkInterface struct {
    Name, MAC string
    Addrs     []string
    Rx, Tx    uint64
    Up        bool
}
type ListenPort struct {
    Proto, Addr string
    Port        uint16
    PID         int
    Process     string
}
type PackageCount struct { // M6: installed count only; per-package
    Distro    string         // listings arrive with multi-distro support,
    Manager   string         // post-MVP
    Installed int
}
type UserSession struct {
    User, TTY, From string
    LoginAt         time.Time
}
type SSHAttempt struct {
    Time     time.Time
    User     string
    SourceIP string
    Success  bool
}
```

## 5. Event & update system

- The whole UI is one Bubble Tea root model; everything asynchronous arrives as a typed `tea.Msg`.
- Each module collects via a `tea.Cmd` with a per-module ticker (configurable interval); collection is async and cancellable on screen switch or quit — the UI never blocks on I/O.
- Results arrive as typed messages (e.g., `ProcessesUpdatedMsg`, `ActionResultMsg`) routed by the root model to the owning screen.
- Panels render explicit loading / error / stale states; a failed collection degrades one panel, never the program.

## 6. Navigation & input

- Sidebar switches modules; screens form a stack (e.g., logs → filtered unit view), `esc` pops back.
- Global keymap (quit, palette, help, sidebar focus) is shadowed by the active screen's keymap (sort, filter, signal).
- Command palette jumps to any module or whitelisted action by fuzzy name; the help overlay lists every binding active in the current context.
- Mouse: cell-motion events hit-tested against registered zones (sidebar items, table rows, buttons).
- Parity rule: **every mouse action has a keyboard equivalent**; all bindings are configurable via the config file.

## 7. Security model

| Control | Mechanism |
|---------|-----------|
| Whitelist-only actions | Only operations registered in `internal/actions` may run; there is no generic command runner. |
| Confirmation gates | Destructive operations (service stop/restart, process signals) require an explicit modal confirmation. |
| No shell interpolation | External commands run via `exec` with fixed argv and timeouts; never `sh -c`; no user-typed arguments. |
| Unprivileged by default | All reads work as a regular user; the tool is fully useful in read-only mode. |
| Privileged ops via D-Bus | systemd mutations go through the system bus and PolicyKit, not setuid or sudo wrappers. |
| Audit | Every action attempt (denied, confirmed, executed, failed) is logged locally. |

## 8. System information strategy

Prefer kernel interfaces over shelling out. When a command is unavoidable (pacman fallback, post-MVP distro package managers), it runs with fixed argv, a timeout, and its output is parsed — never re-interpolated.

| Info area | Preferred source | Fallback |
|-----------|------------------|----------|
| CPU usage / model | `/proc/stat`, `/proc/cpuinfo` | — |
| Memory | `/proc/meminfo` | `sysinfo(2)` |
| Load / uptime | `/proc/loadavg`, `/proc/uptime` | — |
| Kernel | `uname(2)`, `/proc/version` | — |
| Hardware | sysfs/DMI (`/sys/devices/virtual/dmi`) | `/proc/cpuinfo` |
| Sensors | `/sys/class/hwmon` | — |
| Disks / partitions | `/proc/mounts`, `statfs`, `/proc/diskstats` | `lsblk -J` (fixed argv) |
| Network interfaces | `/proc/net/dev`, sysfs, netlink | `ip -j address` (fixed argv) |
| Ports / connections | `/proc/net/{tcp,tcp6,udp,udp6}` | `ss` (fixed argv) |
| Processes | `/proc/<pid>/` | — |
| Services | systemd D-Bus API (`go-systemd`) | — |
| Journal | `sdjournal` API | — |
| Installed package count | pacman local database directory (`/var/lib/pacman/local`) | `pacman -Qq` (fixed argv, timeout) |
| Users / sessions | utmp file, logind D-Bus | `lastlog` file |
| SSH attempts | `wtmp`, journal of `sshd` unit | auth log parsing |

## 9. Key dependencies

The dependency list is deliberately short; every entry must justify its place.

| Dependency | Rationale |
|------------|-----------|
| `charmbracelet/bubbletea` | Elm-style TUI runtime: messages, commands, mouse events. |
| `charmbracelet/bubbles` | Reusable components: table, text input, viewport. |
| `charmbracelet/lipgloss` | Styling and dark/light themes. |
| `coreos/go-systemd` (with `godbus/dbus`) | systemd units and journal via D-Bus without shelling out. |
| `gopkg.in/yaml.v3` | User configuration parsing. |

## 10. Testing strategy

- Parsers (`/proc`, `/sys`, pacman output) are tested with golden files under `testdata/`.
- TUI flows are tested with `teatest`: drive messages in, assert the rendered view.
- `internal/actions` sits behind interfaces so tests run against fakes — no test mutates a real system; CI runs build, vet, lint, and test on every pull request.

## 11. Non-goals (MVP)

- No database or persistence layer.
- No WebUI or remote API.
- No multi-server management.
- No historical metrics beyond live views.
