package client

import (
	"testing"

	"github.com/Andrewshihs/garmin-sync/internal/auth"
	"github.com/Andrewshihs/garmin-sync/internal/config"
)

func fakeSession(region config.Region) *auth.Session {
	return &auth.Session{
		OAuth1: auth.OAuth1Token{OAuthToken: "tok", OAuthTokenSecret: "sec", Domain: region.OrDefault().Domain()},
		OAuth2: auth.OAuth2Token{TokenType: "Bearer", AccessToken: "at"},
		Region: region.OrDefault(),
	}
}

func TestNewWithSessionResolvesRegionBaseURL(t *testing.T) {
	dir := t.TempDir()

	c := NewWithSession(dir, "cn", fakeSession(config.RegionCN), Options{})
	if got, want := c.BaseURL(), "https://connectapi.garmin.cn"; got != want {
		t.Errorf("session cn BaseURL = %q; want %q", got, want)
	}
	if c.Region() != config.RegionCN {
		t.Errorf("Region() = %q; want cn", c.Region())
	}

	c = NewWithSession(dir, "p", fakeSession(config.RegionGlobal), Options{})
	if got, want := c.BaseURL(), "https://connectapi.garmin.com"; got != want {
		t.Errorf("session global BaseURL = %q; want %q", got, want)
	}

	// Legacy session without a region marker falls back to global (historical behaviour).
	legacy := fakeSession(config.RegionGlobal)
	legacy.Region = ""
	legacy.OAuth1.Domain = ""
	c = NewWithSession(dir, "p", legacy, Options{})
	if got, want := c.BaseURL(), "https://connectapi.garmin.com"; got != want {
		t.Errorf("legacy BaseURL = %q; want %q", got, want)
	}

	// An explicit --region overrides what the session recorded.
	c = NewWithSession(dir, "cn", fakeSession(config.RegionCN), Options{Region: config.RegionGlobal})
	if got, want := c.BaseURL(), "https://connectapi.garmin.com"; got != want {
		t.Errorf("override BaseURL = %q; want %q", got, want)
	}

	// GARMIN_CONNECTAPI_BASE_URL / Options.BaseURL still wins over everything.
	c = NewWithSession(dir, "cn", fakeSession(config.RegionCN), Options{BaseURL: "http://127.0.0.1:8080"})
	if got := c.BaseURL(); got != "http://127.0.0.1:8080" {
		t.Errorf("explicit BaseURL = %q", got)
	}
}
