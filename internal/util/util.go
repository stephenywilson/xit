package util

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

func TimestampSlug() string {
	return time.Now().Format("20060102-150405-000")
}

func CommandSlug(args []string) string {
	if len(args) == 0 {
		return "cmd"
	}
	// Take first 3 args, sanitize
	parts := make([]string, 0, len(args))
	for i, a := range args {
		if i >= 3 {
			break
		}
		parts = append(parts, sanitizeSlug(a))
	}
	return strings.Join(parts, "-")
}

var slugRe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func sanitizeSlug(s string) string {
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 30 {
		s = s[:30]
	}
	if s == "" {
		return "x"
	}
	return s
}

func EstimateTokens(b []byte) int {
	// Rough estimate: bytes/4 for UTF-8 text
	if utf8.Valid(b) {
		return len(b) / 4
	}
	return len(b) / 4
}

func TruncateLines(lines []string, max int) []string {
	if len(lines) <= max {
		return lines
	}
	return lines[:max]
}

func DeduplicateConsecutive(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	out := []string{lines[0]}
	for i := 1; i < len(lines); i++ {
		if lines[i] != lines[i-1] {
			out = append(out, lines[i])
		}
	}
	return out
}

func RunDedupWithCount(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	type entry struct {
		line  string
		count int
	}
	entries := []entry{{line: lines[0], count: 1}}
	for i := 1; i < len(lines); i++ {
		if lines[i] == entries[len(entries)-1].line {
			entries[len(entries)-1].count++
		} else {
			entries = append(entries, entry{line: lines[i], count: 1})
		}
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.count > 1 {
			out = append(out, fmt.Sprintf("%s (×%d)", e.line, e.count))
		} else {
			out = append(out, e.line)
		}
	}
	return out
}

func ContainsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func IsHighRiskPath(path string) bool {
	lower := strings.ToLower(path)
	risky := []string{
		"auth", "session", "login", "logout", "password", "secret",
		"payment", "billing", "stripe", "paypal",
		"security", "crypto", "encrypt", "decrypt",
		"config", "env", "settings", ".env",
		"ci", "cd", "deploy", "dockerfile", "k8s", "kube",
		"nginx", "apache", "proxy",
		"migrate", "migration", "schema",
		"permission", "role", "acl", "rbac",
		"firewall", "cors", "csrf", "xss", "sql",
	}
	for _, r := range risky {
		if strings.Contains(lower, r) {
			return true
		}
	}
	return false
}

func GroupByDir(paths []string) map[string][]string {
	groups := make(map[string][]string)
	for _, p := range paths {
		dir := p
		if idx := strings.LastIndex(p, "/"); idx >= 0 {
			dir = p[:idx]
		} else {
			dir = "."
		}
		if dir == "" {
			dir = "."
		}
		groups[dir] = append(groups[dir], p)
	}
	return groups
}

func ShortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)[:8]
}
