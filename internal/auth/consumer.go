package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Andrewshihs/garmin-sync/internal/config"
)

const (
	oauthConsumerURL   = "https://thegarth.s3.amazonaws.com/oauth_consumer.json"
	consumerCacheTTL   = 7 * 24 * time.Hour
	defaultHTTPTimeout = 20 * time.Second
	maxRetries         = 3
)

var retryBaseDelay = 200 * time.Millisecond

type oauthConsumer struct {
	ConsumerKey    string `json:"consumer_key"`
	ConsumerSecret string `json:"consumer_secret"`
}

type oauthConsumerCache struct {
	ConsumerKey    string `json:"consumer_key"`
	ConsumerSecret string `json:"consumer_secret"`
	FetchedAt      int64  `json:"fetched_at"`
}

// GetOAuthConsumer fetches the OAuth consumer config from S3 with retry logic.
// It first checks the cache, and only fetches from S3 if the cache is missing or expired.
func getOAuthConsumer(ctx context.Context, configDir string, httpClient *http.Client) (oauthConsumer, error) {
	cachePath := config.OAuthConsumerCachePath(configDir)

	if cached, ok := loadCachedConsumer(cachePath); ok {
		return cached, nil
	}

	if httpClient == nil {
		httpClient = &http.Client{
			Timeout:   defaultHTTPTimeout,
			Transport: defaultTransport,
		}
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 200ms, 400ms, 800ms
			delay := retryBaseDelay * (1 << (attempt - 1))
			select {
			case <-ctx.Done():
				return oauthConsumer{}, ctx.Err()
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, oauthConsumerURL, nil)
		if err != nil {
			return oauthConsumer{}, err
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			lastErr = fmt.Errorf("oauth consumer fetch failed: %s", resp.Status)
			// Don't retry on client errors (4xx)
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				return oauthConsumer{}, lastErr
			}
			continue
		}

		var raw oauthConsumer
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			resp.Body.Close()
			lastErr = err
			continue
		}
		resp.Body.Close()

		if raw.ConsumerKey == "" || raw.ConsumerSecret == "" {
			lastErr = errors.New("oauth consumer response missing fields")
			continue
		}

		cache := oauthConsumerCache{
			ConsumerKey:    raw.ConsumerKey,
			ConsumerSecret: raw.ConsumerSecret,
			FetchedAt:      time.Now().Unix(),
		}
		_ = saveJSON(cachePath, cache, 0o600)

		return raw, nil
	}

	return oauthConsumer{}, fmt.Errorf("oauth consumer fetch failed after %d attempts: %w", maxRetries+1, lastErr)
}

func loadCachedConsumer(path string) (oauthConsumer, bool) {
	cache, err := loadJSON[oauthConsumerCache](path)
	if err != nil {
		return oauthConsumer{}, false
	}

	if cache.ConsumerKey == "" || cache.ConsumerSecret == "" || cache.FetchedAt == 0 {
		return oauthConsumer{}, false
	}
	age := time.Since(time.Unix(cache.FetchedAt, 0))
	if age < 0 || age > consumerCacheTTL {
		return oauthConsumer{}, false
	}

	return oauthConsumer{
		ConsumerKey:    cache.ConsumerKey,
		ConsumerSecret: cache.ConsumerSecret,
	}, true
}
