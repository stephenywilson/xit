package autoshim

import (
	"strings"
)

// ShouldCompress decides whether a command's output should be compressed.
func ShouldCompress(tool string, args []string, rawBytes int, exitCode int) bool {
	// Machine-readable outputs should not be rewritten.
	if hasMachineReadableFlag(args) {
		return false
	}

	tk := tupleKey(tool, args)

	// High-priority commands that are always noisy regardless of size.
	switch tk {
	case "git diff", "git log":
		return true
	case "go test":
		return true
	case "npm test", "pnpm test":
		return true
	case "pytest", "cargo test":
		return true
	case "docker logs":
		return true
	case "npm install", "npm ci", "pnpm install", "pnpm ci":
		return false
	}

	switch tool {
	case "rg", "grep":
		return true
	case "tsc", "eslint":
		return rawBytes >= 400 || exitCode != 0
	case "docker":
		return rawBytes >= 800 || exitCode != 0
	case "jq":
		return false // jq is usually machine-readable
	}

	// If output is tiny and command succeeded, passthrough to avoid overhead.
	if rawBytes < 800 && exitCode == 0 {
		return false
	}

	// Default: compress only large successful outputs or any failing output.
	if exitCode != 0 {
		return rawBytes >= 400
	}
	return rawBytes >= 800
}

func hasMachineReadableFlag(args []string) bool {
	for _, a := range args {
		switch a {
		case "--json", "--porcelain", "-z", "--format", "--quiet", "-q",
			"--raw-output", "--compact-output", "--null", "--no-color",
			"--output-format", "--input-format":
			return true
		}
		if strings.HasPrefix(a, "--format=") || strings.HasPrefix(a, "--output=") {
			return true
		}
	}
	return false
}

func tupleKey(tool string, args []string) string {
	if len(args) == 0 {
		return tool
	}
	if len(args) == 1 {
		return tool + " " + args[0]
	}
	return tool + " " + args[0] + " " + args[1]
}
