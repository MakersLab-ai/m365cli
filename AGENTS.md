# Repository Guidelines

## Project Structure

- `cmd/m365/`: CLI entrypoint (`main.go`).
- `internal/cli/`: command wiring (one file per domain: `mail`, `calendar`,
  `contacts`, `drive`, `sp`, `doctor`). Thin glue — no business logic.
- `internal/config/`: config loading + the fail-closed allowlist matcher.
- `internal/auth/`: app-only certificate auth (MSAL) + 0600 token cache.
- `internal/graph/`: the scoped Graph REST client — the single choke point that
  enforces `allowed_mailboxes` / `allowed_sites` before any network call.
- `internal/{mail,calendar,contacts}/`: HTTP-independent domain logic and Graph
  payload builders (the send guardrail lives in `mail`).
- `docs/`: setup and reference docs. `.agents/skills/m365/`: the agent skill.

## Build, Test, Development

- `make build` — build `bin/m365`.
- `make test` — run all unit tests.
- `make vet` / `make fmt` — vet and format.
- `make check` — fmt + vet + test (run before committing).

## Conventions

- **TDD.** Write the test first for any logic in `internal/*` (config, auth,
  graph, and the domain builders). CLI glue is exercised via the domain packages.
- **Output discipline.** Data → stdout (the JSON envelope via `internal/output`);
  human hints/progress → stderr. Keep stdout parseable.
- **Security is centralized.** All mailbox/site access goes through
  `internal/graph` so the allowlist cannot be bypassed. Don't add Graph calls
  that skip it (the one documented exception is `SearchSites`, read-only
  discovery). Never put secrets on the command line; mail/event bodies come from
  `--body-file`.
- **Fail closed.** Empty allowlists deny everything; new guardrails should keep
  that property.

## Testing Notes

- Unit tests use stdlib `testing` + `httptest` (no live tenant needed).
- Live end-to-end requires a real Azure app + tenant; see `docs/azure-setup.md`
  and verify with `m365 doctor --live`.
