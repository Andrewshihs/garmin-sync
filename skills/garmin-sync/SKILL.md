---
name: garmin-sync
description: Query and sync Garmin Connect data via the garmin-sync CLI. Use when the user asks about Garmin activities, health metrics (sleep, heart rate, SpO2, stress, body battery, steps, respiration, intensity minutes), training metrics (readiness, training status, VO2max, HRV, lactate threshold, fitness age, race predictions), weight/body composition, or syncing activities between Garmin Connect regions (China CN and international Global).
---

# Garmin Sync CLI

CLI reference for the `garmin` binary (github.com/Andrewshihs/garmin-sync) —
a Garmin Connect CLI with dual-region (China / Global) support.

## Core model

- Garmin splits accounts into regions: **CN** (garmin.cn) and **Global**
  (garmin.com). They do not see each other's data. Tokens are stored per
  **profile** under `~/.config/garmin/tokens/<profile>/`. Common setup:
  profiles `cn` and `global`.
- Most commands take `-p/--profile <name>` and `--region cn|global`. Query the
  profile that owns the data you need — querying the wrong region returns
  empty results or auth errors without a clear explanation.
- `-f json` on every data command. Prefer JSON for programmatic use; tables
  are for humans.

## Dates (all query commands)

| Flag | Meaning |
| --- | --- |
| `--date YYYY-MM-DD` | single day (default: today) |
| `--from YYYY-MM-DD --to YYYY-MM-DD` | inclusive window |
| `--days N` | last N days ending today |

## Exit codes and empty states (critical)

| Exit | Meaning | JSON shape on stdout |
| --- | --- | --- |
| 0 | data returned (sync: completed, including "nothing to sync") | normal payload |
| 1 | error, including **not authenticated** | error JSON or message on stderr |
| 2 | **no_data** — request valid, zero records for the window | `{"reason": "no_data", "detail": "..."}` |
| 3 | **unsupported** — this account/device does not produce the metric | `{"reason": "unsupported", "detail": "..."}` |

Agent rules:

- Exit 2 is a normal outcome (rest day, metric not yet synced today). Report
  "no data for <window>" — never retry-loop.
- Exit 3 means the device never produces the metric (e.g. no `training
  readiness` on non-supporting watches). Don't ask again for this
  device/metric; suggest checking the device model.
- Exit 1 + "not authenticated" → `garmin auth login --profile <p> --region
  <r>` (interactive MFA; cannot be automated).
- The CLI cannot distinguish "device was off" from "metric missing" — both
  are exit 2.

## Authentication

```sh
garmin auth login --profile cn --region cn --email you@example.com --password-stdin
garmin auth login --profile global --region global --email you@example.com --password-stdin
garmin auth status -p cn --region cn -f json
```

Tokens auto-refresh (about hourly); expired refresh tokens require an
interactive re-login.

## Activities

### `activities list`

| Parameter | Type | Requirement | Description |
| --- | --- | --- | --- |
| `-p/--profile` | string | recommended | Account profile (e.g. `cn`) |
| `--region` | string | recommended | `cn` or `global` |
| `--limit` | int | optional | Max activities (default 20) |
| `--from/--to` | date | optional | Inclusive date window |
| `--days N` | int | optional | Last N days |
| `--type` | string | optional | Activity type filter (running, cycling, …) |
| `-f json` | flag | optional | JSON output |

**Returns:** id, local start time, type, name, distance (km), duration,
calories, average HR.

**Limits and notes:** newest first. Freshly recorded activities can take
minutes-to-hours to appear; "missing today's workout" may be indexing delay.

### Other activity subcommands

| Command | Purpose |
| --- | --- |
| `activities get <id> -f json` | Full detail (raw Garmin JSON) |
| `activities splits <id>` | Lap/split analysis |
| `activities export <id> --type fit\|gpx\|tcx --out FILE` | Download original file |
| `activities upload FILE` | Upload FIT/GPX/TCX to the profile's region |
| `activities weather <id>` | Weather recorded during the activity (temp, apparent temp, humidity, wind, description) |
| `activities update <id> --name "..." --type ...` | Rename / retype |
| `activities delete <id> --force` | Delete (irreversible) |
| `activities weather <id>` | Weather recorded during the activity |

## Health (per-day metrics; all accept the date flags and `-f json`)

| Command | Fields returned |
| --- | --- |
| `health sleep` | score, total/deep/light/rem/awake seconds, avg SpO2, avg respiration, sleep start/end |
| `health heart-rate` | resting, min, max |
| `health spo2` | average, lowest, latest |
| `health respiration` | avg waking, highest, lowest |
| `health stress` | average, max, qualifier (`--values` adds the intraday timeline as JSON) |
| `health body-battery` | highest, lowest, most recent, charged, drained |
| `health steps` | steps, goal, distance |
| `health intensity-minutes` | moderate, vigorous, weekly goal |
| `health summary` | steps, resting HR, stress, body battery in one payload |

## Training

| Command | Fields returned |
| --- | --- |
| `training readiness` | score, level, sleep score, HRV weekly avg, acute load, recovery time |
| `training status` | status phrase/id, weekly training load, trend (`"—"` phrase = no data) |
| `training hrv` | last night avg, weekly avg, status, baseline |
| `training vo2-max` (alias `vo2max`) | running, cycling |
| `training fitness-age` | fitness age estimate |
| `training race-predictions` | predicted 5K/10K/half/marathon times |
| `training lactate-threshold [--sport running\|cycling]` | LTHR (bpm) and threshold speed time series; API caps the window at 366 days |

## Weight

| Command | Purpose |
| --- | --- |
| `weight list --days 30 -f json` | weigh-ins incl. weight/BMI/body fat/muscle/bone/visceral fat/metabolic age/source |
| `weight latest` | most recent weigh-in |
| `weight log 72.5 --date YYYY-MM-DD` | add a weigh-in |
| `weight delete --sample-pk N --date YYYY-MM-DD --force` | delete (irreversible; requires exact pk+date pair) |

## Sync (CN → Global activity copy)

| Parameter | Type | Requirement | Description |
| --- | --- | --- | --- |
| `--source` | string | optional | Source profile (default `cn`) |
| `--target` | string | optional | Target profile (default `global`) |
| `--source-region/--target-region` | string | optional | Region override |
| `--limit` | int | optional | Newest N activities to check (default 30 on schedulers) |
| `--delay` | int | optional | Seconds between uploads |
| `--dry-run` | flag | optional | List what would be uploaded, touch nothing |
| `--notify-bark KEY` | string | optional | Phone push on failures (api.day.app) |

**Returns (JSON):** counts plus `successes` (source/target IDs) and `failures`
(activity, reason).

**Limits and notes:**

- Identity = name + type + start time; a persistent ledger also remembers
  everything already handled, so re-running is always safe and duplicates
  (including target-side copies made by other tools) are skipped.
- Activities missing their original FIT sync via TCX/GPX fallback. On the
  target those copies may show type `other` and an auto-derived name.
- "No new activities to sync" + exit 0 = steady state, not an error.
- Ignore the global `-p/--profile` and `--region` flags here — sync defines
  its own direction flags.

## Outdoor analysis: factor in weather

Pace and heart rate are strongly affected by conditions. Before attributing a
pace/HR change to fitness, check the weather recorded during each outdoor
activity:

```sh
garmin activities weather <activity-id> -p cn --region cn -f json
```

**Returns:** `temp`, `apparent_temp` (feels-like), `relative_humidity`,
`wind_speed`, `wind_direction`, `description` (e.g. clear/rain).

Agent guidance:

- Apparent temperature ≥ ~25 °C with high humidity typically adds 10–25 bpm
  at the same pace (cardiac drift) — a "slower" run in heat is not necessarily
  lost fitness.
- Strong headwind effectively adds pace cost that GPS pace does not explain.
- Indoor/treadmill activities return no weather — treat missing weather as
  "unknown conditions", not an error.
- When comparing two activities, fetch weather for BOTH before drawing
  conclusions.

## Known data quirks

- Fresh activities may take minutes-to-hours to appear in list APIs.
- Metrics the device doesn't produce (e.g. readiness on some watches) return
  exit 2/3 forever — see the exit-code table.
- Lactate threshold history needs the ecosystem to have computed it; empty on
  fresh accounts.
- Training status uses a literal `"—"` phrase for days without data.
- Health payloads store Garmin's raw camelCase keys: in `cache query` use
  `json_extract(payload, '$.restingHeartRate')`, not snake_case.

## SQL tips (`garmin cache query`)

```sql
-- 7-day resting HR trend
SELECT date, json_extract(payload, '$.restingHeartRate') AS resting_hr
FROM samples WHERE profile = 'cn' AND metric = 'daily_summary' ORDER BY date;

-- weekly running volume
SELECT type, COUNT(*) AS n, ROUND(SUM(json_extract(payload, '$.distance_meters'))/1000.0, 1) AS km
FROM activities WHERE profile = 'cn' GROUP BY type ORDER BY km DESC;

-- sleep score trend (minutes)
SELECT date, json_extract(payload, '$.score') AS score,
       json_extract(payload, '$.total_sleep_seconds')/60.0 AS sleep_min
FROM samples WHERE metric = 'sleep' ORDER BY date;
```
