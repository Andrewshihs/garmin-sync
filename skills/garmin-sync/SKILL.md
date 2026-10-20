---
name: garmin-sync
description: Query and sync Garmin Connect data via the garmin-sync CLI. Use when the user asks about Garmin activities, health metrics (sleep, heart rate, SpO2, stress, body battery, steps, respiration, intensity minutes), training metrics (readiness, VO2max, HRV, lactate threshold, training status), weight/body composition, or syncing activities between Garmin Connect regions (China CN and international Global).
---

# Garmin Sync CLI

Wrapper knowledge for the `garmin` binary (github.com/Andrewshihs/garmin-sync) —
a Garmin Connect CLI with dual-region (China / Global) support.

## Core model

- Garmin splits accounts into regions: **CN** (garmin.cn) and **Global**
  (garmin.com). Tokens are stored per **profile** under
  `~/.config/garmin/tokens/<profile>/`. Common setup: profiles `cn` and
  `global`.
- Most data commands accept `-p/--profile <name>` and `--region cn|global`.
  Pick the profile that owns the data you need — querying the wrong region
  yields empty results or auth errors, not a clear message.
- JSON output everywhere via `-f json`. Human tables otherwise.

## Exit codes and empty states (important)

| Exit | Meaning | JSON shape |
| --- | --- | --- |
| 0 | data returned (or sync completed / "nothing to sync") | normal payload |
| 1 | error, including **not authenticated** | `{"message": "not authenticated ...", ...}` |
| 2 | **no_data** — request valid, zero records for the window | `{"reason": "no_data", "detail": "..."}` |
| 3 | **unsupported** — this account/device does not produce the metric | `{"reason": "unsupported", "detail": "..."}` |

Rules for agents:

- Exit 2 is a **normal outcome** (e.g. rest day → no activity; metric not yet
  synced today). Never retry-loop on it; report "no data for <window>".
- Exit 3 means don't ask again for this device/metric (e.g. some devices never
  produce `training readiness`). Suggest checking the device model instead of
  re-querying.
- Exit 1 with "not authenticated" → run `garmin auth login --profile <p>
  --region <r>` (interactive MFA may be required; cannot be automated).
- Do not distinguish "device off today" from "metric missing" — the CLI cannot
  know; both are exit 2.

## Command map

```sh
# Activities (list/get/splits/export/upload/update/delete/weather)
garmin activities list -p cn --region cn --days 7 -f json
garmin activities get -p cn --region cn <activity-id> -f json

# Health (all support --date / --from --to / --days)
garmin health sleep -p cn --region cn --days 7 -f json
garmin health heart-rate -p cn --region cn --date 2026-01-15
garmin health spo2|stress|body-battery|steps|respiration|intensity-minutes|summary ...

# Training
garmin training readiness|status|hrv -p cn --region cn --days 7 -f json
garmin training vo2-max -p cn --region cn          # alias: vo2max
garmin training lactate-threshold -p cn --region cn --sport running -f json

# Weight (body composition fields included)
garmin weight list -p cn --region cn --days 30 -f json
garmin weight latest -p cn --region cn

# Sync activities CN -> Global (see below)
garmin sync --source cn --target global --dry-run
```

Notes:

- Date flags: `--date` (single day) or `--from/--to` (inclusive) or `--days N`
  (last N days). All accept `-f json`.
- `training lactate-threshold`: window capped at 366 days by the API.
- Raw API passthroughs (e.g. `activities get -f json`) return Garmin's own
  camelCase field names; the parsed summaries use snake_case.

## Sync semantics (garmin sync)

- `--source/--target` name **profiles** (not regions). Regions resolve from
  each profile's saved login region; `--source-region/--target-region`
  override.
- Direction is persistent-stateful: a ledger records every source activity
  already handled. Re-running is always safe — it uploads only what the target
  is missing, then exits 0 with "No new activities to sync". Never expect or
  force a re-sync of a specific past activity.
- Activities missing their original FIT (common for app-imported workouts) are
  synced via TCX/GPX fallback. On the target side those copies may show type
  `other` and an auto-derived name — expected.
- `--dry-run` lists what would be uploaded without touching the target.

## Known data quirks

- Freshly recorded activities may take minutes-to-hours to appear in list
  APIs; a "missing today's workout" answer may just be indexing delay.
- Devices that don't produce a metric return exit 2/3 forever (e.g. no
  readiness on non-supporting watches) — see exit-code table.
- Lactate threshold history only exists if the ecosystem computed it; empty on
  fresh accounts.
