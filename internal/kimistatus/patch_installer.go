package kimistatus

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/stephenywilson/xit/internal/kimihook"
)

const (
	PatchBeginMarker = "# --- XiT bottom toolbar patch begin ---"
	PatchEndMarker   = "# --- XiT bottom toolbar patch end ---"
	HelperBeginMarker = "# --- XiT helper begin ---"
	HelperEndMarker   = "# --- XiT helper end ---"
)

// PatchCheckResult holds the result of checking if a Kimi installation is patchable.
type PatchCheckResult struct {
	KimiPath       string
	KimiVersion    string
	PackageDir     string
	PromptPyPath   string
	Patchable      bool
	Installed      bool
	BackupPath     string
	Reason         string
	TargetVersion  string
}

// LocateKimiPackage finds the installed kimi_cli Python package directory.
func LocateKimiPackage() (string, error) {
	// Allow env override for tests
	if v := os.Getenv("XIT_KIMI_PACKAGE_DIR"); v != "" {
		return v, nil
	}

	// Find kimi binary
	kimiPath, err := exec.LookPath("kimi")
	if err != nil {
		return "", fmt.Errorf("kimi not found in PATH: %w", err)
	}

	// Find Python interpreter from shebang
	python, err := pythonFromShebang(kimiPath)
	if err != nil {
		// Fallback: try python3 directly
		python = "python3"
	}

	// Run one-liner to get package dir
	cmd := exec.Command(python, "-c", "import kimi_cli, inspect, os; print(os.path.dirname(inspect.getfile(kimi_cli)))")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to locate kimi_cli package: %w (output: %s)", err, string(out))
	}
	pkgDir := strings.TrimSpace(string(out))
	if pkgDir == "" {
		return "", fmt.Errorf("kimi_cli package dir is empty")
	}
	return pkgDir, nil
}

func pythonFromShebang(scriptPath string) (string, error) {
	f, err := os.Open(scriptPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return "", fmt.Errorf("empty script")
	}
	line := scanner.Text()
	if !strings.HasPrefix(line, "#!") {
		return "", fmt.Errorf("no shebang")
	}
	parts := strings.Fields(strings.TrimPrefix(line, "#!"))
	if len(parts) == 0 {
		return "", fmt.Errorf("empty shebang")
	}
	return parts[0], nil
}

// DetectKimiVersion runs `kimi --version` and returns the version string.
func DetectKimiVersion() string {
	path, err := exec.LookPath("kimi")
	if err != nil {
		return ""
	}
	out, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// CheckPatchable checks if the Kimi prompt.py can be patched.
func CheckPatchable(pkgDir string) *PatchCheckResult {
	res := &PatchCheckResult{
		KimiPath:     "",
		KimiVersion:  DetectKimiVersion(),
		PackageDir:   pkgDir,
		PromptPyPath: filepath.Join(pkgDir, "ui", "shell", "prompt.py"),
		Patchable:    false,
		Installed:    false,
		Reason:       "",
		TargetVersion: "1.46",
	}

	if path, err := exec.LookPath("kimi"); err == nil {
		res.KimiPath = path
	}

	// Check file exists
	if _, err := os.Stat(res.PromptPyPath); err != nil {
		res.Reason = fmt.Sprintf("prompt.py not found at %s", res.PromptPyPath)
		return res
	}

	content, err := os.ReadFile(res.PromptPyPath)
	if err != nil {
		res.Reason = fmt.Sprintf("cannot read prompt.py: %v", err)
		return res
	}
	contentStr := string(content)

	// Check if already patched
	if strings.Contains(contentStr, PatchBeginMarker) {
		res.Installed = true
		res.Reason = "XiT patch already installed"
		// Still patchable if we want to reinstall after uninstall
		res.Patchable = true
		return res
	}

	// Check for expected function
	if !strings.Contains(contentStr, "def _render_bottom_toolbar(self)") {
		res.Reason = "_render_bottom_toolbar function not found"
		return res
	}

	// Check for right_width calculation (where we insert the patch)
	if !strings.Contains(contentStr, "_display_width(right_text)") {
		res.Reason = "_display_width(right_text) not found"
		return res
	}

	// Version check (only refuse known incompatible; allow if not determinable)
	if res.KimiVersion != "" && !strings.Contains(res.KimiVersion, res.TargetVersion) {
		res.Reason = fmt.Sprintf("Kimi version %s may not be compatible (target: %s.x); use --force to override", res.KimiVersion, res.TargetVersion)
		return res
	}

	res.Patchable = true
	return res
}

// FindBackup returns the most recent XiT backup file for prompt.py.
func FindBackup(promptPath string) string {
	dir := filepath.Dir(promptPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var latest string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), "prompt.py.xit-backup-") {
			candidate := filepath.Join(dir, e.Name())
			if latest == "" || candidate > latest {
				latest = candidate
			}
		}
	}
	return latest
}

// InstallPatch applies the XiT patch to prompt.py.
func InstallPatch(promptPath, xitHome string, force bool) error {
	res := CheckPatchable(filepath.Dir(filepath.Dir(filepath.Dir(promptPath))))
	if !res.Patchable && !force {
		return fmt.Errorf("not patchable: %s", res.Reason)
	}
	if res.Installed {
		return fmt.Errorf("XiT patch already installed; run uninstall first")
	}

	content, err := os.ReadFile(promptPath)
	if err != nil {
		return err
	}
	contentStr := string(content)

	// Preflight validation on temp copy
	if err := ValidatePatch(promptPath, xitHome); err != nil {
		return fmt.Errorf("preflight validation failed: %w", err)
	}

	// Backup
	timestamp := time.Now().Format("20060102-150405")
	backupPath := promptPath + ".xit-backup-" + timestamp
	if err := os.WriteFile(backupPath, content, 0644); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	// Compute SHA256 of original
	h := sha256.New()
	h.Write(content)
	origHash := fmt.Sprintf("%x", h.Sum(nil))

	// Build patched content
	patched, err := applyPatch(contentStr, xitHome)
	if err != nil {
		return fmt.Errorf("patch application failed: %w", err)
	}

	// Write
	if err := os.WriteFile(promptPath, []byte(patched), 0644); err != nil {
		return fmt.Errorf("write failed: %w", err)
	}

	// Verify markers present
	newContent, err := os.ReadFile(promptPath)
	if err != nil {
		return fmt.Errorf("verify read failed: %w", err)
	}
	if !strings.Contains(string(newContent), PatchBeginMarker) {
		// Restore backup
		os.WriteFile(promptPath, content, 0644)
		os.Remove(backupPath)
		return fmt.Errorf("patch verification failed: markers not found")
	}

	_ = origHash
	return nil
}

// ValidatePatch applies the patch to a temp copy and runs python -m py_compile.
func ValidatePatch(promptPath, xitHome string) error {
	content, err := os.ReadFile(promptPath)
	if err != nil {
		return fmt.Errorf("read prompt.py: %w", err)
	}
	patched, err := applyPatch(string(content), xitHome)
	if err != nil {
		return fmt.Errorf("apply patch: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "xit-kimi-patch-validate-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpPath := filepath.Join(tmpDir, "prompt.py")
	if err := os.WriteFile(tmpPath, []byte(patched), 0644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}

	python, _ := pythonFromShebang(promptPath)
	if python == "" {
		python = "python3"
	}
	// If python3 doesn't exist, try python
	if _, err := exec.LookPath(python); err != nil {
		python = "python"
	}
	cmd := exec.Command(python, "-m", "py_compile", tmpPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("py_compile failed: %w\noutput: %s", err, string(out))
	}
	return nil
}

// UninstallPatch restores prompt.py from the latest XiT backup.
func UninstallPatch(promptPath string) error {
	backup := FindBackup(promptPath)
	if backup == "" {
		return fmt.Errorf("no XiT backup found for %s", promptPath)
	}

	content, err := os.ReadFile(backup)
	if err != nil {
		return fmt.Errorf("read backup failed: %w", err)
	}

	if err := os.WriteFile(promptPath, content, 0644); err != nil {
		return fmt.Errorf("restore failed: %w", err)
	}

	// Remove backup after successful restore
	os.Remove(backup)
	return nil
}

// DryRunPatch returns the patch that would be applied, without modifying files.
func DryRunPatch(promptPath, xitHome string) (string, error) {
	content, err := os.ReadFile(promptPath)
	if err != nil {
		return "", err
	}
	patched, err := applyPatch(string(content), xitHome)
	if err != nil {
		return "", err
	}
	return diffPreview(string(content), patched), nil
}

func applyPatch(content, xitHome string) (string, error) {
	// Find "right_width = _display_width(right_text)"
	lines := strings.Split(content, "\n")
	rwIdx := -1
	for i, line := range lines {
		if strings.Contains(line, "_display_width(right_text)") {
			rwIdx = i
			break
		}
	}
	if rwIdx < 0 {
		return "", fmt.Errorf("could not find '_display_width(right_text)'")
	}

	// Find spacer line: fragments.append(("", " " * max(0, columns - left_width - right_width)))
	spacerIdx := -1
	for i := rwIdx + 1; i < len(lines); i++ {
		if strings.Contains(lines[i], `fragments.append(("", " " * max(0, columns - left_width - right_width))`) {
			spacerIdx = i
			break
		}
	}

	// Check if left_width is initialized between right_width and spacer
	hasLeftWidthInit := false
	if spacerIdx > 0 {
		for i := rwIdx + 1; i < spacerIdx; i++ {
			if strings.Contains(lines[i], "left_width") && strings.Contains(lines[i], "=") {
				hasLeftWidthInit = true
				break
			}
		}
	}

	// Determine insertion point and XiT block
	var insertIdx int
	var xitBlock string

	if spacerIdx > rwIdx && hasLeftWidthInit {
		// Real Kimi prompt: insert XiT before spacer, adjust left_width
		fragIndent := ""
		for _, ch := range lines[spacerIdx] {
			if ch == ' ' || ch == '\t' {
				fragIndent += string(ch)
			} else {
				break
			}
		}
		xitBlock = fragIndent + PatchBeginMarker + "\n"
		xitBlock += fragIndent + "if _xit_status_text:\n"
		xitBlock += fragIndent + "    fragments.append((\"fg:#D4A017 bold\", _xit_status_text))\n"
		xitBlock += fragIndent + "    fragments.append((\"\", \"  \"))\n"
		xitBlock += fragIndent + "    left_width += _xit_status_width + 2\n"
		xitBlock += fragIndent + PatchEndMarker
		insertIdx = spacerIdx
	} else {
		// Fallback: insert before first fragments.append after right_width,
		// or before return if no fragments.append exists.
		firstFragIdx := -1
		for i := rwIdx + 1; i < len(lines); i++ {
			if strings.Contains(lines[i], "fragments.append") {
				firstFragIdx = i
				break
			}
		}
		if firstFragIdx < 0 {
			// No fragments.append after right_width; find return statement
			for i := rwIdx + 1; i < len(lines); i++ {
				if strings.Contains(lines[i], "return") {
					firstFragIdx = i
					break
				}
			}
		}
		if firstFragIdx < 0 {
			return "", fmt.Errorf("could not find insertion point after right_width")
		}
		fragIndent := ""
		for _, ch := range lines[firstFragIdx] {
			if ch == ' ' || ch == '\t' {
				fragIndent += string(ch)
			} else {
				break
			}
		}
		xitBlock = fragIndent + PatchBeginMarker + "\n"
		xitBlock += fragIndent + "if _xit_status_text:\n"
		xitBlock += fragIndent + "    fragments.append((\"fg:#D4A017 bold\", _xit_status_text))\n"
		xitBlock += fragIndent + PatchEndMarker
		insertIdx = firstFragIdx
	}

	// Compute indentation of the target line
	targetLine := lines[rwIdx]
	indent := ""
	for _, ch := range targetLine {
		if ch == ' ' || ch == '\t' {
			indent += string(ch)
		} else {
			break
		}
	}

	// Build patch block before right_width (compute XiT status)
	patchBlock := indent + PatchBeginMarker + "\n"
	patchBlock += indent + "_xit_status_text = _xit_bottom_toolbar_status_text()\n"
	patchBlock += indent + "_xit_status_width = _display_width(_xit_status_text) if _xit_status_text else 0\n"
	patchBlock += indent + PatchEndMarker + "\n"

	// Assemble
	newLines := append([]string{}, lines[:rwIdx]...)
	newLines = append(newLines, patchBlock)
	newLines = append(newLines, lines[rwIdx:insertIdx]...)
	newLines = append(newLines, xitBlock)
	newLines = append(newLines, lines[insertIdx:]...)

	// Append helper function at end of file
	helper := helperFunction(xitHome)
	newLines = append(newLines, "")
	newLines = append(newLines, helper)

	return strings.Join(newLines, "\n"), nil
}

func helperFunction(xitHome string) string {
	return HelperBeginMarker + `
_XIT_KIMI_SESSION_STARTED_AT = time.time()

def _xit_format_saved_tokens(saved_bytes):
    saved_tokens = saved_bytes // 4
    if saved_tokens <= 0:
        return ""
    if saved_tokens < 1000:
        return "省" + str(saved_tokens) + " Token"
    return "省" + str(round(saved_tokens / 1000)) + "k Token"

def _xit_bottom_toolbar_status_text():
    try:
        import os
        import json
        import time
        from datetime import datetime, timezone, timedelta

        # 1. Check runtime state (project-first, then user home)
        state = None
        cwd = os.getcwd()
        if cwd:
            project_current = os.path.join(cwd, ".xit", "state", "current.json")
            try:
                with open(project_current, "r", encoding="utf-8") as f:
                    state = json.load(f)
            except Exception:
                pass
        if not state:
            user_current = os.path.expanduser("~/.xit/state/current.json")
            try:
                with open(user_current, "r", encoding="utf-8") as f:
                    state = json.load(f)
            except Exception:
                pass

        now = time.time()
        if state:
            status = state.get("status", "")
            started_at_str = state.get("started_at", "")
            finished_at_str = state.get("finished_at", "")
            started_at = 0
            finished_at = 0
            try:
                started_at = datetime.fromisoformat(started_at_str.replace("Z", "+00:00")).timestamp()
            except Exception:
                pass
            try:
                finished_at = datetime.fromisoformat(finished_at_str.replace("Z", "+00:00")).timestamp()
            except Exception:
                pass

            # Ignore old state from before this Kimi process started
            if started_at and started_at < _XIT_KIMI_SESSION_STARTED_AT:
                state = None
            if state and finished_at and finished_at < _XIT_KIMI_SESSION_STARTED_AT:
                state = None

            if state and status == "running" and started_at and (now - started_at) < 600:
                return "吸T神功 · 正在吸T中"
            if state and status == "completed" and finished_at and (now - finished_at) < 30:
                saved = state.get("saved_bytes", 0)
                if saved:
                    return "吸T完成 · 本次" + _xit_format_saved_tokens(saved)
                return "吸T完成 · 已压缩输出"
            if state and status == "failed" and finished_at and (now - finished_at) < 30:
                return "吸T完成 · 已压缩输出"

        # 2. Check turn state (project-first, then user home)
        turn_state = None
        if cwd:
            project_turn = os.path.join(cwd, ".xit", "state", "turn.json")
            try:
                with open(project_turn, "r", encoding="utf-8") as f:
                    turn_state = json.load(f)
            except Exception:
                pass
        if not turn_state:
            user_turn = os.path.expanduser("~/.xit/state/turn.json")
            try:
                with open(user_turn, "r", encoding="utf-8") as f:
                    turn_state = json.load(f)
            except Exception:
                pass

        # 4. State machine priority
        if turn_state:
            turn_status = turn_state.get("status", "")
            turn_started_at_str = turn_state.get("started_at", "")
            turn_finished_at_str = turn_state.get("finished_at", "")
            turn_started_at = 0
            turn_finished_at = 0
            try:
                turn_started_at = datetime.fromisoformat(turn_started_at_str.replace("Z", "+00:00")).timestamp()
            except Exception:
                pass
            try:
                turn_finished_at = datetime.fromisoformat(turn_finished_at_str.replace("Z", "+00:00")).timestamp()
            except Exception:
                pass

            # Ignore old turn state from before this Kimi process started
            if turn_started_at and turn_started_at < _XIT_KIMI_SESSION_STARTED_AT:
                turn_state = None
            if turn_state and turn_finished_at and turn_finished_at < _XIT_KIMI_SESSION_STARTED_AT:
                turn_state = None

            if turn_state:
                if turn_status == "session_started":
                    return "吸T神功 · 准备就绪"

                if turn_status in ("thinking", "active"):
                    return "吸T神功 · 守护你的T"

                if turn_status == "turn_completed" and turn_finished_at and (now - turn_finished_at) < 60:
                    # Compute turn-scoped stats
                    turn_auto_count = 0
                    turn_saved = 0
                    if turn_started_at:
                        turn_end = turn_finished_at if turn_finished_at else now
                        candidates = []
                        if cwd:
                            candidates.append(os.path.join(cwd, ".xit", "history.jsonl"))
                        candidates.append(os.path.expanduser("~/.xit/history.jsonl"))
                        candidates.append(os.path.expanduser("~/.xit/kimi-hooks/events.jsonl"))
                        for path in candidates:
                            if not os.path.exists(path):
                                continue
                            with open(path, "r", encoding="utf-8") as f:
                                for line in f:
                                    line = line.strip()
                                    if not line:
                                        continue
                                    try:
                                        rec = json.loads(line)
                                    except Exception:
                                        continue
                                    ts_str = rec.get("timestamp", "")
                                    try:
                                        ts = datetime.fromisoformat(ts_str.replace("Z", "+00:00"))
                                        rec_ts = ts.timestamp()
                                        if rec_ts < turn_started_at or rec_ts > turn_end:
                                            continue
                                    except Exception:
                                        continue
                                    rawb = rec.get("raw_bytes", 0) or 0
                                    summaryb = rec.get("summary_bytes", 0) or 0
                                    action = rec.get("action", "")
                                    is_auto = False
                                    if rawb and summaryb and rawb > summaryb:
                                        is_auto = True
                                    if action == "reroute":
                                        is_auto = True
                                    if is_auto:
                                        turn_auto_count += 1
                                    if rawb and summaryb and rawb > summaryb:
                                        turn_saved += rawb - summaryb

                    # Completed-only rotation (5s cycle per candidate)
                    if turn_auto_count > 0:
                        text = "本次吸T" + str(turn_auto_count) + "次 · " + _xit_format_saved_tokens(turn_saved)
                        dw = 0
                        for ch in text:
                            if ord(ch) > 127:
                                dw += 2
                            else:
                                dw += 1
                        if dw > 32:
                            short = "本次吸T" + str(turn_auto_count) + "次"
                            dw = 0
                            for ch in short:
                                if ord(ch) > 127:
                                    dw += 2
                                else:
                                    dw += 1
                            if dw <= 32:
                                text = short
                            else:
                                text = "吸T完成 · raw_log 已留证"
                        candidates_text = [
                            text,
                            "吸T完成 · raw_log 已留证",
                            "吸T神功 · 等待下轮发功",
                        ]
                        idx = int((time.time() // 5) % len(candidates_text))
                        return candidates_text[idx]
                    else:
                        candidates_text = [
                            "吸T神功 · 本次已守护",
                            "吸T神功 · 等待下轮发功",
                        ]
                        idx = int((time.time() // 5) % len(candidates_text))
                        return candidates_text[idx]

        # 5. In turn-scoped mode, do not show session aggregate in toolbar.
        # Fall through to ready.

        # 6. ready
        return "吸T神功 · 准备就绪"
    except Exception:
        return "吸T神功 · 准备就绪"
` + HelperEndMarker
}

// UpdateCheckResult holds the result of checking whether the patch is still valid.
type UpdateCheckResult struct {
	Version          string
	PromptHash       string
	PatchMarker      string
	BackupExists     bool
	PlacementValid   bool
	Action           string
}

// CheckUpdate performs a read-only check of patch health against current Kimi.
func CheckUpdate(pkgDir string) *UpdateCheckResult {
	res := &UpdateCheckResult{
		Version:     DetectKimiVersion(),
		PromptHash:  "",
		PatchMarker: "absent",
		BackupExists: false,
		PlacementValid: false,
		Action:      "none",
	}

	promptPath := filepath.Join(pkgDir, "ui", "shell", "prompt.py")
	if _, err := os.Stat(promptPath); err != nil {
		res.Action = "uninstall recommended (prompt.py missing)"
		return res
	}

	content, err := os.ReadFile(promptPath)
	if err != nil {
		res.Action = "uninstall recommended (cannot read prompt.py)"
		return res
	}

	h := sha256.New()
	h.Write(content)
	res.PromptHash = fmt.Sprintf("%.16s", fmt.Sprintf("%x", h.Sum(nil)))

	contentStr := string(content)
	if strings.Contains(contentStr, PatchBeginMarker) {
		res.PatchMarker = "present"
	}

	backup := FindBackup(promptPath)
	res.BackupExists = backup != ""

	res.PlacementValid = strings.Contains(contentStr, "def _render_bottom_toolbar(self)") &&
		strings.Contains(contentStr, "_display_width(right_text)")

	switch {
	case res.PatchMarker == "present" && !res.PlacementValid:
		res.Action = "reinstall recommended (Kimi structure changed)"
	case res.PatchMarker == "present" && !res.BackupExists:
		res.Action = "uninstall recommended (backup missing)"
	case res.PatchMarker == "absent" && res.BackupExists:
		res.Action = "none (patch not installed, backup present)"
	case res.PatchMarker == "present":
		res.Action = "none"
	default:
		res.Action = "none"
	}

	return res
}

func diffPreview(original, patched string) string {
	origLines := strings.Split(original, "\n")
	patchedLines := strings.Split(patched, "\n")

	var added []string
	for _, line := range patchedLines {
		found := false
		for _, oline := range origLines {
			if oline == line {
				found = true
				break
			}
		}
		if !found {
			added = append(added, "+ "+line)
		}
	}
	return strings.Join(added, "\n")
}

// ToolbarPreview holds what the toolbar would display.
type ToolbarPreview struct {
	Preview               string
	Rotation              []string
	Language              string
	RotationEnabled       bool
	Mode                  string
	Style                 string
	PowerState            string
	Position              string
	HistoryInToolbar      bool
	RawLogInIdle          bool
	ReadyText             string
	GuardingText          string
	AbsorbingText         string
	CompletedText         string
	TurnResultWithAuto    []string
	TurnResultWithoutAuto []string
	RotationScope         string
	HistoryInReady        bool
	RawLogInReady         bool
	EnglishInToolbar      bool
	ONOFF                 bool
	Unit                  string
	TokenMethod           string
	ToolbarExample        string
	RotationInterval      string
	ToolbarScope          string
	AbsorbingProgressText string
}

// ComputeToolbarPreview computes the toolbar preview from XiT history.
// Mirrors the Python helper state machine using a local time-window approximation
// for session stats (the real toolbar uses Kimi process start time).
func ComputeToolbarPreview(xitHome string) *ToolbarPreview {
	// 1. Check runtime state
	statePath := filepath.Join(xitHome, "state", "current.json")
	now := time.Now()
	var state struct {
		Status     string `json:"status"`
		StartedAt  string `json:"started_at"`
		FinishedAt string `json:"finished_at"`
		SavedBytes int    `json:"saved_bytes"`
	}
	if data, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(data, &state)
	}

	base := ToolbarPreview{
		Language:              "zh_only",
		Mode:                  "turn_scoped_visual_state_machine",
		Style:                 "fg:#D4A017 bold",
		PowerState:            "",
		Position:              "second_line_left",
		RotationEnabled:       false,
		HistoryInToolbar:      false,
		RawLogInIdle:          false,
		ReadyText:             "吸T神功 · 准备就绪",
		GuardingText:          "吸T神功 · 守护你的T",
		AbsorbingText:         "吸T神功 · 正在吸T中",
		AbsorbingProgressText: "吸T神功 · 已接管12k Token",
		CompletedText:         "吸T完成 · 本次省9k Token",
		TurnResultWithAuto:    []string{"本次吸T1次 · 省9k Token", "吸T完成 · raw_log 已留证", "吸T神功 · 等待下轮发功"},
		Unit:                  "token",
		TokenMethod:           "saved_bytes / 4",
		ToolbarExample:        "本次吸T1次 · 省9k Token",
		TurnResultWithoutAuto: []string{"吸T神功 · 本次已守护", "吸T神功 · 等待下轮发功"},
		RotationScope:         "completed_only",
		HistoryInReady:        false,
		RawLogInReady:         false,
		EnglishInToolbar:      false,
		ONOFF:                 false,
		RotationInterval:      "5s",
		ToolbarScope:          "current_turn_first",
	}

	if state.Status == "running" && state.StartedAt != "" {
		if t, err := time.Parse(time.RFC3339, state.StartedAt); err == nil && now.Sub(t) < 10*time.Minute {
			base.Preview = "吸T神功 · 正在吸T中"
			return &base
		}
	}
	if state.Status == "completed" && state.FinishedAt != "" {
		if t, err := time.Parse(time.RFC3339, state.FinishedAt); err == nil && now.Sub(t) < 30*time.Second {
			tokenStr := kimihook.FormatSavedTokens(state.SavedBytes)
			if tokenStr != "" {
				base.Preview = "吸T完成 · 本次" + tokenStr
			} else {
				base.Preview = "吸T完成 · 已压缩输出"
			}
			return &base
		}
	}
	if state.Status == "failed" && state.FinishedAt != "" {
		if t, err := time.Parse(time.RFC3339, state.FinishedAt); err == nil && now.Sub(t) < 30*time.Second {
			base.Preview = "吸T完成 · 已压缩输出"
			return &base
		}
	}

	// 2. Check turn state (project-first, then user home)
	var turn struct {
		Status     string `json:"status"`
		StartedAt  string `json:"started_at"`
		FinishedAt string `json:"finished_at"`
	}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		projectTurnPath := filepath.Join(cwd, ".xit", "state", "turn.json")
		if data, err := os.ReadFile(projectTurnPath); err == nil {
			_ = json.Unmarshal(data, &turn)
		}
	}
	if turn.Status == "" {
		userTurnPath := filepath.Join(xitHome, "state", "turn.json")
		if data, err := os.ReadFile(userTurnPath); err == nil {
			_ = json.Unmarshal(data, &turn)
		}
	}

	// Compute turn-scoped stats for lower-priority states
	turnAutoCount, turnSaved := computeTurnStatsForPreview(xitHome, turn)

	if turn.Status == "session_started" {
		base.Preview = "吸T神功 · 准备就绪"
		return &base
	}

	if turn.Status == "thinking" || turn.Status == "active" {
		base.Preview = "吸T神功 · 守护你的T"
		return &base
	}

	// 4. turn_completed_result
	if turn.Status == "turn_completed" && turn.FinishedAt != "" {
		if t, err := time.Parse(time.RFC3339, turn.FinishedAt); err == nil && now.Sub(t) < 60*time.Second {
			if turnAutoCount > 0 {
				tokenStr := kimihook.FormatSavedTokens(turnSaved)
				base.Preview = fmt.Sprintf("本次吸T%d次 · %s", turnAutoCount, tokenStr)
				base.RotationEnabled = true
			} else {
				base.Preview = "吸T神功 · 本次已守护"
				base.RotationEnabled = true
			}
			return &base
		}
	}

	// 5. session_result — in turn-scoped mode, do not show session aggregate in toolbar.
	// Fall through to ready.
	_ = turnSaved

	// 6. ready
	base.Preview = "吸T神功 · 准备就绪"
	return &base
}

func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if r > 127 {
			w += 2
		} else {
			w++
		}
	}
	return w
}

func computeXiTStats(xitHome string) (observed, autoCount, saved int) {
	observed, autoCount, saved, _ = computeXiTStatsWithSession(xitHome, 0)
	return
}

func computeXiTStatsWithSession(xitHome string, windowSeconds int) (observed, autoCount, saved int, sessionSaved int) {
	candidates := []string{}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		candidates = append(candidates, filepath.Join(cwd, ".xit", "history.jsonl"))
	}
	candidates = append(candidates, filepath.Join(xitHome, "history.jsonl"))
	candidates = append(candidates, filepath.Join(xitHome, "kimi-hooks", "events.jsonl"))

	if windowSeconds <= 0 {
		windowSeconds = 7200 // 2 hours
	}
	cutoff := time.Now().Add(-time.Duration(windowSeconds) * time.Second)

	for _, path := range candidates {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var rec struct {
				Timestamp    string `json:"timestamp"`
				RawBytes     int    `json:"raw_bytes"`
				SummaryBytes int    `json:"summary_bytes"`
				Action       string `json:"action"`
			}
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				continue
			}
			observed++
			if rec.RawBytes > 0 && rec.SummaryBytes > 0 && rec.RawBytes > rec.SummaryBytes {
				autoCount++
				saved += rec.RawBytes - rec.SummaryBytes
			}
			if rec.Action == "reroute" {
				autoCount++
			}

			ts, _ := time.Parse(time.RFC3339, rec.Timestamp)
			if !ts.IsZero() && ts.After(cutoff) && rec.RawBytes > rec.SummaryBytes {
				sessionSaved += rec.RawBytes - rec.SummaryBytes
			}
		}
		f.Close()
	}
	return
}

// computeTurnStatsForPreview returns autoCount and savedBytes for records within the turn time range.
func computeTurnStatsForPreview(xitHome string, turn struct {
	Status     string `json:"status"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
}) (autoCount, savedBytes int) {
	if turn.StartedAt == "" {
		return 0, 0
	}
	turnStart, err := time.Parse(time.RFC3339, turn.StartedAt)
	if err != nil {
		return 0, 0
	}
	turnEnd := time.Now()
	if turn.FinishedAt != "" {
		if t, err := time.Parse(time.RFC3339, turn.FinishedAt); err == nil {
			turnEnd = t
		}
	}

	candidates := []string{}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		candidates = append(candidates, filepath.Join(cwd, ".xit", "history.jsonl"))
	}
	candidates = append(candidates, filepath.Join(xitHome, "history.jsonl"))
	candidates = append(candidates, filepath.Join(xitHome, "kimi-hooks", "events.jsonl"))

	for _, path := range candidates {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var rec struct {
				Timestamp    string `json:"timestamp"`
				RawBytes     int    `json:"raw_bytes"`
				SummaryBytes int    `json:"summary_bytes"`
				Action       string `json:"action"`
			}
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				continue
			}
			ts, _ := time.Parse(time.RFC3339, rec.Timestamp)
			if ts.IsZero() {
				continue
			}
			if ts.Before(turnStart) || ts.After(turnEnd) {
				continue
			}
			if rec.RawBytes > 0 && rec.SummaryBytes > 0 && rec.RawBytes > rec.SummaryBytes {
				autoCount++
				savedBytes += rec.RawBytes - rec.SummaryBytes
			}
			if rec.Action == "reroute" {
				autoCount++
			}
		}
		f.Close()
	}
	return
}
