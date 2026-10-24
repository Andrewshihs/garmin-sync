package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	garminactivities "github.com/Andrewshihs/garmin-sync/internal/activities"
	"github.com/Andrewshihs/garmin-sync/internal/cache"
	"github.com/Andrewshihs/garmin-sync/internal/config"
	garminhealth "github.com/Andrewshihs/garmin-sync/internal/health"
	"github.com/Andrewshihs/garmin-sync/internal/output"
	"github.com/Andrewshihs/garmin-sync/internal/timeutil"
	garmintraining "github.com/Andrewshihs/garmin-sync/internal/training"
	garminweight "github.com/Andrewshihs/garmin-sync/internal/weight"
	"github.com/spf13/cobra"
)

const cacheDBFileName = "cache.sqlite3"

func cacheDBPath(cfgDir string) string {
	return cfgDir + "/" + cacheDBFileName
}

func NewCacheCmd(opts *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Local SQLite cache for trend analysis",
		Long: `Fetch health/training/activity data into a local SQLite database so
agents and scripts can analyze trends with plain SQL instead of hitting the
API repeatedly.

Typical flow:

  garmin cache backfill -p cn --region cn --days 90
  garmin cache status -p cn
  garmin cache query "SELECT date, json_extract(payload, '$.resting_heart_rate')
                       FROM samples WHERE metric = 'daily_summary' ORDER BY date"`,
	}
	cmd.AddCommand(newCacheBackfillCmd(opts), newCacheStatusCmd(opts), newCacheQueryCmd(opts))
	return cmd
}

func newCacheBackfillCmd(opts *globalOptions) *cobra.Command {
	var date, from, to string
	var days int
	var metricsFlag string

	cmd := &cobra.Command{
		Use:   "backfill",
		Short: "Fetch data for a date window into the local cache",
		RunE: func(cmd *cobra.Command, args []string) error {
			dates, err := timeutil.ResolveDates(timeutil.RangeOptions{Date: date, From: from, To: to, Days: days}, time.Now())
			if err != nil {
				return err
			}

			c, err := newQueryClient(cmd, opts, true)
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			cfgDir, err := config.ResolveConfigDir(opts.ConfigDir)
			if err != nil {
				return err
			}
			db, err := cache.Open(cacheDBPath(cfgDir))
			if err != nil {
				return err
			}
			defer db.Close()

			profile := orDefault(opts.Profile, "default")
			region := "global"
			if r, err := regionForCmd(cmd, opts, cfgDir); err == nil && r != "" {
				region = string(r)
			}
			fetchedAt := time.Now()

			dateMetrics := []struct {
				label  string
				metric string
				fetch  func(context.Context, string) (any, error)
			}{
				{"daily summary", "daily_summary", wrapHealth(func(d string) (garminhealth.DailySummary, error) { return garminhealth.GetDailySummary(ctx, c, d) },
					func(s garminhealth.DailySummary) (any, bool) {
						return s, s.TotalSteps != nil || s.RestingHeartRate != nil || s.AverageStressLevel != nil || s.BodyBatteryHighestValue != nil
					})},
				{"sleep", "sleep", wrapHealth(func(d string) (garminhealth.SleepSummary, error) { return garminhealth.GetSleep(ctx, c, d) },
					func(s garminhealth.SleepSummary) (any, bool) {
						return s, s.Score != nil || s.TotalSleepSeconds != nil
					})},
				{"SpO2", "spo2", wrapHealth(func(d string) (garminhealth.SpO2Summary, error) { return garminhealth.GetSpO2(ctx, c, d) },
					func(s garminhealth.SpO2Summary) (any, bool) {
						return s, s.Average != nil || s.Lowest != nil || s.Latest != nil
					})},
				{"training readiness", "readiness", wrapTraining(func(d string) (garmintraining.ReadinessSummary, error) { return garmintraining.GetReadiness(ctx, c, d) },
					func(s garmintraining.ReadinessSummary) (any, bool) {
						return s, s.Score != nil
					})},
				{"HRV", "hrv", wrapTraining(func(d string) (garmintraining.HRVSummary, error) { return garmintraining.GetHRV(ctx, c, d) },
					func(s garmintraining.HRVSummary) (any, bool) {
						return s, s.LastNightAvg != nil || s.WeeklyAvg != nil
					})},
			}

			want := map[string]bool{}
			if strings.TrimSpace(metricsFlag) != "" {
				for _, m := range strings.Split(metricsFlag, ",") {
					want[strings.TrimSpace(strings.ToLower(m))] = true
				}
			}

			inserted := 0
			for _, dm := range dateMetrics {
				if len(want) > 0 && !want[dm.metric] {
					continue
				}
				results, err := mapDatesConcurrently(ctx, dates, 4, func(ctx context.Context, d string) (any, error) {
					return dm.fetch(ctx, d)
				})
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warn: %s: %v\n", dm.label, err)
					continue
				}
				n := 0
				for i, payload := range results {
					if payload == nil {
						continue
					}
					if err := cache.UpsertSample(db, cache.Sample{
						Profile: profile, Region: region, Metric: dm.metric,
						Date: dates[i], Payload: payload, FetchedAt: fetchedAt,
					}); err == nil {
						n++
					}
				}
				inserted += n
				fmt.Fprintf(cmd.ErrOrStderr(), "%s: %d days cached\n", dm.label, n)
			}

			start, end := dates[0], dates[len(dates)-1]

			// Range-based sources: activities and weight.
			if len(want) == 0 || want["activities"] {
				acts, err := garminactivities.List(ctx, c, 200, start, end, "")
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warn: activities: %v\n", err)
				} else {
					n := 0
					for _, a := range acts {
						// Weather is best-effort: indoor/imported activities
						// may not have any recorded conditions.
						var weather string
						if w, werr := garminactivities.GetWeather(ctx, c, a.ID); werr == nil {
							if wb, merr := json.Marshal(w); merr == nil {
								weather = string(wb)
							}
						}
						ar := cache.ActivityRow{
							Profile: profile, ActivityID: a.ID, StartTimeLocal: a.StartTimeLocal,
							Type: a.Type, Name: a.Name, DistanceMeters: a.DistanceMeters,
							DurationSeconds: a.DurationSeconds, Calories: a.Calories, AvgHR: a.AvgHR,
							Weather: weather, Payload: a, FetchedAt: fetchedAt,
						}
						if err := cache.UpsertActivity(db, ar); err == nil {
							n++
						}
					}
					inserted += n
					fmt.Fprintf(cmd.ErrOrStderr(), "activities: %d cached\n", n)
				}
			}
			if len(want) == 0 || want["weight"] {
				ws, err := garminweight.List(ctx, c, start, end)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warn: weight: %v\n", err)
				} else {
					n := 0
					for _, w := range ws {
						payloadJSON, _ := json.Marshal(w)
						if err := cache.UpsertSample(db, cache.Sample{
							Profile: profile, Region: region, Metric: "weight",
							Date: w.Date, Payload: json.RawMessage(payloadJSON), FetchedAt: fetchedAt,
						}); err == nil {
							n++
						}
					}
					inserted += n
					fmt.Fprintf(cmd.ErrOrStderr(), "weight: %d cached\n", n)
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "✓ %d samples cached (%s..%s)\n", inserted, dates[0], dates[len(dates)-1])
			return nil
		},
	}
	addDateRangeFlags(cmd, &date, &from, &to, &days)
	cmd.Flags().StringVar(&metricsFlag, "metrics", "", "Comma-separated subset of metrics (default: all)")
	return cmd
}

// wrapHealth adapts a per-date health fetch to the cache backfill: payloads
// without any actual readings come back as nil (skipped, not cached).
func wrapHealth[T any](fetch func(string) (T, error), hasData func(T) (any, bool)) func(context.Context, string) (any, error) {
	return func(ctx context.Context, date string) (any, error) {
		v, err := fetch(date)
		if err != nil {
			return nil, err
		}
		payload, ok := hasData(v)
		if !ok {
			return nil, nil
		}
		return payload, nil
	}
}

// wrapTraining is wrapHealth for training-domain summaries.
func wrapTraining[T any](fetch func(string) (T, error), hasData func(T) (any, bool)) func(context.Context, string) (any, error) {
	return func(ctx context.Context, date string) (any, error) {
		v, err := fetch(date)
		if err != nil {
			return nil, err
		}
		payload, ok := hasData(v)
		if !ok {
			return nil, nil
		}
		return payload, nil
	}
}

func newCacheStatusCmd(opts *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show cached coverage per metric",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgDir, err := config.ResolveConfigDir(opts.ConfigDir)
			if err != nil {
				return err
			}
			db, err := cache.Open(cacheDBPath(cfgDir))
			if err != nil {
				return err
			}
			defer db.Close()

			profile := orDefault(opts.Profile, "default")
			statuses, err := cache.Status(db, profile)
			if err != nil {
				return err
			}
			if opts.Format == "json" {
				return output.JSONTo(cmd.OutOrStdout(), statuses)
			}
			if len(statuses) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Cache is empty. Run `garmin cache backfill` first.")
				return nil
			}
			rows := make([][]string, 0, len(statuses))
			for _, s := range statuses {
				rows = append(rows, []string{s.Metric, fmt.Sprintf("%d", s.Count), s.FirstDay, s.LastDay})
			}
			return renderTableTo(cmd.OutOrStdout(), opts.Format, []string{"metric", "samples", "first_day", "last_day"}, rows)
		},
	}
	return cmd
}

func newCacheQueryCmd(opts *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "query [sql]",
		Short: "Run a read-only SQL query against the cache",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgDir, err := config.ResolveConfigDir(opts.ConfigDir)
			if err != nil {
				return err
			}
			// The SELECT/WITH whitelist in cache.Query is the read-only guard;
			// mode=ro is avoided because WAL readers need WAL write access.
			db, err := sql.Open("sqlite", "file:"+cacheDBPath(cfgDir))
			if err != nil {
				return err
			}
			defer db.Close()

			cols, rows, err := cache.Query(db, args[0])
			if err != nil {
				return err
			}
			if opts.Format == "json" {
				objects := make([]map[string]any, 0, len(rows))
				for _, r := range rows {
					obj := map[string]any{}
					for i, c := range cols {
						obj[c] = r[i]
					}
					objects = append(objects, obj)
				}
				return output.JSONTo(cmd.OutOrStdout(), objects)
			}
			tableRows := make([][]string, 0, len(rows))
			for _, r := range rows {
				cells := make([]string, len(r))
				for i, v := range r {
					switch v := v.(type) {
					case nil:
						cells[i] = "—"
					case []byte:
						cells[i] = string(v)
					default:
						cells[i] = fmt.Sprintf("%v", v)
					}
				}
				tableRows = append(tableRows, cells)
			}
			return renderTableTo(cmd.OutOrStdout(), opts.Format, cols, tableRows)
		},
	}
	return cmd
}
