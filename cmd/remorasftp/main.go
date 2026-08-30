// Command remorasftp is the single executable for the RemoraSFTP local engine.
//
// Launching it with no arguments starts the loopback server and (in GUI
// mode) opens the default browser at the embedded file-manager UI. The same
// binary provides CLI commands (connect, ls, put, get, ...) that use the
// identical internal services.
package main

import (
	"fmt"
	"os"

	"remorasftp/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "remorasftp:", err)
		os.Exit(1)
	}
}
