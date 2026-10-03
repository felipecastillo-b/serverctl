# serverctl Roadmap

**Incremental delivery from empty repository to v1.0.0. Each milestone is sized to become one GitHub Issue.**

## 1. How we work

- Every change: GitHub Issue → feature branch → Pull Request (English titles and descriptions, `Closes #N`).
- Commit messages follow Conventional Commits.
- PRs are review-sized, around ≤ 400 changed lines; larger work is split into stacked PRs.
- All code and documentation are MIT licensed.

## 2. Milestones

| Milestone | Scope | Exit criteria |
|-----------|-------|---------------|
| M0 — Repo & tooling | README, CONTRIBUTING, Makefile, CI (build/vet/lint/test), goreleaser scaffold | CI green on default branch; `make build` produces a binary |
| M1 — TUI shell | Config load, dark/light theme, screen router, sidebar, keymap, help overlay, mouse wiring, quit | Shell navigable by keyboard and mouse across stub screens |
| M2 — Dashboard overview | CPU, RAM, uptime, load, kernel, hardware summary, sensors | Dashboard refreshes live at the configured interval |
| M3 — Processes | Process list with sort/filter, signal actions with confirmation | A process can be inspected and signalled end-to-end |
| M4 — Services & logs | Unit list, start/stop/restart with confirmation, journal viewer | Service lifecycle works via D-Bus; journal tails live |
| M5 — Storage & network | Disks/partitions/usage, interfaces/traffic, listening ports, connections | Values match system tools (`df`, `ip`, `ss`) |
| M6 — Packages & access | pacman packages + available updates, users/sessions, SSH attempts | Package inventory and access audit views populated |
| M7 — UX polish | Command palette, dark/light theme refinement, configurable intervals, error surfaces | Palette covers all modules; stale/error states visible |
| M8 — Release engineering | goreleaser pipeline, PKGBUILD/AUR, docs, v1.0.0 | v1.0.0 tagged; installable from release assets and AUR |

## 3. MVP cut

**MVP = M0–M4.** Dashboard plus processes plus services/logs already delivers daily-driver value for routine server checks over SSH, with the smallest safe action surface.

## 4. V1 cut

**V1 = M0–M8.** Every module from the product brief, plus packaging and release automation.

## 5. Out of scope

Same as the MVP non-goals in ARCHITECTURE.md: no database, no WebUI, no multi-server support, no historical metrics.
