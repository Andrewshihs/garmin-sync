package main

import "fmt"

type cliError struct {
	err      error
	rendered bool
}

func (e *cliError) Error() string {
	return e.err.Error()
}

func (e *cliError) Unwrap() error {
	return e.err
}

func renderedError(err error) error {
	if err == nil {
		return nil
	}
	return &cliError{err: err, rendered: true}
}

// Empty-state taxonomy: consumers (scripts, SKILL agents) need to tell apart
// "the account/device simply has no data for this request" from "this device
// never produces the metric at all" from authentication problems.
// Not-authenticated stays exit 1 (renderedError); the other two get their own
// exit codes and a JSON `reason`.
const (
	EmptyReasonNoData      = "no_data"     // exit 2: request fine, zero records
	EmptyReasonUnsupported = "unsupported" // exit 3: device/account does not produce this metric
)

type emptyDataError struct {
	reason string
	detail string
}

func (e *emptyDataError) Error() string {
	if e.detail == "" {
		return fmt.Sprintf("no data (%s)", e.reason)
	}
	return fmt.Sprintf("%s: %s", e.reason, e.detail)
}

func emptyExitCode(reason string) int {
	if reason == EmptyReasonUnsupported {
		return 3
	}
	return 2
}
