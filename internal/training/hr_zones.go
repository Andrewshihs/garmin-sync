package training

import (
	"context"
	"net/url"

	"github.com/Andrewshihs/garmin-sync/internal/client"
)

// HRZoneEntry is one sport's heart rate zone configuration, matching Garmin's
// biometric-service response keys (camelCase).
type HRZoneEntry struct {
	TrainingMethod                string `json:"trainingMethod"`
	RestingHeartRateUsed          int    `json:"restingHeartRateUsed"`
	LactateThresholdHeartRateUsed int    `json:"lactateThresholdHeartRateUsed"`
	Zone1Floor                    int    `json:"zone1Floor"`
	Zone2Floor                    int    `json:"zone2Floor"`
	Zone3Floor                    int    `json:"zone3Floor"`
	Zone4Floor                    int    `json:"zone4Floor"`
	Zone5Floor                    int    `json:"zone5Floor"`
	MaxHeartRateUsed              int    `json:"maxHeartRateUsed"`
	Sport                         string `json:"sport"`
}

// GetHRZones returns the heart rate zone configuration for the account.
// sport is an optional filter (e.g. "RUNNING"); pass "" for all zones.
func GetHRZones(ctx context.Context, c *client.Client, sport string) ([]HRZoneEntry, error) {
	path := "/biometric-service/heartRateZones/v2"
	if sport != "" {
		path += "?" + url.Values{"sport": {sport}}.Encode()
	}
	var zones []HRZoneEntry
	if err := c.GetJSON(ctx, path, nil, &zones); err != nil {
		return nil, err
	}
	return zones, nil
}

// HRZoneSummary is a flattened view for CLI output.
type HRZoneSummary struct {
	Sport      string `json:"sport"`
	Method     string `json:"method"`
	RestingHR  int    `json:"resting_hr"`
	MaxHR      int    `json:"max_hr"`
	LTHR       int    `json:"lthr"`
	Zone1Floor int    `json:"zone1_floor"`
	Zone2Floor int    `json:"zone2_floor"`
	Zone3Floor int    `json:"zone3_floor"`
	Zone4Floor int    `json:"zone4_floor"`
	Zone5Floor int    `json:"zone5_floor"`
}

// ToSummaries converts raw HR zone entries to flat CLI-friendly rows.
func ToSummaries(zones []HRZoneEntry) []HRZoneSummary {
	out := make([]HRZoneSummary, 0, len(zones))
	for _, z := range zones {
		out = append(out, HRZoneSummary{
			Sport:      z.Sport,
			Method:     z.TrainingMethod,
			RestingHR:  z.RestingHeartRateUsed,
			MaxHR:      z.MaxHeartRateUsed,
			LTHR:       z.LactateThresholdHeartRateUsed,
			Zone1Floor: z.Zone1Floor,
			Zone2Floor: z.Zone2Floor,
			Zone3Floor: z.Zone3Floor,
			Zone4Floor: z.Zone4Floor,
			Zone5Floor: z.Zone5Floor,
		})
	}
	return out
}
