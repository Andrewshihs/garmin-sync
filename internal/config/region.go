package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Region selects which Garmin Connect instance to talk to.
// Garmin runs a separate China instance (garmin.cn) with its own SSO and API hosts;
// tokens from one region are not valid in the other.
type Region string

const (
	// RegionGlobal is connectapi.garmin.com (the default upstream behaviour).
	RegionGlobal Region = "global"
	// RegionCN is connectapi.garmin.cn (Garmin Connect China).
	RegionCN Region = "cn"

	// RegionEnv lets scripts pin the region without passing --region everywhere.
	RegionEnv = "GARMIN_REGION"
)

// regionFile is the per-profile marker written on login so that later commands
// can pick the right hosts from --profile alone.
type regionFile struct {
	Region string `json:"region"`
}

// ParseRegion normalises user input. Empty means "not specified".
func ParseRegion(s string) (Region, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return "", nil
	case "cn", "garmin.cn", "china", "com.cn", "中国", "国区":
		return RegionCN, nil
	case "global", "com", "garmin.com", "intl", "international":
		return RegionGlobal, nil
	default:
		return "", fmt.Errorf("unknown region %q (supported: global, cn)", s)
	}
}

func (r Region) Validate() error {
	switch r {
	case "", RegionGlobal, RegionCN:
		return nil
	default:
		return fmt.Errorf("unknown region %q (supported: global, cn)", string(r))
	}
}

// OrDefault maps the empty region to global.
func (r Region) OrDefault() Region {
	if r == "" {
		return RegionGlobal
	}
	return r
}

func (r Region) IsCN() bool { return r.OrDefault() == RegionCN }

// Domain is the cookie/account domain for the region.
func (r Region) Domain() string {
	if r.IsCN() {
		return "garmin.cn"
	}
	return "garmin.com"
}

// SSOBase is the SSO root used for signin/MFA pages.
func (r Region) SSOBase() string {
	if r.IsCN() {
		return "https://sso.garmin.cn/sso"
	}
	return "https://sso.garmin.com/sso"
}

func (r Region) SSOEmbed() string  { return r.SSOBase() + "/embed" }
func (r Region) SSOSignin() string { return r.SSOBase() + "/signin" }
func (r Region) SSOMFA() string    { return r.SSOBase() + "/verifyMFA/loginEnterMfaCode" }

// APIHost is the connectapi host name (also used for the DI-Backend header).
func (r Region) APIHost() string {
	if r.IsCN() {
		return "connectapi.garmin.cn"
	}
	return "connectapi.garmin.com"
}

// APIBase is the authenticated Connect API root.
func (r Region) APIBase() string { return "https://" + r.APIHost() }

// WebBase is the modern website root (course/activity links shown to users).
func (r Region) WebBase() string {
	if r.IsCN() {
		return "https://connect.garmin.cn/modern"
	}
	return "https://connect.garmin.com/modern"
}

func (r Region) String() string { return string(r.OrDefault()) }

// RegionPath is the per-profile region marker inside the tokens directory.
func RegionPath(configDir, profile string) string {
	return filepath.Join(TokensDir(configDir, profile), "region.json")
}

// SaveRegion records the region a profile was authenticated against.
func SaveRegion(configDir, profile string, r Region) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if r == "" {
		return nil
	}
	data, err := json.MarshalIndent(regionFile{Region: r.String()}, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(RegionPath(configDir, profile), append(data, '\n'), 0o600)
}

// LoadRegion reads the recorded region; a missing marker is not an error.
func LoadRegion(configDir, profile string) (Region, error) {
	b, err := ReadFile(RegionPath(configDir, profile), 1<<10)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var f regionFile
	if err := json.Unmarshal(b, &f); err != nil {
		return "", fmt.Errorf("parse %s: %w", RegionPath(configDir, profile), err)
	}
	r, err := ParseRegion(f.Region)
	if err != nil {
		return "", err
	}
	return r, nil
}

// RegionFromDomain infers a region from a stored OAuth1 token domain, used as a
// fallback for sessions created before the region marker existed.
func RegionFromDomain(domain string) Region {
	d := strings.ToLower(strings.TrimSpace(domain))
	// Older login flows stored the bare region name instead of the SSO domain.
	if strings.Contains(d, "garmin.cn") || d == "cn" {
		return RegionCN
	}
	return ""
}

// ResolveRegion applies: explicit flag > GARMIN_REGION env > persisted marker > default.
// `flagValue` is the raw --region string; `flagSet` reports whether the user passed it.
func ResolveRegion(flagValue string, flagSet bool, configDir, profile string) (Region, error) {
	if flagSet && strings.TrimSpace(flagValue) != "" {
		r, err := ParseRegion(flagValue)
		if err != nil {
			return "", err
		}
		return r, nil
	}
	if env := strings.TrimSpace(os.Getenv(RegionEnv)); env != "" {
		r, err := ParseRegion(env)
		if err != nil {
			return "", err
		}
		return r, nil
	}
	stored, err := LoadRegion(configDir, profile)
	if err != nil {
		return "", err
	}
	if stored != "" {
		return stored, nil
	}
	return RegionGlobal, nil
}
