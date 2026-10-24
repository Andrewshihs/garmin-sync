package cache

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpsertSampleRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now()
	if err := UpsertSample(db, Sample{
		Profile: "cn", Region: "cn", Metric: "daily_summary",
		Date: "2026-10-20", Payload: map[string]any{"restingHeartRate": 48}, FetchedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	// 同键重写应替换 payload
	if err := UpsertSample(db, Sample{
		Profile: "cn", Region: "cn", Metric: "daily_summary",
		Date: "2026-10-20", Payload: map[string]any{"restingHeartRate": 52}, FetchedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	cols, rows, err := Query(db, "SELECT date, payload FROM samples WHERE profile = 'cn'")
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 2 || len(rows) != 1 {
		t.Fatalf("cols=%v rows=%v", cols, rows)
	}
	if !strings.Contains(rows[0][1].(string), "52") {
		t.Fatalf("payload=%v", rows[0][1])
	}
}

func TestQueryReadOnlyGuard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, bad := range []string{
		"DELETE FROM samples",
		"DROP TABLE samples",
		"SELECT 1; DROP TABLE samples",
		"PRAGMA journal_mode = DELETE",
	} {
		if _, _, err := Query(db, bad); err == nil {
			t.Fatalf("expected rejection for %q", bad)
		} else if !strings.Contains(err.Error(), ErrReadOnly.Error()) {
			t.Fatalf("q=%q err=%v", bad, err)
		}
	}
}

func TestUpsertActivityWithWeather(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := UpsertActivity(db, ActivityRow{
		Profile: "cn", ActivityID: 42, StartTimeLocal: "2026-10-21 21:37:05",
		Type: "running", Name: "Morning Run",
		DistanceMeters: 5000, DurationSeconds: 1800, Weather: `{"temp":64}`,
		Payload: map[string]any{"ok": true}, FetchedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	cols, rows, err := Query(db, "SELECT temp_f, distance_km FROM v_activities WHERE activity_id = 42")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows=%v", rows)
	}
	var tempF float64
	if err := db.QueryRow(`SELECT json_extract(weather, '$.temp') FROM activities WHERE activity_id = 42`).Scan(&tempF); err != nil {
		t.Fatal(err)
	}
	if tempF != 64 {
		t.Fatalf("temp=%v", tempF)
	}
	_ = cols
}
