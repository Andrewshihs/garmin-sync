package training

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLactateThresholdHeartRate(t *testing.T) {
	var gotPath, gotSport, gotAgg string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotSport = r.URL.Query().Get("sport")
		gotAgg = r.URL.Query().Get("aggregationStrategy")
		_, _ = w.Write([]byte(`[{"from":"2025-11-02","until":"2025-11-02","series":"running","value":184.0,"updatedDate":"2025-11-02"},{"from":"2025-11-15","until":"2025-11-15","series":"running","value":185.0}]`))
	}))
	defer srv.Close()

	got, err := LactateThresholdHeartRate(context.Background(), authedClientForTest(srv), "2025-10-14", "2026-10-14", "RUNNING")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/biometric-service/stats/lactateThresholdHeartRate/range/2025-10-14/2026-10-14" {
		t.Fatalf("path=%q", gotPath)
	}
	if gotSport != "RUNNING" || gotAgg != "LATEST" {
		t.Fatalf("sport=%q agg=%q", gotSport, gotAgg)
	}
	if len(got) != 2 || got[0].Value != 184 || got[1].Value != 185 || got[0].Series != "running" {
		t.Fatalf("got %+v", got)
	}
}

func TestLactateThresholdSpeed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/lactateThresholdSpeed/range/2025-10-14/2026-10-14") {
			t.Fatalf("path=%q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[{"from":"2025-11-02","until":"2025-11-02","series":"running","value":0.29166585}]`))
	}))
	defer srv.Close()

	got, err := LactateThresholdSpeed(context.Background(), authedClientForTest(srv), "2025-10-14", "2026-10-14", "RUNNING")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Value < 0.29 || got[0].Value > 0.30 {
		t.Fatalf("got %+v", got)
	}
}

func TestLactateThresholdEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	got, err := LactateThresholdHeartRate(context.Background(), authedClientForTest(srv), "2025-10-14", "2026-10-14", "RUNNING")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestNormalizeLactateSport(t *testing.T) {
	cases := map[string]string{
		"":        "RUNNING",
		"running": "RUNNING",
		"RUN":     "RUNNING",
		"cycling": "CYCLING",
		" Bike ":  "CYCLING",
	}
	for in, want := range cases {
		got, err := NormalizeLactateSport(in)
		if err != nil || got != want {
			t.Fatalf("%q -> (%q, %v); want %q", in, got, err, want)
		}
	}
	if _, err := NormalizeLactateSport("swimming"); err == nil {
		t.Fatal("expected error for unsupported sport")
	}
}
