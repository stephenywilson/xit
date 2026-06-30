package telemetry

import "runtime"

// osLabel normalizes runtime.GOOS to the closed set the schema allows.
func osLabel() string {
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
		return runtime.GOOS
	default:
		return "unknown"
	}
}

// archLabel normalizes runtime.GOARCH to the closed set the schema allows.
func archLabel() string {
	switch runtime.GOARCH {
	case "arm64":
		return "arm64"
	case "amd64":
		return "amd64"
	default:
		return "unknown"
	}
}
