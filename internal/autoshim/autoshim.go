package autoshim

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var DefaultTools = []string{
	"git", "go", "grep", "rg", "npm", "pnpm", "pytest", "cargo",
	"docker", "tsc", "eslint", "jq", "find", "ls", "cat", "head", "tail",
}

const ShimMarker = "# XiT session auto shim"

// CreateShims creates session-scoped shim scripts inside the session directory.
// It returns the shim directory path, extra env vars to inject, and an error.
func CreateShims(sessionDir, xitBin string, tools []string) (string, []string, error) {
	shimDir := filepath.Join(sessionDir, "shims")
	if err := os.MkdirAll(shimDir, 0755); err != nil {
		return "", nil, fmt.Errorf("cannot create shim dir: %w", err)
	}

	if xitBin == "" {
		var err error
		xitBin, err = exec.LookPath("xit")
		if err != nil {
			// Fallback to current executable path
			xitBin = os.Args[0]
		}
	}

	// Resolve original paths for all tools before PATH is modified.
	var envVars []string
	envVars = append(envVars, fmt.Sprintf("XIT_BIN=%s", xitBin))

	for _, tool := range tools {
		orig, err := exec.LookPath(tool)
		if err != nil || orig == "" {
			continue
		}
		// Skip if the found path is inside our own shim directory.
		if strings.HasPrefix(orig, shimDir) {
			continue
		}
		envVar := fmt.Sprintf("XIT_ORIGINAL_%s", strings.ToUpper(tool))
		envVars = append(envVars, fmt.Sprintf("%s=%s", envVar, orig))
	}

	// Write shim scripts.
	for _, tool := range tools {
		shimPath := filepath.Join(shimDir, tool)
		script := fmt.Sprintf("%s\n# target: %s\nexec \"$XIT_BIN\" auto %s \"$@\"\n", ShimMarker, tool, tool)
		if err := os.WriteFile(shimPath, []byte(script), 0755); err != nil {
			return "", nil, fmt.Errorf("cannot write shim for %s: %w", tool, err)
		}
	}

	return shimDir, envVars, nil
}

// ResolveOriginal finds the original binary path for a tool, avoiding recursion.
func ResolveOriginal(tool string) string {
	envVar := fmt.Sprintf("XIT_ORIGINAL_%s", strings.ToUpper(tool))
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	// Fallback: search PATH but try to avoid our own shims.
	origPath, err := exec.LookPath(tool)
	if err != nil {
		return ""
	}
	// If the found path is itself a XiT shim, strip shim dirs from PATH and retry.
	if IsManagedShim(origPath) {
		cleaned := stripShimDirsFromPath(os.Getenv("PATH"))
		cmd := exec.Command("sh", "-c", fmt.Sprintf("PATH=%q command -v %s", cleaned, tool))
		out, err := cmd.Output()
		if err == nil {
			p := strings.TrimSpace(string(out))
			if p != "" {
				return p
			}
		}
	}
	return origPath
}

func IsManagedShim(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.HasPrefix(string(data), ShimMarker)
}

func stripShimDirsFromPath(pathEnv string) string {
	parts := strings.Split(pathEnv, string(os.PathListSeparator))
	var out []string
	for _, p := range parts {
		if strings.Contains(p, ".xit/sessions") && strings.Contains(p, "/shims") {
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, string(os.PathListSeparator))
}
