// Package browser opens the default system browser at a URL. It is a thin,
// platform-isolated helper; failures are returned rather than fatal so the
// app keeps running in headless environments.
package browser

import (
	"os/exec"
	"runtime"
)

// Open launches the default browser at url. It returns an error when no
// browser mechanism is available.
func Open(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// rundll32 respects the default browser association.
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default: // linux/other unix
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
