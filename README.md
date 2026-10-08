# ghealth

CLI for the [Google Health API](https://developers.google.com/health) (v4 by default, v4beta opt-in) — built for AI agents and developers.

- **40 verified data types**: steps, heart rate, exercise, sleep, weight, SpO2, HRV, ECG, blood glucose, nutrition, and more
- **Agent-first**: simplified JSON output, deterministic exit codes, `--dry-run`, `--raw`
- **Single binary**: `go build -o ghealth .`

## Quick Start

```bash
ghealth setup                                              # One-time: GCP project + OAuth
ghealth data steps daily-rollup --from 2026-03-22 --to 2026-03-29  # Weekly step totals
ghealth data heart-rate list --from today --limit 10       # Recent heart rate readings
ghealth schema types                                       # See all available data types
```

## Requirements

Download and Install [Go](https://go.dev/doc/install)

## Installation

```bash
git clone https://github.com/Google-Health-API/google-health-cli.git
cd google-health-cli
go build -o ghealth .
```

## Setup

```bash
ghealth setup
```

Walks you through: GCP project ID, OAuth credentials (download from [Console](https://console.cloud.google.com/apis/credentials) — Desktop application type), Health API enablement, scope selection, and browser-based OAuth login.

Files written under `~/.config/ghealth/` (override with `GHEALTH_CONFIG_DIR`):

- `client_secret.json` — your OAuth client (mode 0600)
- `credentials.json` — access + refresh tokens (mode 0600, plaintext JSON)
- `config.toml` — active profile (project, scopes)

Tokens refresh automatically.

### Non-interactive setup (for agents / CI)

```bash
ghealth setup \
  --project-id my-project \
  --client-secret ~/Downloads/client_secret_123.json \
  --scopes-preset readonly \
  --skip-enable-api \
  --no-prompt
```

Add `--non-interactive-auth` to skip the browser step too — complete later with `ghealth auth login --complete <code>` (see below).

## Authentication

| Scenario | Method |
|----------|--------|
| Interactive | `ghealth setup` or `ghealth auth login` |
| Headless / no browser | `ghealth auth login --non-interactive` → click URL on any device → `ghealth auth login --complete <code>` |
| Move tokens between machines | `ghealth auth export` → `ghealth auth import` |
| Pre-configured token | `export GHEALTH_ACCESS_TOKEN=ya29...` |
| Credential file | `export GHEALTH_CREDENTIALS_FILE=/path/to/creds.json` |
| GCP environment | Application Default Credentials (automatic) |

Precedence: `GHEALTH_ACCESS_TOKEN` > `GHEALTH_CREDENTIALS_FILE` > stored credentials > ADC.

### Headless OAuth flow

```bash
# 1. On the host running ghealth:
ghealth auth login --non-interactive --scopes-preset readonly
# → JSON with auth_url (PKCE S256 challenge + random state baked in)
#   and a complete_command. pending_auth.json holds the verifier locally.

# 2. Open auth_url in any browser, click "Allow".
#    The browser will redirect to a localhost URL that fails to load — expected.
#    Copy either the full redirected URL or just the 'code' query parameter.

# 3. Back on the ghealth host (both forms work):
ghealth auth login --complete 'http://localhost/?code=4/0AX4XfWh...&state=cQq...'
ghealth auth login --complete 4/0AX4XfWh...
# → state validated, PKCE verifier sent on exchange, tokens persisted.
```

State mismatch (URL paste with the wrong `state` parameter) clears the pending flow and returns exit 2. The bare-code form skips state validation but still consumes the pending file, so a stale flow can't be replayed.

### Move tokens between machines

```bash
# source (already authenticated):
ghealth auth export > /tmp/ghealth-creds.json
scp /tmp/ghealth-creds.json target:

# target (also needs client_secret.json — either run 'ghealth setup --non-interactive-auth' or copy it):
ghealth auth import --file /tmp/ghealth-creds.json
```

### Bootstrap from a fresh machine (no client_secret yet)

When no OAuth `client_secret.json` is configured, every auth command returns a
structured error with a `next_steps` array — the same six steps every time —
so an agent can relay it to a user verbatim without scraping prose:

```bash
ghealth auth login
# → exit 5, JSON on stderr:
# {
#   "error": {
#     "type": "config", "code": 5,
#     "message": "No OAuth client_secret.json configured",
#     "hint":    "Run 'ghealth setup' to create or import OAuth credentials",
#     "next_steps": [
#       "Open https://console.cloud.google.com/apis/credentials",
#       "Create or select a Google Cloud project",
#       "Enable the Google Health API (...)",
#       "Create OAuth client ID with Application type: Desktop app",
#       "Download the client_secret JSON",
#       "Run: ghealth setup --client-secret /path/to/client_secret.json"
#     ]
#   }
# }
```

Same `next_steps` are emitted by:

- `ghealth auth login` (interactive / `--non-interactive` / `--complete`)
- `ghealth auth status` when neither stored creds nor env creds are present
- `ghealth auth refresh`, `ghealth auth export` (when nothing to refresh/export)
- `ghealth setup --no-prompt` when `--client-secret` is missing

To fetch the checklist **without** triggering an error (e.g. so an agent can
display the bootstrap steps before calling auth at all):

```bash
ghealth setup --instructions
# → exit 0, JSON on stdout with status: "instructions" and the next_steps array
```

## Data Types

40 types verified against the live API. Run `ghealth schema types` for the full list.

| Type | Key Values | Operations |
|------|-----------|------------|
| `steps` | *(use daily-rollup for `countSum`)* | list, rollup, daily-rollup, reconcile |
| `heart-rate` | `beatsPerMinute` | list, rollup, daily-rollup, reconcile |
| `exercise` | type, duration, calories, avgHeartRate, notes | list, get, create, update, delete, reconcile, export-tcx |
| `sleep` | minutesAsleep, minutesAwake, stageMinutes | list, get, create, update, delete, reconcile |
| `weight` | `weightGrams` | list, get, create, update, delete, rollup, daily-rollup, reconcile |
| `body-fat` | `percentage` | list, get, create, update, delete, rollup, daily-rollup, reconcile |
| `height` | `heightMillimeters` | list, get, create, update, delete, reconcile |
| `distance` | *(use daily-rollup for `millimetersSum`)* | list, rollup, daily-rollup, reconcile |
| `heart-rate-variability` | RMSSD | list, reconcile |
| `oxygen-saturation` | `percentage` (SpO2) | list, reconcile |
| `altitude` | altitude value | list, rollup, daily-rollup, reconcile |
| `active-zone-minutes` | `activeZoneMinutes`, `heartRateZone` | list, daily-rollup, reconcile |
| `activity-level` | SEDENTARY, LIGHT, MODERATE, VIGOROUS | list, reconcile |
| `basal-energy-burned` | `kcal` per interval (BMR) | list, reconcile |
| `active-energy-burned` | `kcal` per interval (activity) | list, rollup, daily-rollup, reconcile |
| `vo2-max` | VO2 max value | list, reconcile |
| `total-calories` | *(use daily-rollup for `kcalSum`)* | daily-rollup |
| `sedentary-period` | sedentary intervals | list, daily-rollup, reconcile |
| `swim-lengths-data` | `swimStrokeType`, `strokeCount` *(use daily-rollup for `strokeCountSum`)* | list, rollup, daily-rollup, reconcile |
| `hydration-log` | `milliliters` consumed | list, get, daily-rollup, reconcile |
| `nutrition-log` | nutrients, `energy`, mealType, food | list, get, rollup, daily-rollup, reconcile |
| `food` | nutrient profiles, servings *(catalog — no time filter)* | list, get |
| `food-measurement-unit` | `displayName` *(catalog — no time filter)* | list, get |
| `blood-glucose` | mg/dL, mealType, measurementTiming | list, get, rollup, daily-rollup, reconcile |
| `core-body-temperature` | `temperatureCelsius` | list, get, rollup, daily-rollup, reconcile |
| `electrocardiogram` | waveform, `resultClassification` *(requires `ecg.readonly`)* | list |
| `irregular-rhythm-notification` | alert windows *(requires `irn.readonly`)* | list |
| `daily-resting-heart-rate` | `beatsPerMinute` per day | list, reconcile |
| `daily-heart-rate-variability` | daily HRV summary | list, reconcile |
| `daily-oxygen-saturation` | daily SpO2 summary | list, reconcile |
| `daily-respiratory-rate` | daily respiratory rate | list, reconcile |
| `daily-vo2-max` | daily VO2 max | list, reconcile |
| `daily-sleep-temperature-derivations` | temp deviation from baseline | list, reconcile |
| `respiratory-rate-sleep-summary` | per-stage respiratory rate | list, reconcile |
| `run-vo2-max` | VO2 max from running | list, daily-rollup, reconcile |
| `floors` | *(rollup only — `countSum`)* | rollup, daily-rollup, reconcile |
| `active-minutes` | *(rollup only)* | rollup, daily-rollup, reconcile |
| `time-in-heart-rate-zone` | *(rollup only)* | daily-rollup, reconcile |
| `calories-in-heart-rate-zone` | *(rollup only — `caloriesInHeartRateZones` array per bucket)* | rollup, daily-rollup, reconcile |
| `daily-heart-rate-zones` | *(reconcile only)* | reconcile |

### Exercise track export

`export-tcx` writes the raw Google TCX, or — with `--as csv` — flattens it to one row per trackpoint (`time, activity, lap, sport, latitude_deg, longitude_deg, altitude_m, distance_m, heart_rate_bpm, cadence_rpm, speed_mps, watts`) for direct `pd.read_csv` consumption. Indoor activities have no track and yield a header-only CSV; their summary/notes live in `data exercise list`. Pass `--output -` to stream to stdout instead of a file.

```bash
ghealth data exercise export-tcx --id <id> --output ride.csv --as csv
ghealth data exercise export-tcx --id <id> --output - --as csv | head   # stream to stdout
```

## Usage

### Reading data

```bash
# Recent heart rate (sample-type: returns individual readings)
ghealth data heart-rate list --from today --limit 10

# Daily step totals for a week (rollup: returns aggregated counts)
ghealth data steps daily-rollup --from 2026-03-22 --to 2026-03-29

# Exercises this month
ghealth data exercise list --from 2026-03-01

# Weight history
ghealth data weight list --limit 20

# Sleep (summary by default, --detail for stage-by-stage breakdown)
ghealth data sleep list --limit 5
ghealth data sleep list --limit 5 --detail
```

Every read (`list`, `get`, `rollup`, `daily-rollup`, `reconcile`) returns the same JSON shape — an object `{"dataPoints": [...]}` with optional `_hints` and `nextPageToken` — so the rows are always under `dataPoints`.

`list` returns up to `--limit` rows (default 500). When more exist it includes a `nextPageToken`; pass it back with `--page-token` to fetch the next page losslessly (no rows skipped or repeated):

```bash
ghealth data heart-rate list --from 2026-06-15 --limit 500            # → {"dataPoints":[…], "nextPageToken":"ABC"}
ghealth data heart-rate list --from 2026-06-15 --limit 500 --page-token ABC
```

### Important: list vs daily-rollup

Some types (steps, distance) return **time intervals without values** from `list`. Use `daily-rollup` to get totals:

```bash
# This returns minute-by-minute intervals (no step count):
ghealth data steps list --from today --limit 5

# This returns daily totals with actual counts:
ghealth data steps daily-rollup --from 2026-03-22 --to 2026-03-29
# → {"dataPoints": [{"date": "2026-03-28", "countSum": "9037"}, ...]}
```

### Gotcha: missing days are NOT zeros

For the presence-aware types — `altitude`, `distance`, `floors`, `steps`, `total-calories` — a date that is **absent** from rollup output means the device was not worn (or did not sync) that day, **not** that the value was zero. A bucket with `countSum: "0"` is a **true zero**: the device was worn and genuinely recorded no activity.

- Missing date → render as "no data", never coalesce to 0
- `countSum: "0"` → true zero (worn, no activity)
- Never average over absent days as if they were zeros — that silently deflates weekly/monthly stats

### Filtering

```bash
ghealth data heart-rate list --from 2026-03-28                  # From date
ghealth data heart-rate list --from 2026-03-28 --to 2026-03-29  # Date range
ghealth data heart-rate list --from today --limit 50            # Today, max 50
ghealth data heart-rate list --from yesterday                   # Since yesterday
```

`--filter` passes a raw expression to the API (overrides `--from`/`--to`). Filter syntax follows [AIP-160](https://google.aip.dev/160) — interval types use `{type}.interval.civil_start_time` (ISO 8601, no `Z`), sleep uses `sleep.interval.civil_end_time` (only end-time is filterable), sample types use `{type}.sample_time.physical_time` (RFC-3339, with `Z`). Only `>=` and `<` comparators are supported.

### Writing data

Writable types: `exercise`, `sleep`, `weight`, `body-fat`, `height`.

Write operations are asynchronous — the API returns an Operation object. Use `list` to verify the data was persisted.

To discover the correct JSON format, inspect a real response: `ghealth data weight list --raw --limit 1`

```bash
# Create (use --raw list output to model the payload structure)
ghealth data weight create --json '{"weight": {"weightGrams": 75500, "sampleTime": {"physicalTime": "2026-03-29T10:00:00Z", "utcOffset": "3600s"}}}'

# Update (use --update-mask to specify which fields to change)
ghealth data weight update --id <id> --json '{"weight": {"weightGrams": 76000}}'

# Delete (accepts bare IDs or full resource names)
ghealth data exercise delete --ids 7649353586249326520
```

## Output

Responses are **simplified by default** — redundant timestamps, empty fields, and repeated metadata are stripped. Timestamps include the user's UTC offset (e.g., `+01:00`).

```bash
ghealth data heart-rate list --from today --limit 2
```
```json
{
  "dataPoints": [
    {"time": "2026-03-29T16:33:07+01:00", "beatsPerMinute": "80", "source": "Google Pixel Watch 4 (41mm)"},
    {"time": "2026-03-29T16:33:04+01:00", "beatsPerMinute": "80", "source": "Google Pixel Watch 4 (41mm)"}
  ]
}
```

```bash
ghealth data steps daily-rollup --from 2026-03-26 --to 2026-03-29
```
```json
{
  "dataPoints": [
    {"date": "2026-03-28", "countSum": "9037"},
    {"date": "2026-03-27", "countSum": "2408"},
    {"date": "2026-03-26", "countSum": "6474"}
  ]
}
```

| Flag | Effect |
|------|--------|
| `--raw` | Return the original API response with no simplification |
| `--format table` | Aligned columns |
| `--format csv` | CSV output (nested objects flatten to dot-separated columns) |
| `-o, --output <file>` | Write data to the file; print only a column schema + 3-row preview to stdout. Prefer this over `> file` (which gives the file but no schema) |
| `--dry-run` | Show the HTTP request without executing |

In `--format csv` and `--format table`, the data stream stays pure: `_hints` and a leftover `nextPageToken` are written to **stderr** rather than mixed into the rows, and an empty result emits an empty CSV (never a JSON object). Use `-o <file>` and the stderr signals together to page through a large export without polluting the CSV.

## AI Agent Skills

The repo ships 2 Agent Skills (`SKILL.md` files) — one for shared prerequisites (auth, setup, global flags) and one covering all 40 data types, operations, patterns, and gotchas.

```bash
# Install all skills at once
npx skills add https://github.com/Google-Health-API/google-health-cli

# Or pick only what you need
npx skills add https://github.com/Google-Health-API/google-health-cli/tree/main/skills/ghealth
npx skills add https://github.com/Google-Health-API/google-health-cli/tree/main/skills/ghealth-shared
```


Agents don't need to read the full skill file upfront. The CLI supports progressive self-discovery:

```bash
ghealth schema types              # What types exist? What operations?
ghealth schema type heart-rate    # Fields, parameters, scope for one type
ghealth data <type> --help        # What operations does this type support?
ghealth data <type> list --help   # What flags does this operation take?
ghealth --dry-run ...             # What HTTP request would this send?
```

## Other Commands

```bash
ghealth user identity                    # User identity
ghealth user profile get                 # Profile (age, stride length)
ghealth user settings get                # Settings (timezone, units)
ghealth auth status                      # Auth state (scopes, expiry)
ghealth schema types                     # All data types + operations
ghealth schema type heart-rate           # Detail for one type
ghealth schema scopes                    # OAuth scopes
ghealth schema endpoints                 # All API endpoints
ghealth config show                      # Show active configuration (project, scopes, format)
ghealth config set timezone <IANA zone>  # Set a config value (keys: project_id, format, timezone)
ghealth webhooks subscribers list        # Manage push-notification subscribers / subscriptions
```

`webhooks` (subscribers, subscriptions, `verify`) manages project-level push notifications and requires the `cloud-platform` scope plus a configured project ID — see the skill docs for details.

Because a `cloud-platform` token is rejected by the data-plane endpoints, keep webhook credentials in a **separate config dir** from your health-data credentials. `auth login` always writes tokens to the active config dir, so use a dedicated `GHEALTH_CONFIG_DIR` for webhooks (not `GHEALTH_CREDENTIALS_FILE`, which only changes where tokens are *read* from):

```bash
export GHEALTH_CONFIG_DIR=~/.config/ghealth-webhooks
ghealth setup                              # its own client_secret + project
ghealth auth login --scopes cloud-platform
ghealth webhooks subscribers list
```

### Timezones

Date arguments (`--from`/`--to`, `today`, `yesterday`) resolve against exactly one timezone, chosen in this order:

1. the active profile's configured zone — `ghealth config set timezone <IANA zone>`
2. otherwise, the machine-local timezone

### Verifying tokens

`ghealth auth status` is a fast local check by default — it reports what's configured without making any network calls, and the `authenticated` field reflects local state only. For env-token / credentials-file modes it is omitted entirely (presence of a token doesn't prove validity).

```bash
ghealth auth status --validate
```

Verifies the access token against Google's `tokeninfo` endpoint. `authenticated` then reflects actual validity, and the response includes `expires_in` and `scope` from Google.

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | API error (4xx/5xx) |
| 2 | Auth error (have client_secret, missing or invalid tokens — run `ghealth auth login`) |
| 3 | Validation error |
| 4 | Network error |
| 5 | Config error (often: no `client_secret.json` — error carries `next_steps` for bootstrap) |

Errors are always JSON on stderr and may include a `next_steps: []string` array for multi-step recovery (currently only emitted when no OAuth client is configured).

## Environment Variables

| Variable | Purpose |
|----------|---------|
| `GHEALTH_ACCESS_TOKEN` | Direct access token |
| `GHEALTH_CREDENTIALS_FILE` | Path to credential JSON |
| `GHEALTH_CONFIG_DIR` | Config directory override |
| `GHEALTH_PROFILE` | Active profile name |
| `GHEALTH_FORMAT` | Default output format (json/table/csv) |
| `GHEALTH_API_VERSION` | API channel: `v4` (default) or `v4beta` |
| `GHEALTH_BASE_URL` | Override the API base URL, including its version segment (e.g. `http://127.0.0.1:8080/v4`). Takes precedence over the API version for requests; its version segment becomes the active channel |


## API channels (v4 and v4beta)

ghealth talks to the GA **v4** API by default. New features bake on the **v4beta** channel before GA; opt in per call or per profile:

```bash
ghealth --api-version v4beta data <type> list --from today   # one call
export GHEALTH_API_VERSION=v4beta                            # this shell
ghealth config set api_version v4beta                        # active profile
```

Resolution order: `--api-version` > `GHEALTH_API_VERSION` > profile `api_version` > `v4`. `v4beta` is a superset of `v4`, so every GA command keeps working on it.

- Beta-only data types and commands are always listed (marked `[beta]` in `--help`, `"channel": "v4beta"` in `ghealth schema types`), but on `v4` they fail with a validation error (exit 3) whose hint says to rerun with `--api-version v4beta`.
- JSON data responses carry `"_api_version"` so you can tell which channel answered; `ghealth auth status` reports the effective `api_version`, its source, and `base_url`.
- If `GHEALTH_BASE_URL` is set, its version segment wins; an explicit `--api-version` that disagrees with it is a validation error.
- Use v4 unless you need a beta-only feature: beta surfaces may change before GA.

## License

Apache 2.0 — see [LICENSE](LICENSE.md).
