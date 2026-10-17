package training

import (
	"context"

	"github.com/Andrewshihs/garmin-sync/internal/client"
	"github.com/Andrewshihs/garmin-sync/internal/convert"
)

type VO2Max struct {
	Running float64 `json:"running"`
	Cycling float64 `json:"cycling"`
}

func GetVO2Max(ctx context.Context, c *client.Client) (VO2Max, error) {
	var raw map[string]any
	if err := c.GetJSON(ctx, "/userprofile-service/userprofile/user-settings", nil, &raw); err != nil {
		return VO2Max{}, err
	}
	userData, _ := raw["userData"].(map[string]any)
	return VO2Max{
		Running: convert.FloatFromAny(userData["vo2MaxRunning"]),
		Cycling: convert.FloatFromAny(userData["vo2MaxCycling"]),
	}, nil
}
