# serverctl Roadmap

**Current release: [v0.2.1](https://github.com/felipecastillo-b/serverctl/releases/tag/v0.2.1) (2026-10-29).**
Milestones M0–M8 shipped in v0.1.0; v0.2.0 fixed hardware sensor detection and
added usefulness ordering; v0.2.1 delivered the M9 hardening batch. This
document covers what is delivered, how work flows, the live bug queue, and the
proposed milestones ahead. Everything under "Proposed milestones" is a proposal
— the maintainer prunes, splits, or reorders.

## 1. How we work

- Every change: GitHub Issue → feature branch → Pull Request (English titles
  and descriptions, `Closes #N`).
- Commit messages follow Conventional Commits.
- PRs are review-sized, around ≤ 400 changed lines; larger work is split into
  stacked PRs.
- All code and documentation are MIT licensed.

### Release policy (0.x)

- Bug-fix-only batches tag patch releases: `v0.2.1`, `v0.2.2`, ...
- The first new feature or visible behavior change bumps MINOR: `v0.3.0`.
- Docs-only changes ride along with the next release and do not need a tag.
- Tags are annotated (`serverctl vX.Y.Z — summary`); the release workflow
  publishes automatically on tag push.

### Bug intake

Anything found in daily use gets a GitHub issue labeled `bug`, with machine
context (distro, kernel, hardware) and the exact command/state that triggered
it. Fixes are grouped into the next patch batch and released per the policy
above. §3 keeps the notable known limitations in one place; the authoritative
queue is GitHub issues.

## 2. Delivered

| Milestone | Scope | Shipped in |
|-----------|-------|------------|
| M0 — Repo & tooling | README, CONTRIBUTING, Makefile, CI (build/vet/lint/test), goreleaser scaffold | v0.1.0 |
| M1 — TUI shell | Config load, dark/light theme, router, sidebar, keymap, help overlay, mouse wiring, quit | v0.1.0 |
| M2 — Dashboard overview | CPU, RAM, uptime, load, kernel, hardware summary, sensors | v0.1.0 |
| M3 — Processes | List with sort/filter, signal actions with confirmation | v0.1.0 |
| M4 — Services & logs | systemd unit list, start/stop/restart, journal viewer | v0.1.0 |
| M5 — Storage & network | Disks/partitions/usage, interfaces/traffic, ports/connections | v0.1.0 |
| M6 — Packages & access | Installed count (Arch), users/sessions, SSH attempt audit | v0.1.0 |
| M7 — UX polish | Command palette, configurable intervals, error/stale surfaces, theme refinement | v0.1.0 |
| M8 — Release engineering | goreleaser pipeline, PKGBUILD parked, docs, v0.1.0 | v0.1.0 |
| M9 — Hardening & release hygiene | Runner pin, action bumps, PR-retarget CI, v0.2.0 docs accuracy, UI clipping papercuts | v0.2.1 |

Post-release work: **v0.2.0** — sensors were undetected on all real hardware
(sysfs symlink walk bug) and now render ordered by usefulness
([#40](https://github.com/felipecastillo-b/serverctl/issues/40),
[#41](https://github.com/felipecastillo-b/serverctl/pull/41)). **v0.2.1** —
M9: CI runner pin and action bumps, CI re-runs on PR retarget, long-error and
footer clipping fixed, docs aligned to v0.2.0 ([#42](https://github.com/felipecastillo-b/serverctl/issues/42)).

### Released versions

| Tag | Date | Highlights |
|-----|------|------------|
| [`v0.1.0`](https://github.com/felipecastillo-b/serverctl/releases/tag/v0.1.0) | 2026-10-14 | First release: all eight modules, palette, themes, packaging pipeline |
| [`v0.2.0`](https://github.com/felipecastillo-b/serverctl/releases/tag/v0.2.0) | 2026-10-15 | Sensors detected on real hardware + ordered by usefulness |
| [`v0.2.1`](https://github.com/felipecastillo-b/serverctl/releases/tag/v0.2.1) | 2026-10-29 | M9: CI hardening, UI clipping papercuts, v0.2.0 docs accuracy |

### Shipped modules

Dashboard (system, hardware, sensors), Processes, Services, Logs, Storage,
Network, Packages, Users — under a command palette, keyboard and mouse
navigation, dark/light themes, and per-module refresh intervals via config.

## 3. Known limitations

Tracked, not forgotten. New field findings join this table and get a GitHub
issue labeled `bug`.

| Area | Limitation / deferred item | Status |
|------|---------------------------|--------|
| Sensors | Temperature-only; chips outside the tier table still render, sorted last (extend the table as hardware appears) | by design |
| Sensors | No fan/voltage/current readings yet | candidate (M12) |
| Services | Unit mutations over SSH usually fail — polkit treats SSH sessions as non-local; no auth agent | open |
| Access | Socket-activated `sshd@.service` units are not matched by the SSH attempt audit | open |
| Logs | Journal visibility depends on the user's groups | environment |
| Mouse | Table row clicks and log wheel/click are not wired | deferred (M13) |
| UI | `esc` screen-stack pop is a no-op until detail views exist | deferred (M13) |
| Cadence | Self-ticked collector chains pause while their screen is inactive | by design (for now) |
| Packages | Count only; pending updates are out of scope (they need elevated tooling or extra dependencies); dpkg/rpm adapters ship in M10 | in progress (M10) |
| Delivery | AUR publication blocked — registrations are disabled on the site; the parked PKGBUILD tracks release metadata only | blocked |
| Testing | `internal/core`, `cmd`, `help`, `sidebar` lack direct tests; no golden screen renders | planned |

## 4. Proposed milestones (v0.3.0 onward)

Proposal for the maintainer to prune or reorder. Each milestone is sized to
become one GitHub Issue.

### M10 — Portability (multi-distro)

- [ ] Package-count adapters for dpkg and rpm beside the Arch adapter (the
      post-MVP seam already exists in `internal/core/ports.go`).
- [ ] Sessions via logind D-Bus; SSH attempts via `auth.log`/wtmp fallbacks.
- [ ] Match socket-activated `sshd@.service` units in the access audit.

### M11 — Containers (new module)

- [ ] Read-only Docker/Podman module: container list, state, image, uptime.
      Lifecycle actions explicitly out of scope for the first cut.

### M12 — Hardware health

- [ ] SMART disk health via optional `smartctl` (read-only: pass/fail plus key
      attributes).
- [ ] Sensor depth: extend the chip tier table from field reports; evaluate
      fan/voltage readings.

### M13 — UX depth

- [ ] Screen stack and detail views (`esc` pops; drill-down from tables).
- [ ] Palette-triggered whitelisted actions (documented follow-up in
      ARCHITECTURE.md §6).
- [ ] Full mouse parity: table row clicks, log wheel/click.
- [ ] Screen-local keybindings in config.

### Engineering quality (threaded through milestones)

- [ ] Golden-render tests for screens; direct tests for `core` contracts and
      the `cmd` entrypoint.

## 5. Out of scope

Same non-goals as ARCHITECTURE.md §11 — revisit only by explicit maintainer
decision:

- No database or persistence layer.
- No WebUI or remote API.
- No multi-server management.
- No historical metrics beyond live views.
- No pending package updates (they need elevated tooling or extra
  dependencies).

## 6. Release runbook

1. Merge PRs to `main` with CI green.
2. Verify the merge commit locally (`go build ./...`, `go vet ./...`,
   `go test ./...`).
3. `git tag -a vX.Y.Z -m "serverctl vX.Y.Z — summary"` and
   `git push origin vX.Y.Z`.
4. `release.yml` builds amd64/arm64 and publishes the GitHub release
   automatically.
5. Post-release: update README status and PKGBUILD when applicable.
