package main

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"github.com/Andrewshihs/garmin-sync/internal/activities"
)

func summary(id int64, name, typ, start string) activities.Summary {
	return activities.Summary{ID: id, Name: name, Type: typ, StartTimeLocal: start}
}

func TestComputeMissingAllNew(t *testing.T) {
	from := []activities.Summary{
		summary(1, "Run A", "running", "2026-10-02 07:00:00"),
		summary(2, "Run B", "running", "2026-10-01 07:00:00"),
	}
	got := computeMissing(from, nil)
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	// oldest first
	if got[0].Name != "Run B" || got[1].Name != "Run A" {
		t.Fatalf("order: %s, %s", got[0].Name, got[1].Name)
	}
	if got[0].ID != 2 || got[1].ID != 1 {
		t.Fatalf("ids: %d, %d", got[0].ID, got[1].ID)
	}
}

func TestComputeMissingIgnoresTargetID(t *testing.T) {
	// The synced copy on the target side has a different ID; identity must
	// match on name/type/start time only.
	from := []activities.Summary{summary(1, "Run A", "running", "2026-10-01 07:00:00")}
	to := []activities.Summary{summary(999, "Run A", "running", "2026-10-01 07:00:00")}
	if got := computeMissing(from, to); len(got) != 0 {
		t.Fatalf("expected no missing, got %v", got)
	}
}

func TestComputeMissingIdentityFieldsMatter(t *testing.T) {
	from := []activities.Summary{
		summary(1, "Run A", "running", "2026-10-01 07:00:00"),
	}
	to := []activities.Summary{
		summary(9, "Run A renamed", "running", "2026-10-01 07:00:00"),
		summary(8, "Run A", "trail_running", "2026-10-01 07:00:00"),
		summary(7, "Run A", "running", "2026-10-01 08:00:00"),
	}
	got := computeMissing(from, to)
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("got %v", got)
	}
}

func TestComputeMissingEmptySource(t *testing.T) {
	to := []activities.Summary{summary(1, "Run A", "running", "2026-10-01 07:00:00")}
	if got := computeMissing(nil, to); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func buildZIP(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractFITFromZIP(t *testing.T) {
	data := buildZIP(t, map[string][]byte{
		"notes.gpx": []byte("<gpx/>"),
		"run.fit":   []byte("FITDATA"),
	})
	got, err := extractFITFromZIP(data)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "FITDATA" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractFITFromZIPNoFIT(t *testing.T) {
	data := buildZIP(t, map[string][]byte{"notes.gpx": []byte("<gpx/>")})
	_, err := extractFITFromZIP(data)
	if err == nil || !strings.Contains(err.Error(), "no FIT file found in ZIP archive") {
		t.Fatalf("err=%v", err)
	}
}

func TestExtractFITFromZIPNotAZip(t *testing.T) {
	_, err := extractFITFromZIP([]byte("definitely not a zip"))
	if err == nil || !strings.Contains(err.Error(), "failed to open ZIP") {
		t.Fatalf("err=%v", err)
	}
}

func runSyncForFlagsTest(t *testing.T, args ...string) error {
	t.Helper()
	cmd := NewSyncCmd(&globalOptions{})
	cmd.SetArgs(args)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	return cmd.Execute()
}

func TestSyncSourceTargetAliasConflict(t *testing.T) {
	for _, args := range [][]string{
		{"--source", "cn", "--from", "other"},
		{"--source", "cn", "--from-profile", "other"},
		{"--from", "cn", "--from-profile", "other"},
		{"--target", "global", "--to", "other"},
		{"--target", "global", "--to-profile", "other"},
		{"--to", "global", "--to-profile", "other"},
	} {
		err := runSyncForFlagsTest(t, args...)
		if err == nil || !strings.Contains(err.Error(), "use only one of") {
			t.Fatalf("args=%v err=%v", args, err)
		}
	}
}

func TestSyncRegionAliasConflict(t *testing.T) {
	err := runSyncForFlagsTest(t, "--source-region", "cn", "--from-region", "cn")
	if err == nil || !strings.Contains(err.Error(), "use only one of --source-region/--from-region") {
		t.Fatalf("err=%v", err)
	}
	err = runSyncForFlagsTest(t, "--target-region", "cn", "--to-region", "cn")
	if err == nil || !strings.Contains(err.Error(), "use only one of --target-region/--to-region") {
		t.Fatalf("err=%v", err)
	}
}

func TestSyncInvalidRegionOverride(t *testing.T) {
	err := runSyncForFlagsTest(t, "--source-region", "mars")
	if err == nil || !strings.Contains(err.Error(), "invalid source region") {
		t.Fatalf("err=%v", err)
	}
	err = runSyncForFlagsTest(t, "--from-region", "mars")
	if err == nil || !strings.Contains(err.Error(), "invalid source region") {
		t.Fatalf("legacy region alias err=%v", err)
	}
}

func TestSyncAcceptsLegacyAliasFlags(t *testing.T) {
	// The live systemd unit still passes --from-profile/--to-profile; they must
	// keep working (this errors later at auth, not at flag parsing).
	err := runSyncForFlagsTest(t, "--from-profile", "cn", "--to-profile", "global", "--from-region", "cn", "--to-region", "global", "--dry-run")
	if err == nil || strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("legacy aliases must parse, err=%v", err)
	}
}
