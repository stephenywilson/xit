package kimihook

import (
	"strings"
)

// ShouldReroute decides whether a Shell/Bash command should be rerouted through XiT.
// This runs at PreToolUse time before output is known, so it relies on
// command patterns rather than raw byte size.
func ShouldReroute(command string) (bool, string) {
	parts := parseCommand(command)
	if len(parts) == 0 {
		return false, ""
	}

	tool := parts[0]
	args := parts[1:]

	// Machine-readable outputs should pass through.
	if hasMachineReadableFlag(args) {
		return false, ""
	}

	tk := tupleKey(tool, args)

	// High-priority commands that are always noisy.
	switch tk {
	case "git diff", "git log":
		return true, "xit auto " + command
	case "npm test", "pnpm test":
		return true, "xit auto " + command
	case "pytest", "cargo test":
		return true, "xit auto " + command
	case "docker logs":
		return true, "xit auto " + command
	case "npm install", "npm ci", "pnpm install", "pnpm ci":
		return false, ""
	}

	// Check subcommands that may have additional flags.
	if len(args) > 0 {
		sub := args[0]
		switch tool + " " + sub {
		case "go test":
			return true, "xit auto " + command
		}
	}

	switch tool {
	case "rg", "grep":
		return true, "xit auto " + command
	case "tsc", "eslint":
		return true, "xit auto " + command
	case "docker":
		return true, "xit auto " + command
	case "jq":
		return false, ""
	case "find", "ls":
		return true, "xit auto " + command
	}

	return false, ""
}

func parseCommand(command string) []string {
	// Simple split; we don't need shell-level parsing.
	return strings.Fields(command)
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
