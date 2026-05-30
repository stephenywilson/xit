package kimirulesinstall

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const SkillName = "xit"

const skillContent = `---
name: XiT
description: Compress high-output shell commands through xit auto to save context window.
---

# XiT Mandatory Routing Contract

## Always use xit auto for high-noise commands

You MUST use xit auto when executing the following commands:

- go test -v ./...
- go test ./... when verbose or large project
- grep -r / grep -rn / grep -R across repo
- rg / ripgrep across repo
- git diff
- git log
- docker logs
- npm test
- pnpm test
- pytest
- cargo test
- tsc
- eslint
- find . when recursive
- cat large files
- head/tail large logs

Correct examples:
  xit auto go test -v ./...
  xit auto grep -rn '^func ' .
  xit auto git diff
  xit auto npm test

Incorrect examples (DO NOT do this):
  go test -v ./...
  grep -rn '^func ' .
  git diff

## Do NOT use xit auto for passthrough commands

You MUST NOT use xit auto for the following commands:

- git status
- git branch
- go version
- node --version
- npm install
- pnpm install
- jq
- commands with --json
- commands with --porcelain
- commands with -z
- short diagnostic commands
- version commands
- install commands

Correct examples:
  git status
  go version
  npm install

Incorrect examples (DO NOT do this):
  xit auto git status
  xit auto go version
  xit auto npm install

## Before every Shell command

Before executing any shell command, classify it internally:

1. high-noise → use xit auto
2. short/structured/install/version/json → passthrough
3. unknown → prefer passthrough unless likely high-output

## After xit auto

After xit auto completes, report only:

- command
- exit_code
- estimated_reduction
- raw_log path
- key facts
- next action

Do NOT paste raw output.
Do NOT paste long tables.
Do NOT repeat entire XiT Auto Summary if not needed.

## Compact final report discipline

Default final report must be compact:

- maximum 80 lines
- no duplicated sections
- no giant tables unless explicitly requested
- no full command output pasted
- no repeated raw logs
- no verbose "I analyzed..." paragraphs
- if details are long, summarize and point to raw_log

## Compact Report Contract

Default report must be:

- concise
- non-duplicated
- no long tables unless asked
- no raw command output
- no full logs
- no repeated command list if already shown
- no more than 80 lines unless user explicitly asks for full report

For verification reports, use this structure:

1. RESULT
2. CHANGED FILES
3. TESTS
4. BUILDS
5. NEW FINDINGS
6. NEXT STEP

Do not repeat the same report twice.
This is important because long reports can consume more context than XiT saved.

中文：高噪音命令必须用 xit auto；短命令、结构化输出、install/version/json/porcelain 命令不要包 xit auto。最终报告默认压缩，不要把省下来的 token 又用长报告烧掉。
`

// UserSkillPath returns the canonical user-scope skill path.
func UserSkillPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".kimi", "skills", SkillName, "SKILL.md"), nil
}

// ProjectSkillPath returns the project-scope skill path relative to cwd.
func ProjectSkillPath() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, ".kimi", "skills", SkillName, "SKILL.md"), nil
}

// ResolveSkillPath returns the skill path for the given scope ("user" or "project").
func ResolveSkillPath(scope string) (string, error) {
	switch scope {
	case "user":
		return UserSkillPath()
	case "project":
		return ProjectSkillPath()
	default:
		return "", fmt.Errorf("unknown scope: %s (use user or project)", scope)
	}
}

type StatusResult struct {
	Scope     string
	SkillPath string
	Installed bool
}

// Status checks whether the XiT skill is installed for the given scope.
func Status(scope string) (*StatusResult, error) {
	path, err := ResolveSkillPath(scope)
	if err != nil {
		return nil, err
	}
	_, statErr := os.Stat(path)
	installed := statErr == nil
	return &StatusResult{
		Scope:     scope,
		SkillPath: path,
		Installed: installed,
	}, nil
}

// Install writes the XiT skill file for the given scope.
// Requires yes == true (caller should have confirmed with --yes).
func Install(scope string) (string, error) {
	path, err := ResolveSkillPath(scope)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("create skill dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(skillContent), 0644); err != nil {
		return "", fmt.Errorf("write skill file: %w", err)
	}
	return path, nil
}

// Uninstall removes the XiT skill file for the given scope.
// Requires yes == true (caller should have confirmed with --yes).
func Uninstall(scope string) (string, error) {
	path, err := ResolveSkillPath(scope)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path, fmt.Errorf("skill not installed at %s", path)
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("remove skill file: %w", err)
	}
	// Remove parent dir if now empty.
	dir := filepath.Dir(path)
	entries, _ := os.ReadDir(dir)
	if len(entries) == 0 {
		os.Remove(dir)
	}
	return path, nil
}

// SkillFileContent returns the SKILL.md content that would be installed.
func SkillFileContent() string {
	return skillContent
}

// RequiredPhrases lists the mandatory phrases that must appear in the skill file.
var RequiredPhrases = []string{
	"XiT Mandatory Routing Contract",
	"Always use xit auto for high-noise commands",
	"Do NOT use xit auto for passthrough commands",
	"Before every Shell command",
	"Compact final report discipline",
	"go test -v ./...",
	"grep -rn",
	"git diff",
	"git status",
	"--json",
	"--porcelain",
	"raw_log",
}

// VerifyResult reports whether the installed skill contains required phrases.
type VerifyResult struct {
	Scope              string
	SkillPath          string
	Installed          bool
	VersionHint        string
	RoutingContract    bool
	PassthroughContract bool
	CompactReport      bool
	Missing            []string
}

// Verify checks whether the installed skill file contains all required phrases.
func Verify(scope string) (*VerifyResult, error) {
	path, err := ResolveSkillPath(scope)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &VerifyResult{
				Scope:       scope,
				SkillPath:   path,
				Installed:   false,
				VersionHint: "v0.2.33",
				Missing:     RequiredPhrases,
			}, nil
		}
		return nil, err
	}
	content := string(data)
	res := &VerifyResult{
		Scope:       scope,
		SkillPath:   path,
		Installed:   true,
		VersionHint: "v0.2.33",
	}
	for _, phrase := range RequiredPhrases {
		if !strings.Contains(content, phrase) {
			res.Missing = append(res.Missing, phrase)
		}
	}
	res.RoutingContract = strings.Contains(content, "XiT Mandatory Routing Contract") &&
		strings.Contains(content, "Always use xit auto for high-noise commands")
	res.PassthroughContract = strings.Contains(content, "Do NOT use xit auto for passthrough commands")
	res.CompactReport = strings.Contains(content, "Compact final report discipline")
	return res, nil
}
