package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Andrewshihs/garmin-sync/internal/activities"
	"github.com/Andrewshihs/garmin-sync/internal/config"
	"github.com/Andrewshihs/garmin-sync/internal/output"
	"github.com/spf13/cobra"
)

const syncFailureRetryAfter = 7 * 24 * time.Hour

// syncLedger remembers which source activities this from→to direction has
// already handled. GPX fallback uploads get renamed/re-typed by Garmin on the
// target side, so the identity diff cannot recognise those copies — without
// the ledger they would be re-uploaded every run. 404-download failures are
// also recorded (data unavailable on the source side) and retried after
// syncFailureRetryAfter; other failures stay transient and retry next run.
type syncLedgerEntry struct {
	Status string `json:"status"` // "ok" | "unavailable"
	Time   int64  `json:"time"`
}
type syncLedger map[string]syncLedgerEntry

func loadSyncLedger(path string) syncLedger {
	ledger := syncLedger{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &ledger)
	}
	return ledger
}

func (l syncLedger) skip(id int64, now time.Time) bool {
	e, ok := l[strconv.FormatInt(id, 10)]
	if !ok {
		return false
	}
	if e.Status == "ok" {
		return true
	}
	return e.Status == "unavailable" && now.Sub(time.Unix(e.Time, 0)) < syncFailureRetryAfter
}

type syncActivity struct {
	ID        int64  `json:"activity_id,omitempty"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	StartTime string `json:"start_time"`
}

type syncSuccess struct {
	CNActivityID     int64  `json:"cn_activity_id"`
	GlobalActivityID int64  `json:"global_activity_id"`
	Name             string `json:"name"`
	Type             string `json:"type"`
}

type syncFailure struct {
	ID     int64  `json:"activity_id,omitempty"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type syncResult struct {
	Status    string         `json:"status,omitempty"`
	Message   string         `json:"message,omitempty"`
	Total     int            `json:"total,omitempty"`
	Synced    int            `json:"synced,omitempty"`
	Failed    int            `json:"failed,omitempty"`
	ToSync    []syncActivity `json:"to_sync,omitempty"`
	Successes []syncSuccess  `json:"successes,omitempty"`
	Failures  []syncFailure  `json:"failures,omitempty"`
}

func NewSyncCmd(opts *globalOptions) *cobra.Command {
	var fromProfile, toProfile string
	var fromProfileAlias, toProfileAlias string
	var fromRegion, toRegion string
	var limit int
	var delay int
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync activities between regions",
		Long: `Sync activities from one Garmin Connect region to another.

Activities are matched by name, type and start time; copies already present on
the target side are skipped. This command ignores the global -p/--profile and
--region flags: regions default to what each profile was logged in with, and
--from-region/--to-region override them when needed.

Examples:
  garmin sync --from cn --to global
  garmin sync --from cn --to global --limit 20 --delay 5
  garmin sync --dry-run`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("from") && cmd.Flags().Changed("from-profile") {
				return fmt.Errorf("use --from or --from-profile, not both")
			}
			if cmd.Flags().Changed("to") && cmd.Flags().Changed("to-profile") {
				return fmt.Errorf("use --to or --to-profile, not both")
			}
			if fromProfileAlias != "" {
				fromProfile = fromProfileAlias
			}
			if toProfileAlias != "" {
				toProfile = toProfileAlias
			}
			if fromProfile == "" {
				fromProfile = "cn"
			}
			if toProfile == "" {
				toProfile = "global"
			}
			if err := config.ValidateProfile(fromProfile); err != nil {
				return fmt.Errorf("invalid --from: %w", err)
			}
			if err := config.ValidateProfile(toProfile); err != nil {
				return fmt.Errorf("invalid --to: %w", err)
			}
			if limit <= 0 {
				return fmt.Errorf("limit must be > 0")
			}
			if delay < 0 {
				return fmt.Errorf("delay must be >= 0")
			}

			cfgDir, err := config.ResolveConfigDir(opts.ConfigDir)
			if err != nil {
				return err
			}
			ledgerPath := filepath.Join(cfgDir, fmt.Sprintf("sync-ledger-%s-%s.json", fromProfile, toProfile))
			ledger := loadSyncLedger(ledgerPath)
			saveLedger := func() {
				if b, err := json.MarshalIndent(ledger, "", "  "); err == nil {
					_ = config.WriteFileAtomic(ledgerPath, append(b, '\n'), 0o600)
				}
			}
			now := time.Now()
			resolveSideRegion := func(flagName, profile string) (config.Region, error) {
				if cmd.Flags().Changed(flagName) {
					v, _ := cmd.Flags().GetString(flagName)
					return config.ParseRegion(v)
				}
				// No override: GARMIN_REGION env > the region recorded for this
				// profile at login > a profile named after its own region
				// (e.g. "cn"/"global"; older tokens lack region.json) > global.
				if v := strings.TrimSpace(os.Getenv(config.RegionEnv)); v != "" {
					return config.ParseRegion(v)
				}
				if r, err := config.LoadRegion(cfgDir, profile); err == nil && r != "" {
					return r, nil
				}
				if r, err := config.ParseRegion(profile); err == nil && r != "" {
					return r, nil
				}
				return config.RegionGlobal, nil
			}
			fromRegionParsed, err := resolveSideRegion("from-region", fromProfile)
			if err != nil {
				return fmt.Errorf("invalid --from-region: %w", err)
			}
			toRegionParsed, err := resolveSideRegion("to-region", toProfile)
			if err != nil {
				return fmt.Errorf("invalid --to-region: %w", err)
			}

			ctx := cmd.Context()
			errOut := cmd.ErrOrStderr()

			fmt.Fprintf(errOut, "Fetching activities from %s region...\n", fromRegionParsed)
			fromClient, err := newAuthedClientFor(cmd, opts, fromProfile, fromRegionParsed)
			if err != nil {
				return err
			}
			fromList, err := activities.List(ctx, fromClient, limit, "", "", "")
			if err != nil {
				return handleAuthedErrorTo(errOut, opts, fmt.Errorf("failed to list activities from %s: %w", fromRegionParsed, err))
			}

			fmt.Fprintf(errOut, "Fetching activities from %s region...\n", toRegionParsed)
			toClient, err := newAuthedClientFor(cmd, opts, toProfile, toRegionParsed)
			if err != nil {
				return err
			}
			toList, err := activities.List(ctx, toClient, limit, "", "", "")
			if err != nil {
				return handleAuthedErrorTo(errOut, opts, fmt.Errorf("failed to list activities from %s: %w", toRegionParsed, err))
			}

			var fromCandidates []activities.Summary
			for _, a := range fromList {
				if !ledger.skip(a.ID, now) {
					fromCandidates = append(fromCandidates, a)
				}
			}
			missing := computeMissing(fromCandidates, toList)

			result := syncResult{Total: len(missing), Status: "ok"}
			out := cmd.OutOrStdout()

			if len(missing) == 0 {
				result.Message = "No new activities to sync"
				if opts.Format == "json" {
					return output.JSONTo(out, result)
				}
				fmt.Fprintln(out, "✓ No new activities to sync")
				return nil
			}

			if dryRun {
				result.ToSync = missing
				if opts.Format == "json" {
					return output.JSONTo(out, result)
				}
				fmt.Fprintf(out, "Would sync %d activities:\n", len(missing))
				for _, a := range missing {
					fmt.Fprintf(out, "  - %s (%s) %s\n", a.Name, a.Type, a.StartTime)
				}
				return nil
			}

			// One private temp dir per run: keeps concurrent runs from
			// clobbering each other's garmin_sync_<id>.fit files.
			tmpDir, err := os.MkdirTemp("", "garmin-sync-")
			if err != nil {
				return fmt.Errorf("create temp dir: %w", err)
			}
			defer os.RemoveAll(tmpDir)

			recordFailure := func(act syncActivity, printed, reason string) {
				fmt.Fprintf(errOut, "  ✗ %s\n", printed)
				result.Failed++
				result.Failures = append(result.Failures, syncFailure{ID: act.ID, Name: act.Name, Reason: reason})
			}

			// exportForSync fetches the activity file to upload. Original FIT is
			// preferred; Garmin's CN download-service frequently 404s originals
			// for imported activities. Fall back to TCX (carries laps: duration/
			// calories/HR even for trackless activities) and finally GPX (track
			// only). Each fallback is content-checked: a TCX without laps or a
			// GPX without a track would create hollow entries on the target side.
			exportForSync := func(id int64) ([]byte, string, error) {
				var buf bytes.Buffer
				fitErr := activities.Export(ctx, fromClient, id, activities.ExportOriginal, &buf)
				if fitErr == nil {
					return buf.Bytes(), "fit", nil
				}
				for _, f := range []struct {
					typ      activities.ExportType
					name     string
					contains string
				}{
					{activities.ExportTCX, "tcx", "<TotalTimeSeconds"},
					{activities.ExportGPX, "gpx", "<trkpt"},
				} {
					buf.Reset()
					if err := activities.Export(ctx, fromClient, id, f.typ, &buf); err == nil &&
						bytes.Contains(buf.Bytes(), []byte(f.contains)) {
						return buf.Bytes(), f.name, nil
					}
				}
				return nil, "", fitErr
			}

			for i, act := range missing {
				fmt.Fprintf(errOut, "[%d/%d] Syncing %s (%s)...\n", i+1, len(missing), act.Name, act.Type)

				data, format, err := exportForSync(act.ID)
				if err != nil {
					fmt.Fprintf(errOut, "  ✗ Failed to download: %v\n", err)
					result.Failed++
					result.Failures = append(result.Failures, syncFailure{ID: act.ID, Name: act.Name, Reason: fmt.Sprintf("download failed: %v", err)})
					// Source-side data unavailable (e.g. original 404 and GPX has
					// no track): back off instead of retrying every run.
					if strings.Contains(err.Error(), "404") {
						ledger[strconv.FormatInt(act.ID, 10)] = syncLedgerEntry{Status: "unavailable", Time: now.Unix()}
						saveLedger()
					}
					continue
				}

				// "Original" exports occasionally come back as a ZIP archive
				// wrapping the FIT file; extract it before uploading.
				if format == "fit" && len(data) > 2 && data[0] == 'P' && data[1] == 'K' {
					data, err = extractFITFromZIP(data)
					if err != nil {
						recordFailure(act, fmt.Sprintf("Failed to extract FIT: %v", err), fmt.Sprintf("extract failed: %v", err))
						continue
					}
				}

				tmpPath := filepath.Join(tmpDir, fmt.Sprintf("garmin_sync_%d.%s", act.ID, format))
				if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
					recordFailure(act, fmt.Sprintf("Failed to write temp file: %v", err), fmt.Sprintf("write failed: %v", err))
					continue
				}

				res, err := activities.Upload(ctx, toClient, activities.UploadOptions{FilePath: tmpPath, Format: format})
				os.Remove(tmpPath)
				if err != nil {
					recordFailure(act, fmt.Sprintf("Failed to upload: %v", err), fmt.Sprintf("upload failed: %v", err))
					continue
				}

				newID := res.FirstActivityID()
				if res.Duplicate() {
					// Garmin rejected the upload as an identical re-upload of an
					// activity already on the target side (typically a copy made
					// outside this tool's window). Same outcome as a fresh sync.
					fmt.Fprintf(errOut, "  ✓ Already synced (ID: %d → %d)\n", act.ID, newID)
				} else {
					fmt.Fprintf(errOut, "  ✓ Synced (ID: %d → %d)\n", act.ID, newID)
				}
				result.Synced++
				succ := syncSuccess{Name: act.Name, Type: act.Type}
				if fromRegionParsed.IsCN() {
					succ.CNActivityID, succ.GlobalActivityID = act.ID, newID
				} else {
					succ.GlobalActivityID, succ.CNActivityID = act.ID, newID
				}
				result.Successes = append(result.Successes, succ)
				ledger[strconv.FormatInt(act.ID, 10)] = syncLedgerEntry{Status: "ok", Time: now.Unix()}
				saveLedger()

				if delay > 0 && i < len(missing)-1 {
					time.Sleep(time.Duration(delay) * time.Second)
				}
			}

			if opts.Format == "json" {
				return output.JSONTo(out, result)
			}
			fmt.Fprintf(out, "✓ Sync completed: %d synced, %d failed\n", result.Synced, result.Failed)
			return nil
		},
	}

	cmd.Flags().StringVar(&fromProfile, "from", "", "Source profile (default: cn)")
	cmd.Flags().StringVar(&toProfile, "to", "", "Target profile (default: global)")
	cmd.Flags().StringVar(&fromProfileAlias, "from-profile", "", "Deprecated alias of --from")
	_ = cmd.Flags().MarkHidden("from-profile")
	cmd.Flags().StringVar(&toProfileAlias, "to-profile", "", "Deprecated alias of --to")
	_ = cmd.Flags().MarkHidden("to-profile")
	cmd.Flags().StringVar(&fromRegion, "from-region", "", "Override source region: cn or global (default: the region --from was logged in with)")
	cmd.Flags().StringVar(&toRegion, "to-region", "", "Override target region: cn or global (default: the region --to was logged in with)")
	cmd.Flags().IntVar(&limit, "limit", 10, "Max activities to check")
	cmd.Flags().IntVar(&delay, "delay", 5, "Delay between uploads (seconds)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be synced without actually syncing")
	return cmd
}

// syncIdentity is the region-independent identity of an activity. A synced
// copy always lands with a new ID on the target side, so the region-local ID
// must not take part in the comparison.
func syncIdentity(a activities.Summary) syncActivity {
	return syncActivity{Name: a.Name, Type: a.Type, StartTime: a.StartTimeLocal}
}

// computeMissing returns the activities present in from but not in to,
// oldest first (by StartTimeLocal string order).
func computeMissing(from, to []activities.Summary) []syncActivity {
	target := make(map[syncActivity]struct{}, len(to))
	for _, a := range to {
		target[syncIdentity(a)] = struct{}{}
	}
	var missing []syncActivity
	for _, a := range from {
		if _, ok := target[syncIdentity(a)]; !ok {
			missing = append(missing, syncActivity{ID: a.ID, Name: a.Name, Type: a.Type, StartTime: a.StartTimeLocal})
		}
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i].StartTime < missing[j].StartTime })
	return missing
}

// extractFITFromZIP pulls the first .fit entry out of a "original" export that
// Garmin delivered as a ZIP archive.
func extractFITFromZIP(data []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("failed to open ZIP: %w", err)
	}
	for _, f := range zr.File {
		if !strings.HasSuffix(strings.ToLower(f.Name), ".fit") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("failed to open FIT file in ZIP: %w", err)
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		if err != nil {
			return nil, fmt.Errorf("failed to read FIT file: %w", err)
		}
		return b, nil
	}
	return nil, fmt.Errorf("no FIT file found in ZIP archive")
}
