package cli

import (
	"fmt"

	"remorasftp/internal/protocol"
)

// trustPromptMessage renders a human-readable note for trust/auth failures
// encountered by one-shot CLI commands.
func trustPromptMessage(err error) string {
	if u, ok := protocol.AsUnknownHostKey(err); ok {
		return fmt.Sprintf("Unverified server identity for %s.\n  SSH host key fingerprint: %s (%s)",
			u.HostPort, u.Fingerprint, u.KeyType)
	}
	if ce, ok := protocol.AsCertError(err); ok {
		return fmt.Sprintf("Unverified server certificate for %s.\n  Certificate fingerprint: %s\n  Subject: %s\n  %s",
			ce.HostPort, ce.Fingerprint, ce.Subject, ce.Detail)
	}
	return ""
}
