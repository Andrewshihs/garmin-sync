package training

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/Andrewshihs/garmin-sync/internal/client"
)

// LactateSample is one entry of Garmin's lactate threshold time series.
// Heart-rate values are bpm; speed values are in an undocumented unit that
// empirically behaves like km/min (≈17.5 km/h for a trained runner).
type LactateSample struct {
	From        string  `json:"from"`
	Until       string  `json:"until"`
	Series      string  `json:"series"`
	Value       float64 `json:"value"`
	UpdatedDate string  `json:"updatedDate,omitempty"`
}

// The range endpoint rejects windows longer than 366 days.
const lactateMaxWindowDays = 366

func lactateRangeURL(kind, from, to, sport string) string {
	path := fmt.Sprintf("/biometric-service/stats/lactateThreshold%s/range/%s/%s", kind, from, to)
	q := url.Values{"sport": {sport}, "aggregationStrategy": {"LATEST"}}
	return path + "?" + q.Encode()
}

// LactateThresholdHeartRate returns the heart-rate lactate threshold series
// (bpm) for the sport ("RUNNING"/"CYCLING") within [from, to] (YYYY-MM-DD).
func LactateThresholdHeartRate(ctx context.Context, c *client.Client, from, to, sport string) ([]LactateSample, error) {
	var out []LactateSample
	if err := c.GetJSON(ctx, lactateRangeURL("HeartRate", from, to, sport), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// LactateThresholdSpeed returns the speed lactate threshold series for the
// sport within [from, to]. See LactateSample about the value unit.
func LactateThresholdSpeed(ctx context.Context, c *client.Client, from, to, sport string) ([]LactateSample, error) {
	var out []LactateSample
	if err := c.GetJSON(ctx, lactateRangeURL("Speed", from, to, sport), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// NormalizeLactateSport maps user input to the sport value Garmin expects.
func NormalizeLactateSport(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "run", "running":
		return "RUNNING", nil
	case "bike", "cycling", "cycle":
		return "CYCLING", nil
	default:
		return "", fmt.Errorf("unsupported sport %q (supported: running, cycling)", s)
	}
}
