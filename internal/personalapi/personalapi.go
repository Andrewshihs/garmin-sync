// Package personalapi implements Garmin's Personal API authentication:
// a personal API key is exchanged for a 24-hour JWT bearer token, which is
// then used against the personal connectapi gateway (a whitelist of
// officially documented read endpoints).
package personalapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Andrewshihs/garmin-sync/internal/config"
)

// KeyFile returns the per-region personal API key path.
// CN:  <cfg>/connect-personal-api-key-cn   (created at connect.garmin.cn/app/settings/personalApiToken)
// GL:  <cfg>/connect-personal-api-key      (official client's default name, connect.garmin.com)
func KeyFile(cfgDir string, cn bool) string {
	if cn {
		return filepath.Join(cfgDir, "connect-personal-api-key-cn")
	}
	return filepath.Join(cfgDir, "connect-personal-api-key")
}

// LoadKey returns the personal API key for the region, or ("", nil) when no
// key is configured (caller falls back to OAuth).
func LoadKey(cfgDir string, cn bool) (string, error) {
	data, err := os.ReadFile(KeyFile(cfgDir, cn))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	key := strings.TrimSpace(string(data))
	return key, nil
}

// gatewayBase is the personal-API gateway (a whitelist proxy in front of the
// same backend services; only documented endpoints answer).
func gatewayBase(cn bool) string {
	if cn {
		return "https://personal.connectapi.garmin.cn"
	}
	return "https://personal.connectapi.garmin.com"
}

// tokenExchangePath is where an ApiKey header is traded for a bearer JWT.
const tokenExchangePath = "/personalapitoken-service/personal-api/personal-api-key/token"

type cachedToken struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"` // unix seconds
}

func tokenCacheFile(cfgDir string, cn bool) string {
	name := "personal-api-token-global.json"
	if cn {
		name = "personal-api-token-cn.json"
	}
	return filepath.Join(cfgDir, name)
}

const refreshWindow = time.Hour // re-exchange when less than this remains

// GetToken returns a cached bearer JWT or exchanges the key for a new one.
// CN responds with a bare JWT string; Global with {"access_token": ...} —
// both are handled.
func GetToken(ctx context.Context, cfgDir string, cn bool) (string, error) {
	key, err := LoadKey(cfgDir, cn)
	if err != nil {
		return "", err
	}
	if key == "" {
		return "", fmt.Errorf("no personal API key for region")
	}

	cachePath := tokenCacheFile(cfgDir, cn)
	if data, err := os.ReadFile(cachePath); err == nil {
		var ct cachedToken
		if json.Unmarshal(data, &ct) == nil && ct.Token != "" &&
			time.Until(time.Unix(ct.ExpiresAt, 0)) > refreshWindow {
			return ct.Token, nil
		}
	}

	transport := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		gatewayBase(cn)+tokenExchangePath, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Authorization", "ApiKey "+key)
	resp, err := transport.Do(req)
	if err != nil {
		return "", fmt.Errorf("personal API token exchange: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("personal API token exchange: HTTP %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}

	token, exp, err := parseTokenResponse(body)
	if err != nil {
		return "", err
	}
	if b, err := json.Marshal(cachedToken{Token: token, ExpiresAt: exp}); err == nil {
		_ = config.WriteFileAtomic(cachePath, append(b, '\n'), 0o600)
	}
	return token, nil
}

// parseTokenResponse accepts both observed shapes: a bare JWT (CN) and a
// JSON object with access_token (Global). Expiry comes from the JWT exp claim.
func parseTokenResponse(body []byte) (string, int64, error) {
	s := strings.TrimSpace(string(body))
	token := s
	var raw json.RawMessage
	if err := json.Unmarshal([]byte(s), &raw); err == nil && strings.HasPrefix(s, "{") {
		var obj struct {
			AccessToken string `json:"access_token"`
		}
		if json.Unmarshal(raw, &obj) == nil && obj.AccessToken != "" {
			token = obj.AccessToken
		}
	} else {
		token = strings.Trim(s, `"`)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", 0, fmt.Errorf("personal API token response is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", 0, fmt.Errorf("personal API JWT payload: %w", err)
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return "", 0, fmt.Errorf("personal API JWT has no exp claim")
	}
	return token, claims.Exp, nil
}
