// Package version resolves the CLI version for --version output and the
// User-Agent header. Release builds inject Version via -ldflags; source
// builds fall back to Go module build info, then "dev".
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// Injected at release time via:
//
//	-ldflags "-X github.com/ai-celeris/celeris-cli/internal/version.Version=1.2.3 ..."
var (
	Version = ""
	Commit  = ""
	Date    = ""
)

// String returns the best available version identifier.
func String() string {
	if Version != "" {
		return strings.TrimPrefix(Version, "v")
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return strings.TrimPrefix(v, "v")
		}
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				return "dev-" + s.Value[:7]
			}
		}
	}
	return "dev"
}

// UserAgent identifies the CLI, its version, and the platform in every
// request so server-side logs can attribute traffic to a CLI build.
func UserAgent() string {
	return fmt.Sprintf("celeris-cli/%s (%s; %s) go/%s",
		String(), runtime.GOOS, runtime.GOARCH,
		strings.TrimPrefix(runtime.Version(), "go"))
}

// Full returns the multi-field form used by `celeris version`.
func Full() string {
	s := fmt.Sprintf("celeris %s", String())
	if Commit != "" {
		s += fmt.Sprintf(" (commit %s", Commit)
		if Date != "" {
			s += fmt.Sprintf(", built %s", Date)
		}
		s += ")"
	}
	return fmt.Sprintf("%s %s/%s %s", s, runtime.GOOS, runtime.GOARCH, runtime.Version())
}
