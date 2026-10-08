---
name: ghealth
description: Query the Google Health API (v4, opt-in v4beta) — steps, heart rate, exercise, sleep, weight, SpO2, HRV, ECG, blood glucose, nutrition, and 40 total data types
---

# ghealth

CLI for the Google Health API (v4 by default). 40 verified data types.

**Prerequisites:** See `../ghealth-shared/SKILL.md` for auth, setup, global flags.

## Choosing the right operation

| Goal | Operation | Example |
|------|-----------|---------|
| Daily totals (steps, distance, calories) | `daily-rollup` | `ghealth data steps daily-rollup --from 2026-03-22 --to 2026-03-29` |
| Individual readings (HR, weight, SpO2) | `list` | `ghealth data heart-rate list --from today --limit 20` |
| Sessions (exercise, sleep) | `list` | `ghealth data exercise list --from 2026-03-01` |
| Daily summaries (resting HR, HRV, resp rate) | `list` | `ghealth data daily-resting-heart-rate list --from 2026-03-01` |
| Merged multi-source data | `reconcile` | `ghealth data weight reconcile --from 2026-01-01` |

**Why this matters:** `steps list` returns minute-level intervals *without counts*. Use `daily-rollup` to get actual step totals (`countSum`). Same for `distance` (`millimetersSum`) and `floors`.

## Types at a glance

Run `ghealth schema types` for the live version. Quick reference:

**Use daily-rollup for totals:**
- `steps` → `countSum` per day
- `distance` → `millimetersSum` per day
- `total-calories` → `kcalSum` per day (rollup-only)
- `floors` → `countSum` (rollup-only)
- `active-minutes` → (rollup-only)
- `swim-lengths-data` → `strokeCountSum` per day
- `calories-in-heart-rate-zone` → `caloriesInHeartRateZones` per day (rollup-only)

**Use list for readings:**
`heart-rate`, `weight` (writable), `body-fat` (writable), `height` (writable), `oxygen-saturation`, `heart-rate-variability`, `altitude`, `vo2-max`, `active-zone-minutes`, `activity-level`, `basal-energy-burned`, `active-energy-burned`, `blood-glucose`, `core-body-temperature`, `respiratory-rate-sleep-summary`, `run-vo2-max`, `sedentary-period`, `swim-lengths-data`, `hydration-log`

**Use list for sessions:**
- `exercise` (writable) — includes type, duration, calories, HR summary, notes
- `sleep` (writable) — includes summary by default. Add `--detail` for per-stage breakdown.

**Cardiac (dedicated scopes, list-only):**
- `electrocardiogram` — waveform samples + rhythm classification. **Requires `ecg.readonly`.**
- `irregular-rhythm-notification` — alert windows. **Requires `irn.readonly`.**

**Nutrition:**
- `nutrition-log` — logged food entries with nutrient/energy breakdown (list, get, rollup, daily-rollup, reconcile)
- `food`, `food-measurement-unit` — reference catalogs (list, get only). **No time filter** — `--from`/`--to` are ignored.

**Daily summaries** (one value per day, filter by date):
`daily-resting-heart-rate`, `daily-heart-rate-variability`, `daily-oxygen-saturation`, `daily-respiratory-rate`, `daily-vo2-max`, `daily-sleep-temperature-derivations`

**Get a single point by ID:** `get --id <id>` is supported on `exercise`, `sleep`, `weight`, `body-fat`, `height`, `hydration-log`, `nutrition-log`, `blood-glucose`, `core-body-temperature`, `food`, `food-measurement-unit`.

## Patterns the CLI can't tell you

These require judgment that `--help` and `schema` don't provide.

**Get the user's timezone before querying date-sensitive data:**
```bash
ghealth user settings get   # → timeZone: "Europe/London", utcOffset: "3600s"
# Then use --from/--to with the correct local dates
```
This reports the account timezone for information only — to have the CLI resolve dates in that zone, set it explicitly with `ghealth config set timezone <IANA zone>`.

**Sleep/exercise page size is capped at 25 per request** (auto-paginated by CLI):
```bash
ghealth data sleep list --limit 5    # CLI handles pagination internally
```

**Paging through large `list` results.** `list` returns up to `--limit` rows (default 500). When more exist, the response carries a `nextPageToken` and a hint. Pass it back with `--page-token` to fetch the next page — it resumes exactly where the last page ended, no rows skipped or repeated:
```bash
ghealth data heart-rate list --from 2026-06-15 --limit 500
#   → {"dataPoints":[…500…], "nextPageToken":"ABC", "_hints":[…]}
ghealth data heart-rate list --from 2026-06-15 --limit 500 --page-token ABC
#   → next 500 rows
```

**Correlate heart rate with exercise sessions:**
```bash
# 1. Get exercise time window
ghealth data exercise list --from today --limit 1
#    → start: "2026-03-29T14:18:32+01:00", end: "2026-03-29T14:39:14+01:00"
# 2. Query HR for that window using --filter (raw API syntax, UTC required)
ghealth data heart-rate list --filter 'heart_rate.sample_time.physical_time >= "2026-03-29T13:18:32Z" AND heart_rate.sample_time.physical_time < "2026-03-29T13:40:00Z"'
```

## Exporting data for analysis

**Use `-o <file>` to write data to a file.** When `-o` is set, stdout shows only a summary with the column schema — not the data itself. This means you can fetch data and immediately write analysis code using the column names from stdout, without reading the file.

```bash
ghealth data steps daily-rollup --from 2026-03-24 --to 2026-03-30 --format csv -o steps.csv
```

**What stdout shows** (this is all the agent sees):
```
Wrote 6 rows to steps.csv

Columns: countSum, date
Preview:
countSum,date
4062,2026-03-29
9122,2026-03-28
2469,2026-03-27
```

**What the file contains** (full CSV, not printed to stdout):
```csv
countSum,date
4062,2026-03-29
9122,2026-03-28
2469,2026-03-27
6541,2026-03-26
4025,2026-03-25
3995,2026-03-24
```

The agent now knows the columns are `countSum` and `date`, and can write `pd.read_csv("steps.csv")` without ever reading the file.

**Do not pipe to file** — use `-o` instead. Piping (`> file.csv`) sends the full data to the file but prints nothing to stdout, so the agent has no column schema and must read the file to learn the structure.

More examples:
```bash
# Sleep — nested stageMinutes auto-flattened to stageMinutes.AWAKE, stageMinutes.DEEP, etc.
ghealth data sleep list --from 2026-03-01 --format csv -o sleep.csv

# Exercise — metricsSummary.caloriesKcal, metricsSummary.averageHeartRateBeatsPerMinute, etc.
ghealth data exercise list --from 2026-03-01 --format csv -o exercise.csv

# Heart rate — 500 readings straight to file
ghealth data heart-rate list --from today --limit 500 --format csv -o hr.csv
```

**Exercise time series (GPS/heart-rate track) → CSV.** `export-tcx --as csv` flattens the TCX track to one row per trackpoint — `pd.read_csv` it directly instead of parsing TCX XML:

```bash
# Find the exercise id first, then export its track
ghealth data exercise list --from 2026-06-01 --limit 10
ghealth data exercise export-tcx --id <id> --output ride.csv --as csv   # or --output - for stdout
```

Columns (fixed, stable for dataframes): `time, activity, lap, sport, latitude_deg, longitude_deg, altitude_m, distance_m, heart_rate_bpm, cadence_rpm, speed_mps, watts`. Absent sensors are empty cells (NaN in pandas), never zeros. `distance_m` is cumulative. **0 rows = indoor/no-sensor activity** (Google emits no track for those) — the session summary and workout notes come from `data exercise list`, not the track export.

## Writing data

Writable types: `exercise`, `sleep`, `weight`, `body-fat`, `height`. Writes are async (API returns Operation object).

**Discover the correct payload format** by inspecting a real response with `--raw`:
```bash
ghealth data weight list --raw --limit 1
# Use the response structure as a template for your create payload
```

Write operations use `create`, `update --id <id> [--update-mask fields]`, `delete --ids <ids>`.

## Gotchas

- **Missing days are NOT zeros** (`altitude`, `distance`, `floors`, `steps`, `total-calories`): a date absent from rollup output means the device wasn't worn / didn't sync — NOT zero. `countSum: "0"` is a true zero (worn, no activity). Never coalesce missing buckets to 0 or average over absent days as zeros — that silently deflates weekly/monthly stats
- String vs number values follow protobuf JSON encoding: `int64` fields (`beatsPerMinute`, `countSum`, `minutesAsleep`) are **strings**; `int32`/`double` fields (`weightGrams`, `caloriesKcal`, `percentage`) are **numbers**
- `--filter` raw syntax: only `>=` and `<` comparators. Civil time fields (no `Z`): interval types use `{type}.interval.civil_start_time`, sleep uses `sleep.interval.civil_end_time` (only end-time is filterable), daily types use `{type}.date`. Physical time fields (with `Z`): sample types use `{type}.sample_time.physical_time`
- **Use v4 by default.** Reach for `--api-version v4beta` only to try a feature marked `[beta]` (in `--help`, or `"channel": "v4beta"` in `ghealth schema types`). Beta commands on v4 exit 3 with a hint; JSON responses carry `_api_version` showing which channel answered
- Write operations are **asynchronous** — the API returns an Operation object, not the created/updated data. Use `list` to verify persistence
- Body fat `delete` returns HTTP 500 — this is an API bug
- Height `update` returns HTTP 400 ("updateMask not recognized") — API bug; use `create` + `delete` as a workaround
- `daily-rollup` aggregates by **civil/local day** (1-day windows; override with `--window-days N`). `rollup` aggregates by **physical time** (`--window-size`, default `86400s`); bare `--from`/`--to` dates anchor at midnight in the configured timezone (`ghealth config set timezone <IANA zone>`), falling back to machine-local time when unset. For local-day totals use `daily-rollup`. Both send their window size explicitly — the API rejects requests that omit it
