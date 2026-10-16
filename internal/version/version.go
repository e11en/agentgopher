// Package version holds the build version of the agentgopher binary.
package version

import "runtime/debug"

// Version is set at build time with -ldflags "-X .../internal/version.Version=v1.2.3".
// A binary from `go install ...@vX.Y.Z` carries the module version in its
// build info instead; that is used when no ldflags were given.
var Version = buildVersion()

func buildVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
