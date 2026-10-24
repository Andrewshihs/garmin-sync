package health

// personal.go adapts Garmin's officially documented Personal API endpoints
// (served by the personal.connectapi gateway) to the same summary structs
// the CLI has always produced. Getters dispatch on Client.PersonalPipeline():
// personal gateway → official path + mapping below; OAuth gateway → the
// original reverse-engineered path, unchanged.

import (
	"context"
	"net/url"
	"time"

	"github.com/Andrewshihs/garmin-sync/internal/client"
)

// v2Entry is one day of the official GET /usersummary-service/v2/dailysummaries
// response; nested objects carry every daily metric.
type v2Entry struct {
	CalendarDate string `json:"calendarDate"`
	HeartRate    struct {
		MinValue     *int `json:"minValue"`
		MaxValue     *int `json:"maxValue"`
		RestingValue *int `json:"restingValue"`
	} `json:"heartRate"`
	Stress struct {
		AvgLevel  *int   `json:"avgLevel"`
		MaxLevel  *int   `json:"maxLevel"`
		Qualifier string `json:"qualifier"`
	} `json:"stress"`
	BodyBattery struct {
		MinValue     *int `json:"minValue"`
		MaxValue     *int `json:"maxValue"`
		ChargedValue *int `json:"chargedValue"`
		DrainedValue *int `json:"drainedValue"`
		LatestValue  *int `json:"latestValue"`
	} `json:"bodyBattery"`
	Movement struct {
		Steps struct {
			Goal             *int     `json:"goal"`
			Value            *int     `json:"value"`
			DistanceInMeters *float64 `json:"distanceInMeters"`
		} `json:"steps"`
	} `json:"movement"`
	IntensityMinutes struct {
		Goal     *int `json:"goal"`
		Moderate *int `json:"moderate"`
		Vigorous *int `json:"vigorous"`
	} `json:"intensityMinutes"`
	PulseOx struct {
		AvgValue    *int `json:"avgValue"`
		MinValue    *int `json:"minValue"`
		LatestValue *int `json:"latestValue"`
	} `json:"pulseOx"`
	Respiration struct {
		AvgValue    *int `json:"avgValue"`
		MaxValue    *int `json:"maxValue"`
		MinValue    *int `json:"minValue"`
		LatestValue *int `json:"latestValue"`
	} `json:"respiration"`
}

type v2Response struct {
	Data []v2Entry `json:"data"`
}

// fetchV2Day returns the official dailysummaries entry for one date, or nil
// when the day has no data (an empty result, not an error).
func fetchV2Day(ctx context.Context, c *client.Client, date string) (*v2Entry, error) {
	q := url.Values{"startDate": {date}, "endDate": {date}}
	var raw v2Response
	if err := c.GetJSON(ctx, "/usersummary-service/v2/dailysummaries", q, &raw); err != nil {
		return nil, err
	}
	if len(raw.Data) == 0 {
		return nil, nil
	}
	e := raw.Data[0]
	return &e, nil
}

func intFromF64(f *float64) *int {
	if f == nil {
		return nil
	}
	v := int(*f)
	return &v
}

func f64FromInt(i *int) *float64 {
	if i == nil {
		return nil
	}
	v := float64(*i)
	return &v
}

// dailySummaryFromV2 maps the official entry onto the CLI's DailySummary.
func dailySummaryFromV2(e *v2Entry) DailySummary {
	if e == nil {
		return DailySummary{}
	}
	return DailySummary{
		CalendarDate:               e.CalendarDate,
		TotalSteps:                 e.Movement.Steps.Value,
		DailyStepGoal:              e.Movement.Steps.Goal,
		TotalDistanceMeters:        intFromF64(e.Movement.Steps.DistanceInMeters),
		MinHeartRate:               e.HeartRate.MinValue,
		MaxHeartRate:               e.HeartRate.MaxValue,
		RestingHeartRate:           e.HeartRate.RestingValue,
		AverageStressLevel:         e.Stress.AvgLevel,
		MaxStressLevel:             e.Stress.MaxLevel,
		StressQualifier:            e.Stress.Qualifier,
		BodyBatteryChargedValue:    e.BodyBattery.ChargedValue,
		BodyBatteryDrainedValue:    e.BodyBattery.DrainedValue,
		BodyBatteryHighestValue:    e.BodyBattery.MaxValue,
		BodyBatteryLowestValue:     e.BodyBattery.MinValue,
		BodyBatteryMostRecentValue: e.BodyBattery.LatestValue,
	}
}

// spo2FromV2 maps the official entry onto the CLI's SpO2Summary.
func spo2FromV2(fallbackDate string, e *v2Entry) SpO2Summary {
	if e == nil {
		return SpO2Summary{Date: fallbackDate}
	}
	return SpO2Summary{
		Date:    e.CalendarDate,
		Average: f64FromInt(e.PulseOx.AvgValue),
		Lowest:  f64FromInt(e.PulseOx.MinValue),
		Latest:  f64FromInt(e.PulseOx.LatestValue),
	}
}

// intensityMinutesFromV2 maps the official entry onto the CLI's summary type.
func intensityMinutesFromV2(fallbackDate string, e *v2Entry) IntensityMinutesSummary {
	if e == nil {
		return IntensityMinutesSummary{Date: fallbackDate}
	}
	return IntensityMinutesSummary{
		Date:       e.CalendarDate,
		Moderate:   f64FromInt(e.IntensityMinutes.Moderate),
		Vigorous:   f64FromInt(e.IntensityMinutes.Vigorous),
		WeeklyGoal: f64FromInt(e.IntensityMinutes.Goal),
	}
}

// respirationFromV2 maps the official entry onto the CLI's RespirationSummary.
// Note: the v2 average is an all-day mean; the OAuth path reports a waking
// average, so AvgWaking semantics differ slightly between pipelines.
func respirationFromV2(fallbackDate string, e *v2Entry) RespirationSummary {
	if e == nil {
		return RespirationSummary{Date: fallbackDate}
	}
	return RespirationSummary{
		Date:      e.CalendarDate,
		AvgWaking: f64FromInt(e.Respiration.AvgValue),
		Highest:   f64FromInt(e.Respiration.MaxValue),
		Lowest:    f64FromInt(e.Respiration.MinValue),
	}
}

// dailySleepOfficial is the official GET /sleep-service/sleep/dailySleep
// response (a different shape from the legacy dailySleepData path).
type dailySleepOfficial struct {
	CalendarDate             string   `json:"calendarDate"`
	SleepTimeSeconds         *int     `json:"sleepTimeSeconds"`
	DeepSleepSeconds         *int     `json:"deepSleepSeconds"`
	LightSleepSeconds        *int     `json:"lightSleepSeconds"`
	RemSleepSeconds          *int     `json:"remSleepSeconds"`
	AwakeSleepSeconds        *int     `json:"awakeSleepSeconds"`
	AverageSpO2Value         *float64 `json:"averageSpO2Value"`
	AverageRespirationValue  *float64 `json:"averageRespirationValue"`
	SleepStartTimestampLocal *int64   `json:"sleepStartTimestampLocal"` // epoch ms
	SleepEndTimestampLocal   *int64   `json:"sleepEndTimestampLocal"`   // epoch ms
	SleepScores              struct {
		Overall *struct {
			Value *int `json:"value"`
		} `json:"overall"`
	} `json:"sleepScores"`
}

// sleepFromOfficial maps the official response onto the CLI's SleepSummary.
func sleepFromOfficial(fallbackDate string, o *dailySleepOfficial) SleepSummary {
	if o == nil {
		return SleepSummary{Date: fallbackDate}
	}
	s := SleepSummary{
		Date:              orDefaultStr(o.CalendarDate, fallbackDate),
		TotalSleepSeconds: o.SleepTimeSeconds,
		DeepSeconds:       o.DeepSleepSeconds,
		LightSeconds:      o.LightSleepSeconds,
		RemSeconds:        o.RemSleepSeconds,
		AwakeSeconds:      o.AwakeSleepSeconds,
	}
	s.AvgSpO2 = o.AverageSpO2Value
	s.AvgRespiration = o.AverageRespirationValue
	if o.SleepScores.Overall != nil {
		s.Score = o.SleepScores.Overall.Value
	}
	if o.SleepStartTimestampLocal != nil && *o.SleepStartTimestampLocal > 0 {
		v := time.UnixMilli(*o.SleepStartTimestampLocal).Format("2006-01-02 15:04:05")
		s.SleepStart = &v
	}
	if o.SleepEndTimestampLocal != nil && *o.SleepEndTimestampLocal > 0 {
		v := time.UnixMilli(*o.SleepEndTimestampLocal).Format("2006-01-02 15:04:05")
		s.SleepEnd = &v
	}
	return s
}

func orDefaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
