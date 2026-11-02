// Package version identifies the running artifact without querying the network
// or the checkout at runtime. Release builds inject the same values as OCI labels.
package version

import "runtime/debug"

// Release and Revision are set with go build -ldflags -X. Plain source builds
// use the VCS revision embedded by the Go toolchain instead.
var Release = "dev"
var Revision = "unknown"

// String prefers the release version, then an explicitly supplied revision,
// then Go's VCS build metadata. A source archive without metadata reports dev.
func String() string {
	info, _ := debug.ReadBuildInfo()
	return Resolve(Release, Revision, info)
}

// Resolve applies artifact precedence to supplied metadata. Keeping resolution
// pure makes source builds and release builds testable without building binaries.
func Resolve(release, revision string, info *debug.BuildInfo) string {
	if release != "" && release != "dev" {
		return release
	}
	if revision != "" && revision != "unknown" {
		return revision
	}
	if info != nil {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				return setting.Value
			}
		}
	}
	return "dev"
}
