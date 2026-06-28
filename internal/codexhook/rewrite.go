package codexhook

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/stephenywilson/xit/internal/filters"
)

// shouldCompressCore reports whether the extracted core command classifies as
// high-noise (should_compress) per the same policy used by other adapters'
// observe/reroute logic, so PreToolUse only reroutes commands XiT would have
// recommended wrapping anyway.
func shouldCompressCore(core string) bool {
	return shellHighOutputStructure(core) || filters.ClassifyPolicy(strings.Fields(core)) == "should_compress"
}

// alreadyWrapped reports whether cmd already invokes `xit auto` (directly or
// via "./xit auto"), so PreToolUse never double-wraps a command the AI wrote
// itself.
func alreadyWrapped(cmd string) bool {
	c := strings.TrimSpace(cmd)
	return strings.HasPrefix(c, "xit auto ") || strings.HasPrefix(c, "./xit auto ")
}

// hasCodexTurnEnv reports whether cmd already carries our injected turn
// identity, so PreToolUse never injects it twice on hook re-delivery/retry.
func hasCodexTurnEnv(cmd string) bool {
	return strings.Contains(cmd, "XIT_CODEX_TURN_ID=")
}

var shellWrapperRe = regexp.MustCompile(`(?is)^(bash|sh)((?:\s+-[a-z]+)*)\s+["'](.+)["']$`)

// extractCodexCoreCommand mirrors the OpenCode plugin's extractCoreCommand: it
// unwraps a `bash -lc "..."` / `sh -c '...'` shell wrapper, then takes the
// last segment after && / ||, then strips a leading "command " prefix — so
// `export PATH=... && go test ./...` resolves to `go test ./...` for
// classification purposes only (the ORIGINAL cmd string is what actually gets
// rewritten, never this extracted core).
func extractCodexCoreCommand(cmd string) string {
	c := strings.TrimSpace(cmd)
	if m := shellWrapperRe.FindStringSubmatch(c); m != nil {
		c = strings.TrimSpace(m[3])
	}
	segments := splitAndOr(c)
	if len(segments) > 0 {
		c = strings.TrimSpace(segments[len(segments)-1])
	}
	c = strings.TrimPrefix(c, "command ")
	return strings.TrimSpace(c)
}

var andOrRe = regexp.MustCompile(`\s*&&\s*|\s*\|\|\s*`)

func splitAndOr(c string) []string {
	return andOrRe.Split(c, -1)
}

// hasTopLevelPipe reports whether core (already split on && / ||, so any "||"
// has been removed) still contains a bare "|" pipe. `xit auto` execs its
// argument list directly — it does not run a shell — so rerouting a piped
// command through it would only wrap the FIRST stage and silently break the
// rest of the pipeline (e.g. piping XiT's short compressed summary into
// `xargs wc -l` instead of the original command's real output). Reroute must
// never attempt this; piped commands are left untouched (passthrough).
func hasTopLevelPipe(core string) bool {
	return strings.Contains(core, "|")
}

var (
	largeBraceLoopRe = regexp.MustCompile(`(?is)\b(?:for|while)\b.*\{(-?\d+)\.\.(-?\d+)(?:\.\.\d+)?\}.*\bdo\b.*\b(?:echo|printf)\b.*\bdone\b`)
	forSeqLoopRe     = regexp.MustCompile(`(?is)\bfor\s+\w+\s+in\s+(?:\$\()?seq\s+(-?\d+)\s+(-?\d+).*?\bdo\b.*\b(?:echo|printf)\b.*\bdone\b`)
	whileOutputRe    = regexp.MustCompile(`(?is)\bwhile\b.*?(?:<=|<|-le|-lt)\s*(-?\d+).*?\bdo\b.*\b(?:echo|printf)\b.*\bdone\b`)
	seqPipeRe        = regexp.MustCompile(`(?is)(?:^|[;&|\s])seq\s+(-?\d+)\s+(-?\d+).*?\|`)
	yesRe            = regexp.MustCompile(`(?is)(?:^|[;&|\s])yes(?:\s|$)`)
)

func shellHighOutputStructure(core string) bool {
	c := strings.TrimSpace(core)
	if c == "" {
		return false
	}
	if yesRe.MatchString(c) {
		return true
	}
	for _, re := range []*regexp.Regexp{largeBraceLoopRe, forSeqLoopRe, whileOutputRe, seqPipeRe} {
		m := re.FindStringSubmatch(c)
		if len(m) >= 3 && largeRange(m[1], m[2]) {
			return true
		}
	}
	return false
}

func largeRange(a, b string) bool {
	start, err1 := strconv.Atoi(a)
	end, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil {
		return false
	}
	if end < start {
		start, end = end, start
	}
	return end-start+1 >= 200
}

func buildCodexWholeRerouteCommand(cmd, envPrefix string) string {
	return envPrefix + "xit auto " + strings.TrimSpace(cmd)
}

// buildCodexFinalCommand rewrites cmd to prepend envPrefix immediately before
// "xit auto " (recursing into a bash/sh -c shell wrapper so the env prefix
// lands inside the quoted inner command, exactly mirroring the OpenCode
// plugin's buildFinalCommand). Used both to inject turn identity into an
// already-AI-written `xit auto ...` call, and to reroute+inject a high-noise
// command that wasn't wrapped yet.
func buildCodexFinalCommand(cmd, envPrefix string) string {
	c := strings.TrimSpace(cmd)
	if m := shellWrapperRe.FindStringSubmatch(c); m != nil {
		inner := buildCodexFinalCommand(m[3], envPrefix)
		return m[1] + m[2] + ` "` + inner + `"`
	}

	lastAnd := strings.LastIndex(c, "&&")
	lastOr := strings.LastIndex(c, "||")
	splitAt := lastAnd
	if lastOr > splitAt {
		splitAt = lastOr
	}
	if splitAt > 0 {
		prefix := c[:splitAt+2]
		suffix := strings.TrimSpace(c[splitAt+2:])
		suffix = strings.TrimPrefix(suffix, "command ")
		suffix = strings.TrimSpace(suffix)
		return prefix + " " + envPrefix + suffix
	}

	if strings.HasPrefix(c, "command ") {
		return envPrefix + strings.TrimSpace(strings.TrimPrefix(c, "command "))
	}

	return envPrefix + c
}

// codexTurnEnvPrefix builds the "VAR=val VAR=val " prefix injected immediately
// before (or, for an already-wrapped command, immediately before "xit auto")
// the command. Values are single-quote-shell-escaped for safety. bridgeRunID
// is only appended when non-empty (i.e. when this PreToolUse call detected it
// is running inside VS Code) — an opaque id, never a raw session/thread/path
// value, that `xit auto` later reads back via XIT_VSCODE_BRIDGE_RUN_ID to
// finish the matching VS Code Codex Bridge pending context.
func codexTurnEnvPrefix(sessionID, turnID, bridgeRunID string) string {
	prefix := fmt.Sprintf("XIT_ADAPTER=codex XIT_CODEX_SESSION_ID=%s XIT_CODEX_TURN_ID=%s ",
		shellSingleQuote(sessionID), shellSingleQuote(turnID))
	if bridgeRunID != "" {
		prefix += fmt.Sprintf("XIT_VSCODE_BRIDGE_RUN_ID=%s ", shellSingleQuote(bridgeRunID))
	}
	return prefix
}

// shellSingleQuote wraps s in single quotes, escaping any embedded single
// quote, so injected ids can never break out of the shell-assignment context.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// RewriteCommandForTurn computes the rewritten Bash command for a PreToolUse
// hook call, plus whether a rewrite is needed at all.
//   - already carries our turn env -> no rewrite (avoid double injection).
//   - already "xit auto ..." (AI wrote it itself) -> inject env right before it.
//   - classified should_compress and not yet wrapped -> reroute AND inject env.
//   - otherwise -> no rewrite (passthrough; not XiT's concern).
func RewriteCommandForTurn(cmd, sessionID, turnID, bridgeRunID string) (rewritten string, changed bool) {
	if hasCodexTurnEnv(cmd) {
		return cmd, false
	}
	envPrefix := codexTurnEnvPrefix(sessionID, turnID, bridgeRunID)
	core := extractCodexCoreCommand(cmd)
	if alreadyWrapped(core) {
		// "xit auto ..." may be the whole command, or wrapped in bash/sh -c —
		// buildCodexFinalCommand recurses into the shell wrapper so the env
		// prefix lands immediately before "xit auto" either way. This is the
		// AI's own deliberate command (including any pipe after it), so it is
		// preserved as-is — only the env prefix is injected.
		return buildCodexFinalCommand(cmd, envPrefix), true
	}
	if shellHighOutputStructure(core) {
		return buildCodexWholeRerouteCommand(cmd, envPrefix), true
	}
	if hasTopLevelPipe(core) {
		return cmd, false
	}
	if shouldCompressCore(core) {
		return buildCodexWholeRerouteCommand(cmd, envPrefix), true
	}
	return cmd, false
}
