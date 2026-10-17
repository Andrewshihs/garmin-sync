# garmin-sync

A fork of [lexyurk/garmin-cli](https://github.com/lexyurk/garmin-cli) — a fast,
ergonomic Garmin Connect CLI — adding **Garmin Connect China (garmin.cn) ⇄
Global (garmin.com) region support** and cross-region **activity sync**.

> See [`docs/upstream-README.md`](docs/upstream-README.md) for the full
> documentation of the base CLI this fork builds on.

## Why

Garmin splits accounts into China and international regions that do not see
each other. Workouts recorded through the CN app/account are invisible on a
Global account. This fork makes the CLI region-aware and adds a `garmin sync`
command that copies activities from one region to the other.

## Features added on top of upstream

- **Dual-region support**: every command accepts `--region cn|global`; each
  named profile remembers the region it logged into (`region.json`)
- **`garmin sync`**: copies activities from a source profile/region to a
  target profile/region
  - identity-based dedup plus a persistent sync ledger (no re-uploads, even
    when the target copy is renamed or recreated by other tools)
  - file format fallback: original FIT → TCX → GPX with content sanity
    checks, so imported activities whose original FIT is missing still sync
    (duration/calories/HR survive via TCX laps)
  - duplicate-safe: Garmin 409 "Duplicate Activity" responses are recognised
    as already-synced
  - `--dry-run`, `--limit`, `--delay`, JSON output
- **`training lactate-threshold`**: lactate threshold heart-rate/speed time
  series (`--sport running|cycling`, `--from/--to/--days`, max window 366 days)
- **`weight` enhancements**: body-composition fields (muscle mass, bone mass,
  visceral fat, metabolic age, physique rating) and `weight delete`
- **Well-defined empty states**: exit `2` (`no_data`) / exit `3`
  (`unsupported`) with a JSON `{"reason", "detail"}` object instead of a
  silently empty result; not-authenticated remains exit `1`
- **Robustness**: automatic token refresh per region, retry with backoff for
  transient edge errors, 409-duplicate tolerance, multipart upload content
  checks

## Build

```sh
go build -o garmin ./cmd/garmin
# or
make build
```

Requires Go 1.24+.

Or install directly:

```sh
go install github.com/Andrewshihs/garmin-sync/cmd/garmin@latest
```

## Authentication

```sh
# CN account
garmin auth login --profile cn --region cn --email you@example.com --password-stdin
# Global account
garmin auth login --profile global --region global --email you@example.com --password-stdin
```

Tokens are stored per profile under `~/.config/garmin/tokens/<profile>/`.
Region overrides resolve in this order: explicit flag → `GARMIN_REGION` env →
the region saved for the profile at login → `global`.

## Activity sync

```sh
# Defaults: --from cn --to global, last 30 activities, 5s between uploads
garmin sync

# Explicit
garmin sync --from cn --to global --limit 30 --delay 5

# Preview without uploading
garmin sync --dry-run
```

Activities are matched by name/type/start time; a persistent ledger prevents
re-uploading anything this direction has already handled. Indoor/trackless
activities (strength training, badminton) are synced via their TCX export.

Exit codes: `0` ok (including "nothing to sync"), `1` error/not
authenticated, `2` no data, `3` metric not produced by this device/account.

## Scheduling

A daily systemd user timer works well. See
[`systemd/garmin-sync.service.example`](systemd/garmin-sync.service.example)
(adjust paths/proxy to your environment).

## License

MIT — see [LICENSE](LICENSE). Base CLI Copyright (c) 2026 Alexey Yurkevich;
fork changes Copyright (c) 2026 this repository's contributors.
