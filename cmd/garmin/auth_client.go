package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Andrewshihs/garmin-sync/internal/auth"
	"github.com/Andrewshihs/garmin-sync/internal/client"
	"github.com/Andrewshihs/garmin-sync/internal/config"
	"github.com/Andrewshihs/garmin-sync/internal/output"
	"github.com/spf13/cobra"
)

func newAuthedClient(cmd *cobra.Command, opts *globalOptions) (*client.Client, error) {
	cfgDir, err := config.ResolveConfigDir(opts.ConfigDir)
	if err != nil {
		return nil, err
	}

	region, err := regionForCmd(cmd, opts, cfgDir)
	if err != nil {
		return nil, err
	}
	return newAuthedClientFor(cmd, opts, opts.Profile, region)
}

// newAuthedClientFor builds a client for an explicit profile+region pair.
// `sync` uses it to address two profiles in one command; everything else goes
// through newAuthedClient.
func newAuthedClientFor(cmd *cobra.Command, opts *globalOptions, profile string, region config.Region) (*client.Client, error) {
	cfgDir, err := config.ResolveConfigDir(opts.ConfigDir)
	if err != nil {
		return nil, err
	}

	// Load app config for timeout settings
	appCfg, _ := config.LoadAppConfig(cfgDir)

	clientOpts := clientOptionsForCmd(cmd, opts)
	clientOpts.Timeout = appCfg.GetHTTPTimeout()
	clientOpts.Region = region

	c, err := client.New(cfgDir, profile, clientOpts)
	if err == nil {
		return c, nil
	}
	if errors.Is(err, auth.ErrNotAuthenticated) {
		_ = renderNotAuthenticatedTo(cmd.ErrOrStderr(), opts.Format, profile)
		return nil, renderedError(err)
	}

	return nil, fmt.Errorf("init client: %w", err)
}

// regionForCmd resolves the Connect region for the current command:
// explicit --region > GARMIN_REGION > region recorded for the profile at login > global.
func regionForCmd(cmd *cobra.Command, opts *globalOptions, cfgDir string) (config.Region, error) {
	if opts == nil {
		return config.RegionGlobal, nil
	}
	flagSet := cmd != nil && cmd.Flags().Changed("region")
	return config.ResolveRegion(opts.Region, flagSet, cfgDir, opts.Profile)
}

func clientOptionsForCmd(cmd *cobra.Command, opts *globalOptions) client.Options {
	var out client.Options

	// Allows advanced users (and tests) to point the CLI at a different Connect API base URL.
	// Example: http://localhost:8080
	if baseURL := strings.TrimSpace(os.Getenv("GARMIN_CONNECTAPI_BASE_URL")); baseURL != "" {
		out.BaseURL = baseURL
	}

	if opts == nil || !opts.Verbose || opts.Quiet {
		return out
	}

	out.Logf = func(format string, args ...any) {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "garmin verbose: "+format+"\n", args...)
	}
	return out
}

func handleAuthedErrorTo(w io.Writer, opts *globalOptions, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, auth.ErrNotAuthenticated) {
		_ = renderNotAuthenticatedTo(w, opts.Format, opts.Profile)
		return renderedError(err)
	}
	return err
}

func renderNotAuthenticatedTo(w io.Writer, format, profile string) error {
	profile = orDefault(profile, "default")

	switch format {
	case "json":
		return output.JSONTo(w, notAuthenticatedJSON{
			Error:   "not_authenticated",
			Message: "Run `garmin auth login`",
			Profile: profile,
		})
	default:
		return renderKVTo(w, format, "Authentication", map[string]string{
			"status":  "not authenticated",
			"message": "Run `garmin auth login`",
			"profile": profile,
		})
	}
}

type notAuthenticatedJSON struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Profile string `json:"profile"`
}
