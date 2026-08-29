// Package shellescape is a local minimal stub used by the offline build.
package shellescape

import "strings"

// Quote returns a minimally shell-quoted copy of s for command lines.
func Quote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n\"'\\$`!#&|;<>(){}[]*?~^") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
