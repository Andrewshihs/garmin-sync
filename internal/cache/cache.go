// Package cache maintains a local SQLite database of health/activity samples
// for trend analysis. Agents can query it directly with SQL via
// `garmin cache query`; the schema is intentionally narrow (one JSON payload
// column per sample) so new metrics need no migrations.
package cache

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS samples (
	profile    TEXT NOT NULL,
	region     TEXT NOT NULL,
	metric     TEXT NOT NULL,
	date       TEXT NOT NULL,
	payload    TEXT NOT NULL,
	fetched_at INTEGER NOT NULL,
	PRIMARY KEY (profile, metric, date)
);
CREATE TABLE IF NOT EXISTS activities (
	profile          TEXT NOT NULL,
	activity_id      INTEGER NOT NULL,
	start_time_local TEXT NOT NULL,
	type             TEXT NOT NULL,
	name             TEXT NOT NULL,
	distance_meters  REAL,
	duration_seconds REAL,
	calories         INTEGER,
	avg_hr           INTEGER,
	weather          TEXT,
	payload          TEXT NOT NULL,
	fetched_at       INTEGER NOT NULL,
	PRIMARY KEY (profile, activity_id)
);
CREATE INDEX IF NOT EXISTS idx_samples_profile_date ON samples (profile, date);
CREATE INDEX IF NOT EXISTS idx_activities_profile_date ON activities (profile, start_time_local);
DROP VIEW IF EXISTS v_daily_metrics;
CREATE VIEW IF NOT EXISTS v_daily_metrics AS
SELECT s.profile AS profile, s.region AS region, s.date AS date,
	json_extract(s.payload, '$.totalSteps') AS steps,
	json_extract(s.payload, '$.restingHeartRate') AS resting_hr,
	json_extract(s.payload, '$.minHeartRate') AS hr_min,
	json_extract(s.payload, '$.maxHeartRate') AS hr_max,
	json_extract(s.payload, '$.averageStressLevel') AS stress_avg,
	json_extract(s.payload, '$.bodyBatteryHighestValue') AS body_battery_high,
	json_extract(s.payload, '$.bodyBatteryLowestValue') AS body_battery_low
FROM samples s WHERE s.metric = 'daily_summary';
DROP VIEW IF EXISTS v_sleep;
CREATE VIEW IF NOT EXISTS v_sleep AS
SELECT s.profile AS profile, s.region AS region, s.date AS date,
	json_extract(s.payload, '$.score') AS score,
	json_extract(s.payload, '$.total_sleep_seconds')/60.0 AS sleep_min,
	json_extract(s.payload, '$.deep_seconds')/60.0 AS deep_min,
	json_extract(s.payload, '$.rem_seconds')/60.0 AS rem_min,
	json_extract(s.payload, '$.avg_spo2') AS avg_spo2
FROM samples s WHERE s.metric = 'sleep';
DROP VIEW IF EXISTS v_activities;
CREATE VIEW IF NOT EXISTS v_activities AS
SELECT profile, activity_id, date(start_time_local) AS date, start_time_local, type, name,
	distance_meters/1000.0 AS distance_km, duration_seconds/60.0 AS duration_min,
	CASE WHEN distance_meters > 0
		THEN duration_seconds/distance_meters*1000/60.0 END AS pace_min_per_km,
	avg_hr, calories,
	json_extract(weather, '$.temp') AS temp_f,
	json_extract(weather, '$.apparent_temp') AS apparent_temp_f,
	json_extract(weather, '$.relative_humidity') AS humidity
FROM activities;
`

// Open creates/opens the cache database and ensures the schema.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init cache schema: %w", err)
	}
	// Lightweight migration for caches created before the weather column.
	if _, err := db.Exec(`ALTER TABLE activities ADD COLUMN weather TEXT`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		_ = db.Close()
		return nil, fmt.Errorf("migrate cache schema: %w", err)
	}
	return db, nil
}

// Sample is one metric/date row of the samples table.
type Sample struct {
	Profile   string
	Region    string
	Metric    string
	Date      string
	Payload   any // marshaled to JSON
	FetchedAt time.Time
}

// UpsertSample stores (or replaces) the payload for one profile/metric/date.
func UpsertSample(db *sql.DB, s Sample) error {
	payload, err := json.Marshal(s.Payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	_, err = db.Exec(`INSERT INTO samples (profile, region, metric, date, payload, fetched_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(profile, metric, date) DO UPDATE SET
		  region = excluded.region, payload = excluded.payload, fetched_at = excluded.fetched_at`,
		s.Profile, s.Region, s.Metric, s.Date, string(payload), s.FetchedAt.Unix())
	return err
}

// ActivityRow is one activity cached for SQL analysis. Weather is the raw
// JSON from the activity weather endpoint (empty when unavailable, e.g.
// indoor activities).
type ActivityRow struct {
	Profile         string
	ActivityID      int64
	StartTimeLocal  string
	Type            string
	Name            string
	DistanceMeters  float64
	DurationSeconds float64
	Calories        int
	AvgHR           int
	Weather         string
	Payload         any
	FetchedAt       time.Time
}

// UpsertActivity stores (or replaces) one cached activity.
func UpsertActivity(db *sql.DB, a ActivityRow) error {
	payload, err := json.Marshal(a.Payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	_, err = db.Exec(`INSERT INTO activities (profile, activity_id, start_time_local, type, name,
		distance_meters, duration_seconds, calories, avg_hr, weather, payload, fetched_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(profile, activity_id) DO UPDATE SET
		  start_time_local = excluded.start_time_local, type = excluded.type, name = excluded.name,
		  distance_meters = excluded.distance_meters, duration_seconds = excluded.duration_seconds,
		  calories = excluded.calories, avg_hr = excluded.avg_hr, weather = excluded.weather,
		  payload = excluded.payload, fetched_at = excluded.fetched_at`,
		a.Profile, a.ActivityID, a.StartTimeLocal, a.Type, a.Name,
		a.DistanceMeters, a.DurationSeconds, a.Calories, a.AvgHR, a.Weather, string(payload), a.FetchedAt.Unix())
	return err
}

// MetricStatus describes the cached coverage of one metric.
type MetricStatus struct {
	Metric   string `json:"metric"`
	Count    int    `json:"count"`
	FirstDay string `json:"first_day"`
	LastDay  string `json:"last_day"`
}

// Status returns per-metric coverage for a profile.
func Status(db *sql.DB, profile string) ([]MetricStatus, error) {
	rows, err := db.Query(`SELECT metric, COUNT(*), MIN(date), MAX(date)
		FROM samples WHERE profile = ? GROUP BY metric ORDER BY metric`, profile)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MetricStatus{}
	for rows.Next() {
		var m MetricStatus
		if err := rows.Scan(&m.Metric, &m.Count, &m.FirstDay, &m.LastDay); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	act, err := activityStatus(db, profile)
	if err != nil {
		return nil, err
	}
	if act != nil {
		out = append(out, *act)
	}
	return out, nil
}

func activityStatus(db *sql.DB, profile string) (*MetricStatus, error) {
	var count int
	var first, last sql.NullString
	err := db.QueryRow(`SELECT COUNT(*), COALESCE(MIN(start_time_local), ''), COALESCE(MAX(start_time_local), '')
		FROM activities WHERE profile = ?`, profile).Scan(&count, &first, &last)
	if err != nil {
		return nil, err
	}
	if count == 0 || !first.Valid || len(first.String) < 10 {
		return nil, nil
	}
	return &MetricStatus{Metric: "activities", Count: count, FirstDay: first.String[:10], LastDay: last.String[:10]}, nil
}

// ErrReadOnly is returned when a query is not a read-only statement.
var ErrReadOnly = fmt.Errorf("only SELECT/WITH queries are allowed")

// Query executes a single read-only SQL statement and returns columns and rows.
// Values keep their SQLite types (int64/float64/string/[]byte/nil).
func Query(db *sql.DB, sqlText string) ([]string, [][]any, error) {
	trimmed := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(sqlText), ";"))
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "select") && !strings.HasPrefix(lower, "with") {
		return nil, nil, fmt.Errorf("%w: only SELECT/WITH queries are supported", ErrReadOnly)
	}
	if strings.Contains(strings.TrimRight(lower, ";"), ";") {
		return nil, nil, fmt.Errorf("%w: multiple statements are not supported", ErrReadOnly)
	}
	rows, err := db.Query(trimmed)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}
	out := [][]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		out = append(out, vals)
	}
	return cols, out, rows.Err()
}
