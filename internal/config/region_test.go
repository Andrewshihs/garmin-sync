package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegionEndpoints(t *testing.T) {
	cn := RegionCN
	global := RegionGlobal

	if cn.APIBase() != "https://connectapi.garmin.cn" {
		t.Errorf("cn APIBase = %q", cn.APIBase())
	}
	if global.APIBase() != "https://connectapi.garmin.com" {
		t.Errorf("global APIBase = %q", global.APIBase())
	}
	if cn.SSOBase() != "https://sso.garmin.cn/sso" {
		t.Errorf("cn SSOBase = %q", cn.SSOBase())
	}
	if cn.SSOEmbed() != "https://sso.garmin.cn/sso/embed" {
		t.Errorf("cn SSOEmbed = %q", cn.SSOEmbed())
	}
	if cn.SSOMFA() != "https://sso.garmin.cn/sso/verifyMFA/loginEnterMfaCode" {
		t.Errorf("cn SSOMFA = %q", cn.SSOMFA())
	}
	if cn.Domain() != "garmin.cn" || global.Domain() != "garmin.com" {
		t.Errorf("domains wrong: cn=%q global=%q", cn.Domain(), global.Domain())
	}
	if cn.APIHost() != "connectapi.garmin.cn" {
		t.Errorf("cn APIHost = %q", cn.APIHost())
	}
	if cn.WebBase() != "https://connect.garmin.cn/modern" {
		t.Errorf("cn WebBase = %q", cn.WebBase())
	}
	// Empty region must behave like global, never produce "https://<empty>".
	if Region("").APIBase() != "https://connectapi.garmin.com" {
		t.Errorf("empty region APIBase = %q", Region("").APIBase())
	}
}

func TestParseRegion(t *testing.T) {
	for _, in := range []string{"cn", "CN", " garmin.cn ", "china", "中国", "国区"} {
		r, err := ParseRegion(in)
		if err != nil || r != RegionCN {
			t.Errorf("ParseRegion(%q) = %q, %v; want cn", in, r, err)
		}
	}
	for _, in := range []string{"", "global", "com", "garmin.com"} {
		r, err := ParseRegion(in)
		if err != nil {
			t.Errorf("ParseRegion(%q) error: %v", in, err)
		}
		if in == "" {
			if r != "" {
				t.Errorf(`ParseRegion("") = %q; want empty (unset)`, r)
			}
			continue
		}
		if r != RegionGlobal {
			t.Errorf("ParseRegion(%q) = %q; want global", in, r)
		}
	}
	if _, err := ParseRegion("eu"); err == nil {
		t.Error("ParseRegion(eu) expected error")
	}
	if err := Region("weird").Validate(); err == nil {
		t.Error("Validate(weird) expected error")
	}
}

func TestSaveLoadRegionRoundTrip(t *testing.T) {
	dir := t.TempDir()

	got, err := LoadRegion(dir, "cn")
	if err != nil {
		t.Fatalf("LoadRegion before save: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty marker, got %q", got)
	}

	if err := SaveRegion(dir, "cn", RegionCN); err != nil {
		t.Fatalf("SaveRegion: %v", err)
	}
	got, err = LoadRegion(dir, "cn")
	if err != nil || got != RegionCN {
		t.Fatalf("LoadRegion = %q, %v; want cn", got, err)
	}

	// Marker lives inside the profile's token dir, so `garmin auth logout` takes it with it.
	want := filepath.Join(dir, "tokens", "cn", "region.json")
	if p := RegionPath(dir, "cn"); p != want {
		t.Errorf("RegionPath = %q; want %q", p, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("marker file missing: %v", err)
	}
}

func TestResolveRegionPrecedence(t *testing.T) {
	dir := t.TempDir()
	if err := SaveRegion(dir, "cn", RegionCN); err != nil {
		t.Fatal(err)
	}

	t.Setenv(RegionEnv, "")

	// 1) persisted marker when nothing else is set
	r, err := ResolveRegion("", false, dir, "cn")
	if err != nil || r != RegionCN {
		t.Fatalf("stored region = %q, %v; want cn", r, err)
	}

	// 2) explicit flag wins over the marker
	r, err = ResolveRegion("global", true, dir, "cn")
	if err != nil || r != RegionGlobal {
		t.Fatalf("flag override = %q, %v; want global", r, err)
	}

	// 3) env wins over the marker when the flag is absent
	t.Setenv(RegionEnv, "global")
	r, err = ResolveRegion("", false, dir, "cn")
	if err != nil || r != RegionGlobal {
		t.Fatalf("env override = %q, %v; want global", r, err)
	}

	// 4) unknown region is an error, not a silent fallback
	if _, err := ResolveRegion("mars", true, dir, "cn"); err == nil {
		t.Error("ResolveRegion(mars) expected error")
	}

	// 5) profile with no marker defaults to global
	r, err = ResolveRegion("", false, dir, "brandnew")
	if err != nil || r != RegionGlobal {
		t.Fatalf("default = %q, %v; want global", r, err)
	}
}

func TestRegionFromDomainFallback(t *testing.T) {
	if got := RegionFromDomain("garmin.cn"); got != RegionCN {
		t.Errorf("garmin.cn -> %q; want cn", got)
	}
	// Tokens written by older login flows may carry the bare region name.
	if got := RegionFromDomain("cn"); got != RegionCN {
		t.Errorf("cn -> %q; want cn", got)
	}
	if got := RegionFromDomain(" CN "); got != RegionCN {
		t.Errorf(`" CN " -> %q; want cn`, got)
	}
	if got := RegionFromDomain("garmin.com"); got != "" {
		t.Errorf("garmin.com -> %q; want empty", got)
	}
	if got := RegionFromDomain("global"); got != "" {
		t.Errorf("global -> %q; want empty (global is the default anyway)", got)
	}
	if got := RegionFromDomain("").OrDefault(); got != RegionGlobal {
		t.Errorf(`"" -> %q; want global`, got)
	}
}
