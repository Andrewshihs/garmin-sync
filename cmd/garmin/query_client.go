package main

import (
	"context"
	"time"

	"github.com/Andrewshihs/garmin-sync/internal/auth"
	"github.com/Andrewshihs/garmin-sync/internal/client"
	"github.com/Andrewshihs/garmin-sync/internal/config"
	"github.com/Andrewshihs/garmin-sync/internal/personalapi"
	"github.com/spf13/cobra"
)

// newQueryClient builds the client for read-only data commands.
//
// Routing: when a Personal API key exists for the resolved region AND the
// command's endpoints are served by the personal gateway (officially
// documented whitelist), the personal pipeline is used — it is Garmin's
// supported API and immune to reverse-engineered-endpoint breakage. The
// endpoint set is a fixed whitelist because the personal gateway 404s
// anything undocumented (probed 2026-10-24).
//
// Commands whose endpoints are NOT on the whitelist (readiness, lactate
// threshold, VO2max, training status, race predictions, weather, splits,
// export) must keep calling newAuthedClient (OAuth pipeline).
//
// Write commands always use OAuth: the Personal API is read-only by design.
func newQueryClient(cmd *cobra.Command, opts *globalOptions, personalOK bool) (*client.Client, error) {
	cfgDir, err := config.ResolveConfigDir(opts.ConfigDir)
	if err != nil {
		return nil, err
	}
	region, err := regionForCmd(cmd, opts, cfgDir)
	if err != nil {
		return nil, err
	}

	if personalOK {
		key, err := personalapi.LoadKey(cfgDir, region.IsCN())
		if err == nil && key != "" {
			token, terr := personalapi.GetToken(context.Background(), cfgDir, region.IsCN())
			if terr == nil {
				return client.NewWithSession(cfgDir, opts.Profile, &auth.Session{
					OAuth2: auth.OAuth2Token{
						TokenType:   "bearer",
						AccessToken: token,
						// far-future so ensureFreshOAuth2 never tries an OAuth
						// refresh on the personal JWT (we manage its 24h life
						// via the token cache instead)
						ExpiresAt: time.Now().Add(6 * time.Hour).Unix(),
					},
				}, client.Options{
					BaseURL: personalGatewayBase(region.IsCN()),
					Region:  region,
					Logf:    clientOptionsForCmd(cmd, opts).Logf,
				}), nil
			}
			// token exchange failed → fall through to OAuth
		}
	}

	return newAuthedClient(cmd, opts)
}

func personalGatewayBase(cn bool) string {
	if cn {
		return "https://personal.connectapi.garmin.cn"
	}
	return "https://personal.connectapi.garmin.com"
}
