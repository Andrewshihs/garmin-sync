package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// confirmDestructive returns true if the user confirms a destructive action.
//
// When force is true, it returns true without prompting. Otherwise it prompts
// on an interactive TTY; on a non-interactive stdin it returns false (the
// caller should treat that as "refused, pass --force").
func confirmDestructive(cmd *cobra.Command, prompt string, force bool) bool {
	if force {
		return true
	}
	inf, ok := cmd.InOrStdin().(*os.File)
	if !ok || !term.IsTerminal(int(inf.Fd())) {
		return false
	}
	_, _ = cmd.ErrOrStderr().Write([]byte(prompt + " [y/N]: "))
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// confirmExecution implements the two-phase destructive flow: the caller
// prints a preview of the exact target, then calls this with the user's
// decision flags. With --confirm or --force it returns true; otherwise it
// prompts on an interactive TTY, or instructs a non-interactive caller to
// re-run with --confirm (the dsh-plugin confirmationId pattern, stateless
// variant for a CLI).
func confirmExecution(cmd *cobra.Command, preview string, confirmed bool) (bool, error) {
	if confirmed {
		return true, nil
	}
	if f, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		if confirmDestructive(cmd, preview+" — execute? [y/N]: ", false) {
			return true, nil
		}
		return false, fmt.Errorf("aborted")
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s\nNon-interactive session: re-run with --confirm to execute.\n", preview)
	return false, fmt.Errorf("confirmation required (pass --confirm)")
}
