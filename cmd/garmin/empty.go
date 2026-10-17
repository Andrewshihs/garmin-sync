package main

import (
	"fmt"

	"github.com/Andrewshihs/garmin-sync/internal/output"
	"github.com/spf13/cobra"
)

type emptyJSON struct {
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

// emitEmpty renders an empty outcome for the requested format (JSON consumers
// get a reason object instead of an empty array; human output gets a note on
// stderr so stdout stays clean) and returns the sentinel error that maps to
// the dedicated exit code (2 = no_data, 3 = unsupported).
func emitEmpty(cmd *cobra.Command, opts *globalOptions, reason, detail string) error {
	if opts.Format == "json" {
		_ = output.JSONTo(cmd.OutOrStdout(), emptyJSON{Reason: reason, Detail: detail})
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "no data (%s): %s\n", reason, detail)
	}
	return &emptyDataError{reason: reason, detail: detail}
}

// dateRangeDetail renders a compact description of the requested date set for
// empty-state details.
func dateRangeDetail(dates []string) string {
	if len(dates) == 1 {
		return dates[0]
	}
	return fmt.Sprintf("%s..%s (%d days)", dates[0], dates[len(dates)-1], len(dates))
}
