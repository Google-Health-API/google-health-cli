---
name: ghealth-shared
description: Shared prerequisites for all ghealth skills — auth, setup, global flags, command structure
---

# ghealth — Shared Prerequisites

## Setup

```bash
ghealth setup                       # interactive wizard, run once
```

Six steps: GCP project ID, OAuth client_secret JSON (Desktop application), Health API enable, scope selection, browser-based OAuth, save profile.

**Scriptable** (skips prompts):

```bash
ghealth setup \
  --project-id my-project \
  --client-secret ~/Downloads/client_secret_123.json \
  --scopes-preset readonly \
  --skip-enable-api \
  --no-prompt
```

Add `--non-interactive-auth` to skip the browser step and finish later with `ghealth auth login --complete <code>`.

## Files written

All under `~/.config/ghealth/` (override with `GHEALTH_CONFIG_DIR`):

| File | Contents | Mode |
|------|----------|------|
| `client_secret.json` | Your GCP OAuth client | 0600 |
| `credentials.json` | Access + refresh tokens (plaintext JSON) | 0600 |
| `pending_auth.json` | In-progress non-interactive flow state | 0600 |
| `config.toml` | Active profile (project, scopes, format) | 0600 |

## Authentication

| Method | Usage |
|--------|-------|
| Stored credentials | `ghealth auth login` (default after setup) |
| Headless (no browser) | `ghealth auth login --non-interactive` → click URL → `ghealth auth login --complete <code>` |
| Move tokens between machines | `ghealth auth export > creds.json` → on target: `ghealth auth import --file creds.json` |
| Access token (no refresh) | `export GHEALTH_ACCESS_TOKEN=ya29...` |
| Credential file | `export GHEALTH_CREDENTIALS_FILE=/path/to.json` |
| ADC (GCP envs) | Automatic if available |

Precedence: env token > credential file > stored > ADC. Refresh tokens are persisted in `credentials.json` and refreshed automatically (1h access-token lifetime); a 401 from the API force-refreshes once before erroring.

Check: `ghealth auth status`. Exit codes:
- 0 — locally configured; does not make a network call. Expired stored tokens still exit 0 — the JSON reports `authenticated: false` / `expired: true`
- 2 — `client_secret.json` present but no stored tokens (`credentials.json` missing); run `ghealth auth login`
- 5 — no `client_secret.json` at all; error carries `next_steps[]` for bootstrap (see below)

Add `--validate` to verify the access token against Google's tokeninfo endpoint. Without it, env-token / credentials-file modes report `configured: true` but never claim `authenticated: true` — presence of a token doesn't prove validity. With `--validate`, the response includes `authenticated`, `expires_in`, and `scope` from Google.

## Bootstrap recipes for agents

**Fetch the OAuth client_secret checklist (no failing call needed):**

```bash
ghealth setup --instructions
# → exit 0, JSON on stdout with status: "instructions" and next_steps[].
# Same six steps an auth error would emit when no client_secret.json is configured:
#   1. Open https://console.cloud.google.com/apis/credentials
#   2. Create or select a Google Cloud project
#   3. Enable the Google Health API
#   4. Create OAuth client ID with Application type: Desktop app
#   5. Download the client_secret JSON
#   6. Run: ghealth setup --client-secret /path/to/client_secret.json
```

The identical `next_steps` array is also embedded in every error path where a missing `client_secret.json` blocks progress: `auth login` (all modes including `--complete`), `auth status`, `auth refresh`, `auth export`, `setup --no-prompt`. Agents should `jq -e '.error.next_steps'` and relay verbatim.

**Fresh install on a workstation (has browser):**

```bash
ghealth setup    # one interactive run; refresh handles itself afterwards
```

**Fresh install on a headless host (no browser):**

```bash
ghealth setup --project-id $P --client-secret $CS \
              --scopes-preset readonly --skip-enable-api \
              --no-prompt --non-interactive-auth
# stdout prints status: "setup_pending_auth" + an auth_url on stderr
# human clicks the URL on any browser → URL bar shows ?code=... ; agent copies it
ghealth auth login --complete <code>
```

**Mount tokens into a container / second worker:**

```bash
# on authenticated host:
ghealth auth export > /secrets/ghealth-creds.json
# in the container (with the same client_secret.json available):
ghealth auth import --file /secrets/ghealth-creds.json
```

## Command Structure

```
ghealth data <type> <operation> [flags]
ghealth user <subcommand>
ghealth schema <subcommand>
ghealth webhooks <subcommand>
```

`user` subcommands: `identity`, `profile get|update`, `settings get|update`, `irn-profile`, `paired-devices list|get --id <id>`.

`webhooks` subcommands: `subscribers list|create|update|delete`, `subscriptions list|create|update|delete --subscriber <id>`, `verify --url <url>`. **Requires the `cloud-platform` scope** (`ghealth auth login --scopes cloud-platform`) **and a configured project ID.** These are project-level push-notification endpoints; the authenticated identity also needs Health API subscriber IAM permissions on the project.

## Global Flags

| Flag | Effect |
|------|--------|
| `--format json\|table\|csv` | Output format (default: json) |
| `-o <file>` | Write data to file; stdout shows only column schema + 3-row preview (not the data) |
| `--raw` | Bypass response simplification, return original API JSON |
| `--dry-run` | Print HTTP request without executing |
| `--profile <name>` | Use named config profile |
| `--api-version v4\|v4beta` | API channel (default `v4`; also `GHEALTH_API_VERSION` or `ghealth config set api_version`). Commands marked `[beta]` need `v4beta`; on `v4` they exit 3 with a hint. `GHEALTH_BASE_URL`'s version segment wins if set |

`list` also accepts: `--limit N` (max total results, default 500), `--from`, `--to`, `--filter`, `--page-token` (resume from a prior response's `nextPageToken`), `--detail` (sleep).

## Discovery

```bash
ghealth schema types              # All types + supported operations
ghealth schema type <name>        # Fields, parameters for one type
ghealth data <type> --help        # Available operations
ghealth data <type> list --help   # Flags for an operation
```

## Output

- Responses are simplified by default: flat timestamps with UTC offset, compact source, no empty fields
- JSON output always has the same shape for every read (`list`, `get`, `rollup`, `daily-rollup`, `reconcile`): an object `{"dataPoints": [...]}`, with optional `_hints` and `nextPageToken`. The rows are always under `dataPoints` — even for rollups and empty results — so `json.load(out)["dataPoints"]` always works
- `--raw` returns the full API response
- `--format csv` outputs flat CSV; nested objects are auto-flattened to dot-separated columns (e.g., `metricsSummary.caloriesKcal`)
- `-o <file>` writes data to the file and prints **only** a summary to stdout (row count, column names, 3-row preview). Use this instead of piping (`> file`) — piping gives you the file but no schema on stdout
- Errors always JSON on stderr, exit codes 0-5
- Timestamps include user's timezone offset (e.g., `+01:00`)
