package activities

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Andrewshihs/garmin-sync/internal/auth"
	"github.com/Andrewshihs/garmin-sync/internal/client"
)

func uploadTestClient(srv *httptest.Server) *client.Client {
	sess := &auth.Session{OAuth2: auth.OAuth2Token{TokenType: "bearer", AccessToken: "x", ExpiresAt: time.Now().Add(time.Hour).Unix()}}
	return client.NewWithSession("", "", sess, client.Options{HTTPClient: srv.Client(), BaseURL: srv.URL})
}

func TestUpload(t *testing.T) {
	var gotPath, gotDisposition, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s", r.Method)
		}
		gotPath = r.URL.Path
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		data, _ := io.ReadAll(file)
		gotDisposition = header.Header.Get("Content-Disposition")
		gotContentType = header.Header.Get("Content-Type")
		if string(data) != "FITDATA" {
			t.Fatalf("data=%q", data)
		}
		_ = json.NewEncoder(w).Encode(UploadResult{
			DetailedImportResult: DetailedImportResult{
				UploadID:  42,
				Successes: []ImportSuccess{{InternalID: 1234, ExternalID: "ext-1"}},
			},
		})
	}))
	defer srv.Close()

	dir := t.TempDir()
	p := filepath.Join(dir, "activity.fit")
	if err := os.WriteFile(p, []byte("FITDATA"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Upload(context.Background(), uploadTestClient(srv), UploadOptions{FilePath: p})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/upload-service/upload/activity.fit" {
		t.Fatalf("path=%q", gotPath)
	}
	if !strings.Contains(gotDisposition, `name="file"`) || !strings.Contains(gotDisposition, `filename="activity.fit"`) {
		t.Fatalf("disposition=%q", gotDisposition)
	}
	if gotContentType != "application/octet-stream" {
		t.Fatalf("contentType=%q", gotContentType)
	}
	if res.DetailedImportResult.UploadID != 42 || len(res.DetailedImportResult.Successes) != 1 || res.DetailedImportResult.Successes[0].InternalID != 1234 {
		t.Fatalf("res=%+v", res)
	}
}

func TestUploadFormatOverridesExtension(t *testing.T) {
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		_, _ = io.ReadAll(file)
		gotContentType = header.Header.Get("Content-Type")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	p := filepath.Join(dir, "noext")
	if err := os.WriteFile(p, []byte("<gpx/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Upload(context.Background(), uploadTestClient(srv), UploadOptions{FilePath: p, Format: "GPX"}); err != nil {
		t.Fatal(err)
	}
	if gotContentType != "application/gpx+xml" {
		t.Fatalf("contentType=%q", gotContentType)
	}
}

func TestUploadEmptyBodyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	dir := t.TempDir()
	p := filepath.Join(dir, "a.fit")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Upload(context.Background(), uploadTestClient(srv), UploadOptions{FilePath: p})
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || len(res.DetailedImportResult.Successes) != 0 {
		t.Fatalf("res=%+v", res)
	}
}

func TestUploadStatusError(t *testing.T) {
	old := uploadRetryBackoff
	uploadRetryBackoff = 0
	t.Cleanup(func() { uploadRetryBackoff = old })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad file", http.StatusBadRequest)
	}))
	defer srv.Close()

	dir := t.TempDir()
	p := filepath.Join(dir, "a.fit")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Upload(context.Background(), uploadTestClient(srv), UploadOptions{FilePath: p})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "upload failed with status 400") || !strings.Contains(err.Error(), "bad file") {
		t.Fatalf("err=%v", err)
	}
}

func TestUploadDuplicateConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"detailedImportResult":{"uploadId":"","owner":123456789,"fileName":"garmin_sync_12345678.gpx","successes":[],"failures":[{"internalId":98765432101,"externalId":"1700000000000","messages":[{"code":202,"content":"Duplicate Activity."}]}]}}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	p := filepath.Join(dir, "garmin_sync_12345678.gpx")
	if err := os.WriteFile(p, []byte("<gpx/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Upload(context.Background(), uploadTestClient(srv), UploadOptions{FilePath: p})
	if err != nil {
		t.Fatalf("duplicate must not be an error: %v", err)
	}
	if !res.Duplicate() {
		t.Fatal("expected Duplicate()")
	}
	if got := res.FirstActivityID(); got != 98765432101 {
		t.Fatalf("FirstActivityID=%d", got)
	}
}

func TestUpload409NonDuplicate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"detailedImportResult":{"uploadId":"x","successes":[],"failures":[{"externalId":"e1","failureReason":"Invalid file"}]}}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	p := filepath.Join(dir, "a.fit")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Upload(context.Background(), uploadTestClient(srv), UploadOptions{FilePath: p})
	if err == nil || !strings.Contains(err.Error(), "status 409") {
		t.Fatalf("err=%v", err)
	}
}

func TestUploadRetriesTransientServerError(t *testing.T) {
	old := uploadRetryBackoff
	uploadRetryBackoff = 0
	t.Cleanup(func() { uploadRetryBackoff = old })

	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) < 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"detailedImportResult":{"uploadId":3,"successes":[{"internalId":99}],"failures":[]}}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	p := filepath.Join(dir, "a.fit")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Upload(context.Background(), uploadTestClient(srv), UploadOptions{FilePath: p})
	if err != nil {
		t.Fatalf("retry should recover: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d", attempts)
	}
	if got := res.FirstActivityID(); got != 99 {
		t.Fatalf("FirstActivityID=%d", got)
	}
}

func TestUploadMissingFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	_, err := Upload(context.Background(), uploadTestClient(srv), UploadOptions{FilePath: "/nonexistent/a.fit"})
	if err == nil || !strings.Contains(err.Error(), "failed to read file") {
		t.Fatalf("err=%v", err)
	}
}

func TestSanitizeFilename(t *testing.T) {
	got := sanitizeFilename("we\"ird\rname\n.fit")
	if got != "we_ird_name_.fit" {
		t.Fatalf("got=%q", got)
	}
}
