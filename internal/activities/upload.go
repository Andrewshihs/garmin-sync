package activities

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Andrewshihs/garmin-sync/internal/client"
)

// UploadOptions describes one file upload to the Garmin upload service.
type UploadOptions struct {
	// FilePath is the file on disk to upload (.fit/.gpx/.tcx).
	FilePath string
	// Format overrides the file format: fit, gpx, or tcx. When empty the
	// format is auto-detected from the file extension.
	Format string
}

type UploadResult struct {
	DetailedImportResult DetailedImportResult `json:"detailedImportResult"`
}

// flexInt64 tolerates Garmin returning uploadId either as a number or as an
// (often empty) string depending on the endpoint's mood.
type flexInt64 int64

func (f *flexInt64) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" || s == `""` {
		*f = 0
		return nil
	}
	s = strings.Trim(s, `"`)
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return err
	}
	*f = flexInt64(v)
	return nil
}

type DetailedImportResult struct {
	UploadID  flexInt64       `json:"uploadId"`
	Successes []ImportSuccess `json:"successes"`
	Failures  []ImportFailure `json:"failures"`
}

type ImportSuccess struct {
	InternalID int64  `json:"internalId"`
	ExternalID string `json:"externalId"`
}

type ImportFailure struct {
	InternalID    int64           `json:"internalId,omitempty"`
	ExternalID    string          `json:"externalId,omitempty"`
	FailureReason string          `json:"failureReason,omitempty"`
	Messages      []ImportMessage `json:"messages,omitempty"`
}

type ImportMessage struct {
	Code    int    `json:"code"`
	Content string `json:"content"`
}

// IsDuplicate reports whether the import failure means "this activity already
// exists on the account" (Garmin returns 409 with code 202 for identical
// re-uploads).
func (f ImportFailure) IsDuplicate() bool {
	if strings.Contains(f.FailureReason, "Duplicate") {
		return true
	}
	for _, m := range f.Messages {
		if m.Code == 202 || strings.Contains(m.Content, "Duplicate") {
			return true
		}
	}
	return false
}

// FirstActivityID returns the activity id Garmin reported for the import: the
// first success's internalId, or — for duplicate re-uploads — the id of the
// pre-existing activity named in the failure detail.
func (r *UploadResult) FirstActivityID() int64 {
	if r == nil {
		return 0
	}
	if len(r.DetailedImportResult.Successes) > 0 {
		return r.DetailedImportResult.Successes[0].InternalID
	}
	for _, f := range r.DetailedImportResult.Failures {
		if f.InternalID != 0 {
			return f.InternalID
		}
	}
	return 0
}

// Duplicate reports whether the upload was rejected because the identical
// activity already exists on the account — an outcome equivalent to success.
func (r *UploadResult) Duplicate() bool {
	if r == nil {
		return false
	}
	for _, f := range r.DetailedImportResult.Failures {
		if f.IsDuplicate() {
			return true
		}
	}
	return false
}

func sanitizeFilename(s string) string {
	// The filename lands both in a URL path (/upload-service/upload/<name>)
	// and in a multipart Content-Disposition, so strip everything that is
	// hostile to either.
	return strings.NewReplacer(
		"\r", "_", "\n", "_", "\t", "_",
		`"`, "_", `\`, "_", "/", "_",
		"?", "_", "#", "_", "%", "_", "&", "_", "=", "_", ";", "_", ":", "_",
	).Replace(s)
}

// uploadContentType maps a normalized format to the multipart part content type.
func uploadContentType(format string) string {
	switch format {
	case "gpx":
		return "application/gpx+xml"
	case "tcx":
		return "application/vnd.garmin.tcx+xml"
	default:
		// fit and anything unknown: Garmin treats the part as raw bytes and
		// relies on the filename extension.
		return "application/octet-stream"
	}
}

// Upload posts one activity file to the Garmin Connect upload service and
// returns the parsed import result. The response body may be empty (some
// duplicate/async uploads); in that case the returned result carries no detail.
//
// A 409 whose failure detail says "Duplicate Activity" is returned as success:
// the activity already exists on the account, which for sync purposes is the
// same outcome as uploading it. Transient transport/status failures are
// retried (see uploadMaxAttempts).
func Upload(ctx context.Context, c *client.Client, opts UploadOptions) (*UploadResult, error) {
	data, err := os.ReadFile(opts.FilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	format := strings.ToLower(strings.TrimSpace(opts.Format))
	switch format {
	case "fit", "gpx", "tcx":
		// ok
	default:
		// Unknown values (e.g. --format json selecting JSON output) fall back
		// to extension auto-detection.
		format = strings.TrimPrefix(strings.ToLower(filepath.Ext(opts.FilePath)), ".")
	}

	filename := sanitizeFilename(filepath.Base(opts.FilePath))
	contentType := uploadContentType(format)

	var lastErr error
	for attempt := 1; attempt <= uploadMaxAttempts; attempt++ {
		if attempt > 1 {
			time.Sleep(uploadRetryBackoff * time.Duration(attempt-1))
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("upload failed: %w", err)
			}
		}

		result, err := uploadOnce(ctx, c, filename, contentType, data)
		if err == nil {
			return result, nil
		}
		lastErr = err
		var re retryableUploadError
		if !errors.As(err, &re) || ctx.Err() != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

const uploadMaxAttempts = 3

// Empirically the proxy path to Garmin's edge intermittently mangles or drops
// requests (EOF / HTML 400 / 5xx); these clear on retry.
var uploadRetryBackoff = 2 * time.Second

type retryableUploadError struct{ err error }

func (e retryableUploadError) Error() string { return e.err.Error() }
func (e retryableUploadError) Unwrap() error { return e.err }

func uploadOnce(ctx context.Context, c *client.Client, filename, contentType string, data []byte) (*UploadResult, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	h := make(textproto.MIMEHeader)
	h["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename)}
	h["Content-Type"] = []string{contentType}
	part, err := mw.CreatePart(h)
	if err != nil {
		return nil, fmt.Errorf("upload failed: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return nil, fmt.Errorf("upload failed: %w", err)
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("upload failed: %w", err)
	}

	path := fmt.Sprintf("/upload-service/upload/%s", filename)
	resp, err := c.DoRawWithHeaders(
		ctx,
		http.MethodPost,
		path,
		nil,
		&body,
		mw.FormDataContentType(),
		"application/json",
		map[string]string{
			"DI-Backend": c.Region().APIHost(),
		},
	)
	if err != nil {
		return nil, retryableUploadError{fmt.Errorf("upload failed: %w", err)}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, retryableUploadError{fmt.Errorf("upload failed: %w", err)}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		statusErr := fmt.Errorf("upload failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
		if resp.StatusCode == http.StatusConflict {
			// Identical re-upload: Garmin names the pre-existing activity in
			// the failure detail. Treat as done, not as an error.
			result := &UploadResult{}
			if json.Unmarshal(respBody, result) == nil && result.Duplicate() {
				return result, nil
			}
			return nil, statusErr
		}
		if resp.StatusCode == 400 || resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode >= 500 {
			return nil, retryableUploadError{statusErr}
		}
		return nil, statusErr
	}

	result := &UploadResult{}
	if len(bytes.TrimSpace(respBody)) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return nil, fmt.Errorf("upload failed: %w", err)
		}
	}
	return result, nil
}
