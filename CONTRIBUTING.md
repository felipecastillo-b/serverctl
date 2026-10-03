# Contributing to serverctl

Welcome — thank you for helping build a safe, minimal server administration tool.
This guide is short on purpose: everything in it is checked in CI or enforced by review.

## Workflow

1. **Pick or create a GitHub Issue** describing the change.
2. **Branch from `main`** with a typed prefix: `feat/`, `fix/`, `docs/`, `chore/`, or `ci/`.
3. **Commit using Conventional Commits** (table below).
4. **Open the Pull Request in English**, referencing the issue with `Closes #N`.
5. **Keep PRs review-sized** — around ≤ 400 changed lines; split larger work into stacked PRs.

### Conventional Commits

Format: `type(scope): summary` — scope optional, summary imperative.

| Type | Use for |
|------|---------|
| `feat` | New user-facing behavior |
| `fix` | Bug fixes |
| `docs` | Documentation only |
| `chore` | Tooling, dependencies, repo upkeep |
| `ci` | CI/CD configuration |
| `build` | Build system and packaging |
| `test` | Tests, no production code change |
| `refactor` | Code change with no behavior change |

## Standards

- **Language:** every artifact — code, comments, commits, docs, PRs — is in English.
- **Formatting:** `gofmt`-clean (`make fmt`).
- **Linting:** `make lint` (golangci-lint) must pass.
- **Tests:** `make test` must pass; add coverage for new behavior.

## Security rules (non-negotiable)

These restate the security model in [ARCHITECTURE.md](ARCHITECTURE.md) (section 7).

- **Never use `sh -c`** or shell interpolation; external commands run with fixed `argv` and timeouts.
- **Collectors are read-only** — `internal/actions` is the only layer allowed to mutate system state.
- **Actions are whitelisted and confirmed** — destructive operations require an explicit confirmation prompt.
- Stay unprivileged by default; privileged operations go through D-Bus/PolicyKit, never sudo wrappers.

## Tests

- Parsers (`/proc`, `/sys`, pacman output) are tested with **golden files** under `testdata/`.
- TUI flows are tested with **teatest**: drive messages in, assert the rendered view.
- Actions are tested behind interfaces with fakes — **no test may mutate a real system**.

## License

By contributing, you agree that your contributions are licensed under the [MIT License](LICENSE).
