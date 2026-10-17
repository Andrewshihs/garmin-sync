package main

import (
	"errors"
	"fmt"
	"os"
)

var version = "dev"

func main() {
	if err := NewRootCmd(version).Execute(); err != nil {
		var ce *cliError
		if errors.As(err, &ce) && ce.rendered {
			os.Exit(1)
		}

		var ee *emptyDataError
		if errors.As(err, &ee) {
			// The command already rendered its empty-state notice/JSON.
			os.Exit(emptyExitCode(ee.reason))
		}

		_, _ = fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
