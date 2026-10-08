# ghealth — Development Guide

## What is this

ghealth is a CLI wrapping the Google Health API v4. Primary users are AI agents (OpenClaw, Claude Code, Codex, coding agents) that need structured health data access — steps, heart rate, sleep, exercise, weight, SpO2, HRV. Human developers use it too.

The CLI handles OAuth, pagination, response simplification, and contextual hints so agents can focus on the health question, not API plumbing.

## Progressive disclosure

Information is layered so agents load only what they need:

1. `ghealth --help` → commands overview
2. `ghealth data --help` → list vs daily-rollup guidance, all 40 types
3. `ghealth data <type> --help` → available operations
4. `ghealth data <type> <op> --help` → flags for that operation
5. `ghealth schema types` → machine-readable type registry
6. `ghealth schema type <name>` → fields, parameters, scope for one type
7. `_hints` in responses → contextual next-step suggestions
8. `skills/ghealth/SKILL.md` → non-obvious patterns, gotchas (load once)

## Build

```bash
go build -o ghealth .
go vet ./...
```

Verify against the live API: `ghealth auth status`, then `ghealth data steps daily-rollup --from 2026-03-22 --to 2026-03-29`.

## Workflow

- Feature branches + PRs — never commit to main directly
- Only register data types verified against the live API, not from docs alone
- Test new types with curl before adding to the registry

## Where to make changes

| Task | Start here |
|------|-----------|
| Add/remove a data type | `pkg/types/registry.go` |
| Change response format | `pkg/output/simplify.go` |
| Add a contextual hint | `pkg/output/hints.go` |
| Change CLI flags or help text | `cmd/root.go` (globals), `cmd/data.go` (operations) |
| OAuth or auth flow | `pkg/auth/auth.go` |
| Gate a type/command to a pre-GA channel (v4beta) | `Channel`/`OpChannels` on the type in `pkg/types/registry.go`; for non-data commands `markChannel(cmd, "v4beta")` (`cmd/channel.go`). Promote to GA by removing it |

## Documentation to keep updated

| When you... | Update |
|-------------|--------|
| Add/remove a data type | `README.md` types table, `skills/ghealth/SKILL.md` |
| Change flags or commands | `README.md`, `skills/ghealth-shared/SKILL.md` |

## Reference

- [Google Health API docs](https://developers.google.com/health)
- [Health API setup guide](https://developers.google.com/health/setup)
- [skills/](skills/) — agent skill files
