package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Andrewshihs/garmin-sync/internal/client"
	"github.com/Andrewshihs/garmin-sync/internal/output"
	"github.com/Andrewshihs/garmin-sync/internal/profile"
	"github.com/Andrewshihs/garmin-sync/internal/timeutil"
	garmintraining "github.com/Andrewshihs/garmin-sync/internal/training"
	"github.com/spf13/cobra"
)

func NewTrainingCmd(opts *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "training",
		Short: "Training metrics (status, VO2max, HRV, readiness)",
	}

	cmd.AddCommand(
		newTrainingStatusCmd(opts),
		newTrainingReadinessCmd(opts),
		newTrainingVo2maxCmd(opts),
		newTrainingHrvCmd(opts),
		newTrainingLactateThresholdCmd(opts),
		newTrainingFitnessAgeCmd(opts),
		newTrainingRacePredictionsCmd(opts),
	)

	return cmd
}

func newTrainingRacePredictionsCmd(opts *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "race-predictions",
		Short: "Predicted race times (5K, 10K, half, marathon)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAuthedClient(cmd, opts)
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			p, err := profile.Get(ctx, c)
			if err != nil {
				return handleAuthedErrorTo(cmd.ErrOrStderr(), opts, err)
			}

			rp, err := garmintraining.GetRacePredictions(ctx, c, p.DisplayName)
			if err != nil {
				return handleAuthedErrorTo(cmd.ErrOrStderr(), opts, err)
			}

			if opts.Format == "json" {
				return output.JSONTo(cmd.OutOrStdout(), rp)
			}
			return renderKVTo(cmd.OutOrStdout(), opts.Format, "Race predictions", map[string]string{
				"date":     orDash(rp.CalendarDate),
				"5k":       formatSecondsClock(rp.Time5KSeconds),
				"10k":      formatSecondsClock(rp.Time10KSeconds),
				"half":     formatSecondsClock(rp.TimeHalfSeconds),
				"marathon": formatSecondsClock(rp.TimeMarathonSeconds),
			})
		},
	}
	return cmd
}

func formatSecondsClock(sec *int) string {
	if sec == nil || *sec <= 0 {
		return "—"
	}
	s := *sec
	h := s / 3600
	m := (s % 3600) / 60
	ss := s % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, ss)
	}
	return fmt.Sprintf("%d:%02d", m, ss)
}

func newTrainingFitnessAgeCmd(opts *globalOptions) *cobra.Command {
	var date string

	cmd := &cobra.Command{
		Use:   "fitness-age",
		Short: "Fitness age (a.k.a. health age) estimate",
		RunE: func(cmd *cobra.Command, args []string) error {
			d := strings.TrimSpace(date)
			if d == "" {
				d = time.Now().In(time.Local).Format("2006-01-02")
			} else if _, err := time.ParseInLocation("2006-01-02", d, time.Local); err != nil {
				return fmt.Errorf("invalid --date %q (expected YYYY-MM-DD)", date)
			}

			c, err := newAuthedClient(cmd, opts)
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			fa, err := garmintraining.GetFitnessAge(ctx, c, d)
			if err != nil {
				return handleAuthedErrorTo(cmd.ErrOrStderr(), opts, err)
			}

			if opts.Format == "json" {
				return output.JSONTo(cmd.OutOrStdout(), fa)
			}

			fields := map[string]string{
				"date":                   fa.Date,
				"fitness_age":            formatMaybeFloat(fa.FitnessAge, 1),
				"chronological_age":      formatMaybeFloat(fa.ChronologicalAge, 1),
				"achievable_fitness_age": formatMaybeFloat(fa.AchievableAge, 1),
				"previous_fitness_age":   formatMaybeFloat(fa.PreviousAge, 1),
			}
			for k, v := range fa.Components {
				fields["component_"+k] = strconv.FormatFloat(v, 'f', -1, 64)
			}
			return renderKVTo(cmd.OutOrStdout(), opts.Format, "Fitness age", fields)
		},
	}
	cmd.Flags().StringVar(&date, "date", "", "Date (YYYY-MM-DD, default: today)")
	return cmd
}

func newTrainingStatusCmd(opts *globalOptions) *cobra.Command {
	var date string
	var from string
	var to string
	var days int

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Training status (Productive, Peaking, etc.)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAuthedClient(cmd, opts)
			if err != nil {
				return err
			}

			dates, err := timeutil.ResolveDates(timeutil.RangeOptions{Date: date, From: from, To: to, Days: days}, time.Now())
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			results, err := mapDatesConcurrently(ctx, dates, 4, func(ctx context.Context, date string) (garmintraining.StatusSummary, error) {
				return garmintraining.GetStatus(ctx, c, date)
			})
			if err != nil {
				return handleAuthedErrorTo(cmd.ErrOrStderr(), opts, err)
			}

			hasData := false
			for _, r := range results {
				// Garmin fills status_phrase with a "—" placeholder on days
				// without data — treat it as empty.
				if r.StatusID != nil || r.WeeklyTrainingLoad != nil || (r.StatusPhrase != "" && r.StatusPhrase != "—") {
					hasData = true
					break
				}
			}
			if !hasData {
				return emitEmpty(cmd, opts, EmptyReasonNoData, fmt.Sprintf("no training status data for %s", dateRangeDetail(dates)))
			}

			if opts.Format == "json" {
				return output.JSONTo(cmd.OutOrStdout(), results)
			}
			if len(results) == 1 {
				r := results[0]
				return renderKVTo(cmd.OutOrStdout(), opts.Format, "Training status", map[string]string{
					"date":        r.Date,
					"status":      r.StatusPhrase,
					"status_id":   formatMaybeInt(r.StatusID),
					"weekly_load": formatMaybeFloatZeroAsDash(r.WeeklyTrainingLoad, 0),
					"trend":       r.LoadLevelTrend,
				})
			}
			rows := make([][]string, 0, len(results))
			for _, r := range results {
				rows = append(rows, []string{
					r.Date,
					r.StatusPhrase,
					formatMaybeInt(r.StatusID),
					formatMaybeFloatZeroAsDash(r.WeeklyTrainingLoad, 0),
					r.LoadLevelTrend,
				})
			}
			return renderTableTo(cmd.OutOrStdout(), opts.Format, []string{"date", "status", "status_id", "weekly_load", "trend"}, rows)
		},
	}
	cmd.Flags().StringVar(&date, "date", "", "Date (YYYY-MM-DD, default: today)")
	cmd.Flags().StringVar(&from, "from", "", "Start date (YYYY-MM-DD, inclusive)")
	cmd.Flags().StringVar(&to, "to", "", "End date (YYYY-MM-DD, inclusive)")
	cmd.Flags().IntVar(&days, "days", 0, "Shortcut: last N days (ending today)")
	return cmd
}

func newTrainingReadinessCmd(opts *globalOptions) *cobra.Command {
	var date string
	var from string
	var to string
	var days int

	cmd := &cobra.Command{
		Use:   "readiness",
		Short: "Training readiness score (0-100)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAuthedClient(cmd, opts)
			if err != nil {
				return err
			}

			dates, err := timeutil.ResolveDates(timeutil.RangeOptions{Date: date, From: from, To: to, Days: days}, time.Now())
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			results, err := mapDatesConcurrently(ctx, dates, 4, func(ctx context.Context, date string) (garmintraining.ReadinessSummary, error) {
				return garmintraining.GetReadiness(ctx, c, date)
			})
			if err != nil {
				var se *client.StatusError
				if errors.As(err, &se) && se.StatusCode == http.StatusNotFound {
					// The metric endpoint 404s for accounts/devices that do not
					// produce readiness at all — distinct from a day with no data.
					return emitEmpty(cmd, opts, EmptyReasonUnsupported, "training readiness is not available for this account/device")
				}
				return handleAuthedErrorTo(cmd.ErrOrStderr(), opts, err)
			}

			hasData := false
			for _, r := range results {
				if r.Score != nil {
					hasData = true
					break
				}
			}
			if !hasData {
				if len(dates) > 1 {
					return emitEmpty(cmd, opts, EmptyReasonUnsupported,
						fmt.Sprintf("no readiness data across %s..%s (%d days); the device may not produce this metric", dates[0], dates[len(dates)-1], len(dates)))
				}
				return emitEmpty(cmd, opts, EmptyReasonNoData, "no readiness data for "+dates[0])
			}

			if opts.Format == "json" {
				return output.JSONTo(cmd.OutOrStdout(), results)
			}
			if len(results) == 1 {
				r := results[0]
				return renderKVTo(cmd.OutOrStdout(), opts.Format, "Training readiness", map[string]string{
					"date":            r.Date,
					"score":           formatMaybeInt(r.Score),
					"level":           r.Level,
					"sleep_score":     formatMaybeInt(r.SleepScore),
					"hrv_weekly_avg":  formatMaybeInt(r.HRVWeeklyAverage),
					"acute_load":      formatMaybeInt(r.AcuteLoad),
					"recovery_time_s": formatMaybeInt(r.RecoveryTime),
				})
			}

			rows := make([][]string, 0, len(results))
			for _, r := range results {
				rows = append(rows, []string{
					r.Date,
					formatMaybeInt(r.Score),
					r.Level,
					formatMaybeInt(r.SleepScore),
					formatMaybeInt(r.HRVWeeklyAverage),
					formatMaybeInt(r.AcuteLoad),
				})
			}
			return renderTableTo(cmd.OutOrStdout(), opts.Format, []string{"date", "score", "level", "sleep_score", "hrv_weekly_avg", "acute_load"}, rows)
		},
	}
	cmd.Flags().StringVar(&date, "date", "", "Date (YYYY-MM-DD, default: today)")
	cmd.Flags().StringVar(&from, "from", "", "Start date (YYYY-MM-DD, inclusive)")
	cmd.Flags().StringVar(&to, "to", "", "End date (YYYY-MM-DD, inclusive)")
	cmd.Flags().IntVar(&days, "days", 0, "Shortcut: last N days (ending today)")
	return cmd
}

func newTrainingVo2maxCmd(opts *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vo2max",
		Short: "VO2 max estimates",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAuthedClient(cmd, opts)
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			vo2, err := garmintraining.GetVO2Max(ctx, c)
			if err != nil {
				return handleAuthedErrorTo(cmd.ErrOrStderr(), opts, err)
			}

			if opts.Format == "json" {
				return output.JSONTo(cmd.OutOrStdout(), vo2maxJSON{
					Running: vo2.Running,
					Cycling: vo2.Cycling,
				})
			}
			return renderKVTo(cmd.OutOrStdout(), opts.Format, "VO2 max", map[string]string{
				"running": formatMaybeFloat0(vo2.Running, 1),
				"cycling": formatMaybeFloat0(vo2.Cycling, 1),
			})
		},
	}
	return cmd
}

func newTrainingHrvCmd(opts *globalOptions) *cobra.Command {
	var date string
	var from string
	var to string
	var days int

	cmd := &cobra.Command{
		Use:   "hrv",
		Short: "Heart rate variability",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAuthedClient(cmd, opts)
			if err != nil {
				return err
			}

			dates, err := timeutil.ResolveDates(timeutil.RangeOptions{Date: date, From: from, To: to, Days: days}, time.Now())
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			results, err := mapDatesConcurrently(ctx, dates, 4, func(ctx context.Context, date string) (garmintraining.HRVSummary, error) {
				return garmintraining.GetHRV(ctx, c, date)
			})
			if err != nil {
				return handleAuthedErrorTo(cmd.ErrOrStderr(), opts, err)
			}

			hasData := false
			for _, r := range results {
				if r.LastNightAvg != nil || r.WeeklyAvg != nil {
					hasData = true
					break
				}
			}
			if !hasData {
				return emitEmpty(cmd, opts, EmptyReasonNoData, fmt.Sprintf("no HRV data for %s", strings.Join(dates, ", ")))
			}

			if opts.Format == "json" {
				return output.JSONTo(cmd.OutOrStdout(), results)
			}
			if len(results) == 1 {
				r := results[0]
				return renderKVTo(cmd.OutOrStdout(), opts.Format, "HRV", map[string]string{
					"date":           r.Date,
					"status":         r.Status,
					"weekly_avg":     formatMaybeInt(r.WeeklyAvg),
					"last_night_avg": formatMaybeInt(r.LastNightAvg),
					"baseline_low":   formatMaybeFloatZeroAsDash(r.BaselineLowUpper, 0),
					"baseline_high":  formatMaybeFloatZeroAsDash(r.BaselineBalancedUpper, 0),
				})
			}
			rows := make([][]string, 0, len(results))
			for _, r := range results {
				rows = append(rows, []string{
					r.Date,
					r.Status,
					formatMaybeInt(r.WeeklyAvg),
					formatMaybeInt(r.LastNightAvg),
				})
			}
			return renderTableTo(cmd.OutOrStdout(), opts.Format, []string{"date", "status", "weekly_avg", "last_night_avg"}, rows)
		},
	}
	cmd.Flags().StringVar(&date, "date", "", "Date (YYYY-MM-DD, default: today)")
	cmd.Flags().StringVar(&from, "from", "", "Start date (YYYY-MM-DD, inclusive)")
	cmd.Flags().StringVar(&to, "to", "", "End date (YYYY-MM-DD, inclusive)")
	cmd.Flags().IntVar(&days, "days", 0, "Shortcut: last N days (ending today)")
	return cmd
}

type vo2maxJSON struct {
	Running float64 `json:"running"`
	Cycling float64 `json:"cycling"`
}

type lactateThresholdJSON struct {
	Sport     string                         `json:"sport"`
	From      string                         `json:"from"`
	To        string                         `json:"to"`
	HeartRate []garmintraining.LactateSample `json:"heart_rate"`
	Speed     []garmintraining.LactateSample `json:"speed"`
}

// Lactate threshold speed values behave like km/min; surface the implied
// pace for readability alongside the raw value.
func formatLactatePace(speed float64) string {
	if speed <= 0 {
		return "—"
	}
	secPerKM := 60 / speed
	m := int(secPerKM) / 60
	s := int(secPerKM) % 60
	return fmt.Sprintf("%d:%02d /km", m, s)
}

func newTrainingLactateThresholdCmd(opts *globalOptions) *cobra.Command {
	var sport string
	var from string
	var to string
	var days int

	cmd := &cobra.Command{
		Use:   "lactate-threshold",
		Short: "Lactate threshold heart rate & speed history",
		Long: `Lactate threshold heart rate and speed time series.

Values come from Garmin's biometric stats and only exist where a watch or
manual test recorded them (they are typically absent on freshly linked
accounts). The underlying endpoint rejects windows longer than 366 days.
The speed unit is undocumented; the reported raw value empirically behaves
like km/min, so an implied pace is shown next to it.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := garmintraining.NormalizeLactateSport(sport)
			if err != nil {
				return fmt.Errorf("invalid --sport: %w", err)
			}

			now := time.Now()
			today := now.Format("2006-01-02")
			if days > 0 {
				if from != "" || to != "" {
					return fmt.Errorf("use --days or --from/--to, not both")
				}
				to = today
				from = now.AddDate(0, 0, -(days - 1)).Format("2006-01-02")
			}
			if from == "" && to == "" {
				from = now.AddDate(0, 0, -364).Format("2006-01-02")
				to = today
			}
			if from == "" || to == "" {
				return fmt.Errorf("--from and --to must be given together")
			}
			fd, err := time.Parse("2006-01-02", from)
			if err != nil {
				return fmt.Errorf("invalid --from %q (expected YYYY-MM-DD)", from)
			}
			td, err := time.Parse("2006-01-02", to)
			if err != nil {
				return fmt.Errorf("invalid --to %q (expected YYYY-MM-DD)", to)
			}
			if td.Before(fd) {
				return fmt.Errorf("--to %s is before --from %s", to, from)
			}
			if int(td.Sub(fd).Hours()/24)+1 > 366 {
				return fmt.Errorf("date range exceeds 366 days (Garmin endpoint limit)")
			}

			c, err := newAuthedClient(cmd, opts)
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			hr, err := garmintraining.LactateThresholdHeartRate(ctx, c, from, to, s)
			if err != nil {
				return handleAuthedErrorTo(cmd.ErrOrStderr(), opts, err)
			}
			speed, err := garmintraining.LactateThresholdSpeed(ctx, c, from, to, s)
			if err != nil {
				return handleAuthedErrorTo(cmd.ErrOrStderr(), opts, err)
			}

			if opts.Format == "json" {
				return output.JSONTo(cmd.OutOrStdout(), lactateThresholdJSON{
					Sport: s, From: from, To: to, HeartRate: hr, Speed: speed,
				})
			}

			if len(hr) == 0 && len(speed) == 0 {
				return emitEmpty(cmd, opts, EmptyReasonNoData, fmt.Sprintf("no lactate threshold data for %s (%s..%s)", strings.ToLower(s), from, to))
			}

			// Merge both series into one per-date table.
			type lactateRow struct{ hr, speed, pace string }
			byDate := map[string]lactateRow{}
			dateKeys := make([]string, 0, len(hr)+len(speed))
			for _, smp := range hr {
				if _, ok := byDate[smp.From]; !ok {
					dateKeys = append(dateKeys, smp.From)
				}
				r := byDate[smp.From]
				r.hr = formatMaybeInt0(int(smp.Value))
				byDate[smp.From] = r
			}
			for _, smp := range speed {
				if _, ok := byDate[smp.From]; !ok {
					dateKeys = append(dateKeys, smp.From)
				}
				r := byDate[smp.From]
				r.speed = strconv.FormatFloat(smp.Value, 'f', 4, 64)
				r.pace = formatLactatePace(smp.Value)
				byDate[smp.From] = r
			}
			sort.Strings(dateKeys)

			rows := make([][]string, 0, len(dateKeys))
			for _, d := range dateKeys {
				r := byDate[d]
				rows = append(rows, []string{d, orDash(r.hr), orDash(r.speed), orDash(r.pace)})
			}
			return renderTableTo(cmd.OutOrStdout(), opts.Format, []string{"date", "lthr_bpm", "lt_speed_raw", "lt_pace"}, rows)
		},
	}

	cmd.Flags().StringVar(&sport, "sport", "running", "Sport: running or cycling")
	cmd.Flags().StringVar(&from, "from", "", "Start date (YYYY-MM-DD, inclusive; default: 365 days ago)")
	cmd.Flags().StringVar(&to, "to", "", "End date (YYYY-MM-DD, inclusive; default: today)")
	cmd.Flags().IntVar(&days, "days", 0, "Shortcut: last N days (ending today)")
	return cmd
}
