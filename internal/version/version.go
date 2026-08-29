// Package version holds build metadata for the RemoraSFTP executable.
// Values are injected with -ldflags at release build time.
package version

import "runtime"

var (
	// Version is the semantic version of the application.
	Version = "0.1.0-dev"
	// Commit is the git commit the binary was built from.
	Commit = "unknown"
	// BuildTime is the UTC build timestamp.
	BuildTime = "unknown"
)

// Info describes a running build.
type Info struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"buildTime"`
	GoVersion string `json:"goVersion"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

// Get returns the populated build info.
func Get() Info {
	return Info{
		Name:      "RemoraSFTP",
		Version:   Version,
		Commit:    Commit,
		BuildTime: BuildTime,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}

// UserAgent returns the local API user agent string.
func UserAgent() string {
	return "RemoraSFTP/" + Version
}
