package version

var (
	// Version is the build-time version string, injected via ldflags.
	Version = "0.1.0-dev"
	// CommitSHA is the build-time git commit SHA, injected via ldflags.
	CommitSHA = "unknown"
)
