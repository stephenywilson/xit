package kimistatus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunAuditProducesResult(t *testing.T) {
	r := RunAudit()
	if !r.BottomToolbarDetected {
		t.Error("expected bottom toolbar detected")
	}
	if r.RendererMethod == "" {
		t.Error("expected renderer method")
	}
	if len(r.InjectionPaths) == 0 {
		t.Error("expected injection paths")
	}
}

func TestFormatAuditReportContainsConclusion(t *testing.T) {
	r := RunAudit()
	out := FormatAuditReport(r)
	if !strings.Contains(out, "Conclusion") {
		t.Error("expected conclusion section")
	}
	if !strings.Contains(out, "prompt_toolkit") {
		t.Error("expected prompt_toolkit mention")
	}
}

func TestPatchScriptContainsXiTHome(t *testing.T) {
	script := PatchScript("/home/user/.xit")
	if !strings.Contains(script, "/home/user/.xit") {
		t.Error("expected XIT_HOME in patch script")
	}
	if !strings.Contains(script, "EXPERIMENTAL") {
		t.Error("expected EXPERIMENTAL warning")
	}
	if !strings.Contains(script, "sitecustomize") {
		t.Error("expected sitecustomize mention")
	}
}

func TestTerminalTitleSupported(t *testing.T) {
	// This is environment-dependent; just ensure it doesn't panic.
	_ = TerminalTitleSupported()
}

func TestSetTerminalTitle(t *testing.T) {
	title := SetTerminalTitle("XiT active")
	if !strings.Contains(title, "XiT active") {
		t.Error("expected title in OSC sequence")
	}
	if !strings.HasPrefix(title, "\033]0;") {
		t.Error("expected OSC 0 prefix")
	}
}

func TestTitleFromStatus(t *testing.T) {
	if got := TitleFromStatus(0, 0, 0); got != "XiT" {
		t.Errorf("got %q, want XiT", got)
	}
	if got := TitleFromStatus(5, 2, 100); got != "XiT observed 5 auto 2 saved ~100" {
		t.Errorf("got %q", got)
	}
}

func TestStatusTextFromStats(t *testing.T) {
	if got := StatusTextFromStats(0, 0); got != "active" {
		t.Errorf("got %q, want active", got)
	}
	if got := StatusTextFromStats(3, 0); got != "obs 3" {
		t.Errorf("got %q, want obs 3", got)
	}
	if got := StatusTextFromStats(3, 1); got != "obs 3 | auto 1" {
		t.Errorf("got %q, want obs 3 | auto 1", got)
	}
}

func TestDefaultPatchPath(t *testing.T) {
	p := DefaultPatchPath("/home/user/.xit")
	if !strings.HasSuffix(p, "status_bar_patch.py") {
		t.Errorf("unexpected path: %s", p)
	}
}

func fakePromptPy() string {
	return `class ShellPrompt:
    def _render_bottom_toolbar(self):
        fragments = []
        fragments.append(("", "\n"))
        right_text = self._render_right_span(status)
        right_width = _display_width(right_text)

        left_toast = _current_toast("left")
        if left_toast is not None:
            max_left = max(0, columns - right_width - 2)
            if max_left > 0:
                left_text = left_toast.message
                if _display_width(left_text) > max_left:
                    left_text = _truncate_right(left_text, max_left)
                left_width = _display_width(left_text)
                fragments.append(("", left_text))
            else:
                left_width = 0
        else:
            left_width = 0

        fragments.append(("", " " * max(0, columns - left_width - right_width)))
        fragments.append(("", right_text))
        return FormattedText(fragments)
`
}

func TestCheckPatchableWithFakePrompt(t *testing.T) {
	tmpDir := t.TempDir()
	promptDir := filepath.Join(tmpDir, "ui", "shell")
	os.MkdirAll(promptDir, 0755)
	promptPath := filepath.Join(promptDir, "prompt.py")
	os.WriteFile(promptPath, []byte(fakePromptPy()), 0644)

	res := CheckPatchable(tmpDir)
	if !res.Patchable {
		t.Errorf("expected patchable, got: %s", res.Reason)
	}
	if res.Installed {
		t.Error("expected not installed")
	}
	if res.PromptPyPath != promptPath {
		t.Errorf("expected prompt path %s, got %s", promptPath, res.PromptPyPath)
	}
}

func TestCheckPatchableMissingFunction(t *testing.T) {
	tmpDir := t.TempDir()
	promptDir := filepath.Join(tmpDir, "ui", "shell")
	os.MkdirAll(promptDir, 0755)
	promptPath := filepath.Join(promptDir, "prompt.py")
	os.WriteFile(promptPath, []byte("class ShellPrompt:\n    pass\n"), 0644)

	res := CheckPatchable(tmpDir)
	if res.Patchable {
		t.Error("expected not patchable")
	}
	if !strings.Contains(res.Reason, "_render_bottom_toolbar") {
		t.Errorf("expected missing function reason, got: %s", res.Reason)
	}
}

func TestCheckPatchableAlreadyInstalled(t *testing.T) {
	tmpDir := t.TempDir()
	promptDir := filepath.Join(tmpDir, "ui", "shell")
	os.MkdirAll(promptDir, 0755)
	promptPath := filepath.Join(promptDir, "prompt.py")
	content := fakePromptPy()
	content += "\n" + PatchBeginMarker + "\n"
	os.WriteFile(promptPath, []byte(content), 0644)

	res := CheckPatchable(tmpDir)
	if !res.Installed {
		t.Error("expected installed")
	}
	if !res.Patchable {
		// Should still be patchable (for reinstall after uninstall)
		t.Errorf("expected patchable for reinstall, got: %s", res.Reason)
	}
}

func TestDryRunPatch(t *testing.T) {
	tmpDir := t.TempDir()
	promptDir := filepath.Join(tmpDir, "ui", "shell")
	os.MkdirAll(promptDir, 0755)
	promptPath := filepath.Join(promptDir, "prompt.py")
	os.WriteFile(promptPath, []byte(fakePromptPy()), 0644)

	diff, err := DryRunPatch(promptPath, "/tmp/xit")
	if err != nil {
		t.Fatalf("dry run failed: %v", err)
	}
	if !strings.Contains(diff, "XiT") {
		t.Errorf("expected XiT in diff, got:\n%s", diff)
	}
	if !strings.Contains(diff, "_xit_bottom_toolbar_status_text()") {
		t.Errorf("expected _xit_bottom_toolbar_status_text in diff, got:\n%s", diff)
	}
	if !strings.Contains(diff, "_xit_status_width") {
		t.Errorf("expected _xit_status_width in diff, got:\n%s", diff)
	}
	if !strings.Contains(diff, "fg:#D4A017 bold") {
		t.Errorf("expected styled fragment fg:#D4A017 bold in diff, got:\n%s", diff)
	}
	if strings.Contains(diff, `right_text = right_text + "  " + _xit_status_text`) {
		t.Error("diff should not contain old right_text concat pattern")
	}
	if strings.Contains(diff, "fragments.append(_xit_status_frag)") {
		t.Error("diff should not contain old fragment append pattern")
	}
	// New patch must NOT modify right_width and must insert left fragment BEFORE first fragments.append
	if strings.Contains(diff, "_display_width(right_text) + (2 + _xit_status_width") {
		t.Error("diff should not modify right_width to include XiT width")
	}
	// Note: new patch uses fragments.append(("", "  ")) as separator between XiT and left_toast,
	// which is different from the old right_text concatenation pattern.
	// Verify file unchanged
	content, _ := os.ReadFile(promptPath)
	if strings.Contains(string(content), PatchBeginMarker) {
		t.Error("file was modified during dry-run")
	}
}

func TestInstallAndUninstallPatch(t *testing.T) {
	tmpDir := t.TempDir()
	promptDir := filepath.Join(tmpDir, "ui", "shell")
	os.MkdirAll(promptDir, 0755)
	promptPath := filepath.Join(promptDir, "prompt.py")
	original := fakePromptPy()
	os.WriteFile(promptPath, []byte(original), 0644)

	xitHome := t.TempDir()
	if err := InstallPatch(promptPath, xitHome, false); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Verify backup exists
	backup := FindBackup(promptPath)
	if backup == "" {
		t.Fatal("expected backup file")
	}

	// Verify markers present
	content, _ := os.ReadFile(promptPath)
	if !strings.Contains(string(content), PatchBeginMarker) {
		t.Error("expected patch begin marker")
	}
	if !strings.Contains(string(content), HelperBeginMarker) {
		t.Error("expected helper begin marker")
	}

	// Uninstall
	if err := UninstallPatch(promptPath); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}

	// Verify restored
	restored, _ := os.ReadFile(promptPath)
	if string(restored) != original {
		t.Errorf("file not restored. got:\n%s\nwant:\n%s", string(restored), original)
	}

	// Verify backup removed
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Error("expected backup removed after uninstall")
	}
}

func TestInstallRefusesIfAlreadyInstalled(t *testing.T) {
	tmpDir := t.TempDir()
	promptDir := filepath.Join(tmpDir, "ui", "shell")
	os.MkdirAll(promptDir, 0755)
	promptPath := filepath.Join(promptDir, "prompt.py")
	os.WriteFile(promptPath, []byte(fakePromptPy()), 0644)

	xitHome := t.TempDir()
	if err := InstallPatch(promptPath, xitHome, false); err != nil {
		t.Fatalf("first install failed: %v", err)
	}
	if err := InstallPatch(promptPath, xitHome, false); err == nil {
		t.Fatal("expected second install to fail")
	}
}

func TestHelperFunctionFailOpen(t *testing.T) {
	// The helper function string should contain try/except to fail open
	h := helperFunction("/tmp/xit")
	if !strings.Contains(h, "except Exception") {
		t.Error("expected fail-open except block in helper")
	}
	if !strings.Contains(h, "吸T神功 · Kimi · 准备就绪") {
		t.Error("expected fallback '吸T神功 · Kimi · 准备就绪' in helper")
	}
}

func TestHelperContainsChinese(t *testing.T) {
	h := helperFunction("/tmp/xit")
	if !strings.Contains(h, "吸T神功 · Kimi · 准备就绪") {
		t.Error("expected '吸T神功 · Kimi · 准备就绪' in helper")
	}
	if !strings.Contains(h, "吸T神功 · Kimi · 正在吸T中") {
		t.Error("expected running state '吸T神功 · Kimi · 正在吸T中' in helper")
	}
	if !strings.Contains(h, "省") {
		t.Error("expected '省' saved prefix in helper")
	}
	if strings.Contains(h, "XiT ON · session saved") {
		t.Error("helper should not contain old English-mixed candidate 'XiT ON · session saved'")
	}
}

func TestHelperNoRotation(t *testing.T) {
	h := helperFunction("/tmp/xit")
	lines := strings.Split(h, "\n")

	// Find the first occurrence of candidates_text (rotation block)
	firstCandidatesIdx := -1
	for i, line := range lines {
		if strings.Contains(line, "candidates_text = [") {
			firstCandidatesIdx = i
			break
		}
	}
	if firstCandidatesIdx < 0 {
		t.Fatal("expected candidates_text in helper")
	}

	// ready/guarding/absorbing state-machine returns must appear BEFORE any candidates_text block.
	// The fallback "return ready" at the end of the function (after rotation block) is expected.
	for i, line := range lines {
		if strings.Contains(line, "return \"吸T神功 · Kimi · 守护你的T\"") || strings.Contains(line, "return \"吸T神功 · Kimi · 正在吸T中\"") {
			if i > firstCandidatesIdx {
				t.Errorf("line %d: guarding/absorbing return should appear before rotation block (line %d)", i, firstCandidatesIdx)
			}
		}
		// session_started ready return must appear before rotation
		if strings.Contains(line, `turn_status == "session_started"`) {
			for j := i + 1; j < len(lines) && j <= i+3; j++ {
				if strings.Contains(lines[j], "return \"吸T神功 · Kimi · 准备就绪\"") {
					if j > firstCandidatesIdx {
						t.Errorf("line %d: session_started ready return should appear before rotation block", j)
					}
					break
				}
			}
		}
	}

	if strings.Contains(h, "int(time.time()) // 15") {
		t.Error("helper should not contain old 15-second rotation timing logic")
	}
	if strings.Contains(h, "历史吸T") {
		t.Error("helper should not contain lifetime history in default toolbar")
	}
	if strings.Contains(h, "XiT ON · session saved") {
		t.Error("helper should not contain old English-mixed candidate 'XiT ON · session saved'")
	}
}

func TestHelperRotationIntervalFiveSeconds(t *testing.T) {
	h := helperFunction("/tmp/xit")
	if !strings.Contains(h, "time.time() // 5") {
		t.Error("expected completed/result rotation interval to be 5 seconds (time.time() // 5)")
	}
	if strings.Contains(h, "time.time() // 10") {
		t.Error("helper should not contain old 10-second rotation interval")
	}
}

func TestHelperLengthGuard(t *testing.T) {
	h := helperFunction("/tmp/xit")
	if !strings.Contains(h, "ord(ch) > 127") {
		t.Error("expected CJK width heuristic ord(ch) > 127")
	}
	// Turn result still has length guard for display width
	if !strings.Contains(h, "dw <= 32") {
		t.Error("expected display width guard dw <= 32 in turn result")
	}
}

func TestComputeToolbarPreview(t *testing.T) {
	preview := ComputeToolbarPreview("/nonexistent")
	if preview.Language != "zh_only" {
		t.Errorf("expected language zh_only, got %s", preview.Language)
	}
	if preview.RotationEnabled {
		t.Error("expected rotation disabled for ready state")
	}
	if preview.Mode != "turn_scoped_visual_state_machine" {
		t.Errorf("expected mode turn_scoped_visual_state_machine, got %s", preview.Mode)
	}
	if preview.Style != "fg:#D4A017 bold" {
		t.Errorf("expected style fg:#D4A017 bold, got %s", preview.Style)
	}
	if preview.Position != "second_line_left" {
		t.Errorf("expected position second_line_left, got %s", preview.Position)
	}
	if preview.HistoryInToolbar {
		t.Error("expected history_in_toolbar false")
	}
	if preview.RawLogInIdle {
		t.Error("expected raw_log_in_idle false")
	}
	if preview.ReadyText != "吸T神功 · Kimi · 准备就绪" {
		t.Errorf("expected ready_text 吸T神功 · Kimi · 准备就绪, got %s", preview.ReadyText)
	}
	if preview.GuardingText != "吸T神功 · Kimi · 守护你的T" {
		t.Errorf("expected guarding_text 吸T神功 · Kimi · 守护你的T, got %s", preview.GuardingText)
	}
	if preview.AbsorbingText != "吸T神功 · Kimi · 正在吸T中" {
		t.Errorf("expected absorbing_text 吸T神功 · Kimi · 正在吸T中, got %s", preview.AbsorbingText)
	}
	if preview.RotationScope != "completed_only" {
		t.Errorf("expected rotation_scope completed_only, got %s", preview.RotationScope)
	}
	if preview.HistoryInReady {
		t.Error("expected history_in_ready false")
	}
	if preview.RawLogInReady {
		t.Error("expected raw_log_in_ready false")
	}
	if preview.EnglishInToolbar {
		t.Error("expected english_in_toolbar false")
	}
	if preview.ONOFF {
		t.Error("expected ON_OFF false")
	}
	// Without history, default should be ready state
	if preview.Preview != "吸T神功 · Kimi · 准备就绪" {
		t.Errorf("expected default preview '吸T神功 · Kimi · 准备就绪', got %s", preview.Preview)
	}
	// ready state must not contain ON
	if strings.Contains(preview.Preview, "ON") {
		t.Error("ready preview should not contain ON")
	}
	// Default toolbar must not contain lifetime history
	for i, c := range preview.Rotation {
		if strings.Contains(c, "历史吸T") {
			t.Errorf("rotation %d should not contain history: %s", i, c)
		}
	}
}

func TestHelperCorrectJSONLLogic(t *testing.T) {
	h := helperFunction("/tmp/xit")
	// Must skip empty lines before parsing
	if !strings.Contains(h, "if not line:") {
		t.Error("expected 'if not line:' guard in helper")
	}
	if !strings.Contains(h, "continue") {
		t.Error("expected 'continue' after empty-line guard")
	}
	// Must NOT have the old buggy pattern: "if not line:" immediately followed by json.loads
	lines := strings.Split(h, "\n")
	for i, line := range lines {
		if strings.Contains(line, "json.loads(line)") {
			// Walk back up to 3 lines to find a guard; we expect "continue" between empty-line check and json.loads
			foundGuard := false
			for j := 1; j <= 3 && i-j >= 0; j++ {
				if strings.Contains(lines[i-j], "continue") {
					foundGuard = true
					break
				}
			}
			if !foundGuard {
				t.Errorf("json.loads at line %d without continue guard nearby", i)
			}
		}
	}
}

func TestHelperUsesExpandUser(t *testing.T) {
	h := helperFunction("/tmp/xit")
	if !strings.Contains(h, `os.path.expanduser("~/.xit`) {
		t.Error("expected expanduser with ~/.xit in helper")
	}
	if strings.Contains(h, "/tmp/xit") {
		// Should not hard-code the xitHome passed at generation time
		t.Error("helper should not hardcode generator-time xitHome path")
	}
}

func TestHelperIncludesCWD(t *testing.T) {
	h := helperFunction("/tmp/xit")
	if !strings.Contains(h, `os.path.join(cwd, ".xit", "history.jsonl")`) {
		t.Error("expected cwd .xit/history.jsonl candidate in helper")
	}
}

func TestHelperContainsSessionStartedAt(t *testing.T) {
	h := helperFunction("/tmp/xit")
	if !strings.Contains(h, "_XIT_KIMI_SESSION_STARTED_AT = time.time()") {
		t.Error("expected _XIT_KIMI_SESSION_STARTED_AT in helper")
	}
}

func TestHelperContainsStyledFragment(t *testing.T) {
	// The styled fragment is injected by applyPatch, not the helper itself.
	patched, err := applyPatch(fakePromptPy(), "/tmp/xit")
	if err != nil {
		t.Fatalf("applyPatch failed: %v", err)
	}
	if !strings.Contains(patched, "fg:#D4A017 bold") {
		t.Error("expected styled fragment fg:#D4A017 bold in patched output")
	}
}

func TestHelperWidthCalculation(t *testing.T) {
	// Verify patch block computes XiT width but does NOT add it to right_width.
	p := PatchBeginMarker + `
_xit_status_text = _xit_bottom_toolbar_status_text()
_xit_status_width = _display_width(_xit_status_text) if _xit_status_text else 0
` + PatchEndMarker
	if !strings.Contains(p, "_xit_status_width") {
		t.Error("expected _xit_status_width in patch block")
	}
	// The patched prompt.py should keep right_width unchanged (no + _xit_status_width)
	patched, err := applyPatch(fakePromptPy(), "/tmp/xit")
	if err != nil {
		t.Fatalf("applyPatch failed: %v", err)
	}
	if strings.Contains(patched, "_display_width(right_text) + (2 + _xit_status_width") {
		t.Error("patched output should not add XiT width to right_width")
	}
}

func TestValidatePatchTempOnly(t *testing.T) {
	tmpDir := t.TempDir()
	promptDir := filepath.Join(tmpDir, "ui", "shell")
	os.MkdirAll(promptDir, 0755)
	promptPath := filepath.Join(promptDir, "prompt.py")
	original := fakePromptPy()
	os.WriteFile(promptPath, []byte(original), 0644)

	xitHome := t.TempDir()
	if err := ValidatePatch(promptPath, xitHome); err != nil {
		t.Fatalf("validate failed: %v", err)
	}

	// Real file must be unchanged
	content, _ := os.ReadFile(promptPath)
	if string(content) != original {
		t.Error("real prompt.py was modified during validate")
	}
}

func TestHelperIgnoresOldState(t *testing.T) {
	h := helperFunction("/tmp/xit")
	if !strings.Contains(h, "started_at < _XIT_KIMI_SESSION_STARTED_AT") {
		t.Error("expected old started_at filter in helper")
	}
	if !strings.Contains(h, "finished_at < _XIT_KIMI_SESSION_STARTED_AT") {
		t.Error("expected old finished_at filter in helper")
	}
}

func TestHelperNoEnglishMixed(t *testing.T) {
	h := helperFunction("/tmp/xit")
	if strings.Contains(h, "XiT ON · session saved") {
		t.Error("helper should not contain English-mixed idle text 'XiT ON · session saved'")
	}
}

func TestPatchLeftFragmentPlacement(t *testing.T) {
	patched, err := applyPatch(fakePromptPy(), "/tmp/xit")
	if err != nil {
		t.Fatalf("applyPatch failed: %v", err)
	}
	// XiT fragment should be inserted BEFORE the first fragments.append
	lines := strings.Split(patched, "\n")
	var xitFragIdx, rightFragIdx int
	for i, line := range lines {
		if strings.Contains(line, `fragments.append(("fg:#D4A017 bold", _xit_status_text))`) {
			xitFragIdx = i
		}
		if strings.Contains(line, `fragments.append(("", right_text))`) {
			rightFragIdx = i
		}
	}
	if xitFragIdx == 0 {
		t.Error("expected XiT left fragment in patched output")
	}
	if rightFragIdx == 0 {
		t.Fatal("expected right_text fragment append in patched output")
	}
	if xitFragIdx > rightFragIdx {
		t.Error("XiT fragment should be placed BEFORE right_text fragment")
	}
}

func TestPatchPreservesLeftToast(t *testing.T) {
	prompt := `class ShellPrompt:
    def _render_bottom_toolbar(self):
        fragments = []
        left_toast = self._get_left_toast()
        if left_toast:
            fragments.append(("", left_toast))
        right_text = self._render_right_span(status)
        right_width = _display_width(right_text)
        fragments.append(("", " " * max(0, columns - left_width - right_width)))
        fragments.append(("", right_text))
        return FormattedText(fragments)
`
	patched, err := applyPatch(prompt, "/tmp/xit")
	if err != nil {
		t.Fatalf("applyPatch failed: %v", err)
	}
	// XiT fragment should be before left_toast (first fragments.append)
	if !strings.Contains(patched, `fragments.append(("fg:#D4A017 bold", _xit_status_text))`) {
		t.Error("expected XiT styled fragment in patched output")
	}
	// Original left_toast fragment append should still exist
	if !strings.Contains(patched, `fragments.append(("", left_toast))`) {
		t.Error("expected original left_toast append to be preserved")
	}
	// right_text should still be appended
	if !strings.Contains(patched, `fragments.append(("", right_text))`) {
		t.Error("expected original right_text append to be preserved")
	}
}

func TestValidatePatchFailsOnBadSyntax(t *testing.T) {
	tmpDir := t.TempDir()
	promptDir := filepath.Join(tmpDir, "ui", "shell")
	os.MkdirAll(promptDir, 0755)
	promptPath := filepath.Join(promptDir, "prompt.py")
	// Broken Python that won't compile after patch insertion
	os.WriteFile(promptPath, []byte("class ShellPrompt:\n    def _render_bottom_toolbar(self):\n        fragments = [\n        right_text = self._render_right_span(status)\n        right_width = _display_width(right_text)\n        return FormattedText(fragments)\n"), 0644)

	xitHome := t.TempDir()
	// applyPatch should still insert its block, but py_compile should catch the broken syntax
	if err := ValidatePatch(promptPath, xitHome); err == nil {
		t.Fatal("expected validate to fail on broken syntax")
	}
}

func TestPatchSecondLineLeftPlacement(t *testing.T) {
	// Realistic Kimi prompt structure with toast block and spacer
	prompt := `class ShellPrompt:
    def _render_bottom_toolbar(self):
        fragments = []
        fragments.append(("", "\n"))
        right_text = self._render_right_span(status)
        right_width = _display_width(right_text)
        left_toast = _current_toast("left")
        if left_toast is not None:
            max_left = max(0, columns - right_width - 2)
            if max_left > 0:
                left_text = left_toast.message
                left_width = _display_width(left_text)
                fragments.append(("", left_text))
            else:
                left_width = 0
        else:
            left_width = 0
        fragments.append(("", " " * max(0, columns - left_width - right_width)))
        fragments.append(("", right_text))
        return FormattedText(fragments)
`
	patched, err := applyPatch(prompt, "/tmp/xit")
	if err != nil {
		t.Fatalf("applyPatch failed: %v", err)
	}
	// XiT fragment should be before spacer (second-line left)
	if !strings.Contains(patched, `fragments.append(("fg:#D4A017 bold", _xit_status_text))`) {
		t.Error("expected XiT styled fragment in patched output")
	}
	// Should set left_width to account for XiT width (initialized to 0 before block)
	if !strings.Contains(patched, "left_width = _xit_status_width + 2") {
		t.Error("expected left_width adjustment for XiT spacer")
	}
	// Should add gap fragment after XiT
	if !strings.Contains(patched, `fragments.append(("", "  "))`) {
		t.Error("expected gap fragment after XiT status")
	}
	// Should be before spacer (second-line left), not inside toast block
	lines := strings.Split(patched, "\n")
	var xitIdx, spacerIdx int
	for i, line := range lines {
		if strings.Contains(line, `fragments.append(("fg:#D4A017 bold", _xit_status_text))`) {
			xitIdx = i
		}
		if strings.Contains(line, `fragments.append(("", " " * max(0, columns - left_width - right_width))`) {
			spacerIdx = i
		}
	}
	if xitIdx == 0 {
		t.Fatal("XiT fragment not found")
	}
	if spacerIdx == 0 {
		t.Fatal("spacer not found")
	}
	if xitIdx > spacerIdx {
		t.Error("XiT fragment should be BEFORE spacer")
	}
}

func TestPatchRawLogOnlyInCompletedRotation(t *testing.T) {
	h := helperFunction("/tmp/xit")
	// raw_log 已留证 should only appear in turn_completed_result rotation, not in ready/guarding/running
	if !strings.Contains(h, "raw_log 已留证") {
		t.Error("helper should contain raw_log 已留证 for completed rotation")
	}
	// Verify it appears in a candidates_text list context, not as a direct return in ready/running/guarding
	lines := strings.Split(h, "\n")
	inCandidates := false
	foundInCandidates := false
	for _, line := range lines {
		if strings.Contains(line, "candidates_text") {
			inCandidates = true
		}
		if inCandidates && strings.Contains(line, "raw_log 已留证") {
			foundInCandidates = true
		}
		if inCandidates && strings.Contains(line, "]") {
			inCandidates = false
		}
	}
	if !foundInCandidates {
		t.Error("raw_log 已留证 should appear inside candidates_text list for completed rotation")
	}
}

func TestPatchNoHistoryInToolbar(t *testing.T) {
	h := helperFunction("/tmp/xit")
	if strings.Contains(h, "历史吸T") {
		t.Error("helper should not contain lifetime history in default toolbar")
	}
}

func TestPatchNoEnglishMixedSessionSaved(t *testing.T) {
	h := helperFunction("/tmp/xit")
	if strings.Contains(h, "XiT ON · session saved") {
		t.Error("helper should not contain old English-mixed candidate 'XiT ON · session saved'")
	}
}

func TestPatchPreservesFirstLineMode(t *testing.T) {
	// Ensure patch does not insert XiT before first-line mode content
	prompt := `class ShellPrompt:
    def _render_bottom_toolbar(self):
        fragments = []
        fragments.append(("", "agent (model)  "))
        right_text = self._render_right_span(status)
        right_width = _display_width(right_text)
        fragments.append(("", right_text))
        return FormattedText(fragments)
`
	patched, err := applyPatch(prompt, "/tmp/xit")
	if err != nil {
		t.Fatalf("applyPatch failed: %v", err)
	}
	// XiT should be after right_width, not before first fragments.append
	lines := strings.Split(patched, "\n")
	var modeIdx, xitIdx int
	for i, line := range lines {
		if strings.Contains(line, `"agent (model)  "`) {
			modeIdx = i
		}
		if strings.Contains(line, `fragments.append(("fg:#D4A017 bold", _xit_status_text))`) {
			xitIdx = i
		}
	}
	if modeIdx == 0 {
		t.Fatal("mode fragment not found")
	}
	if xitIdx == 0 {
		t.Fatal("XiT fragment not found")
	}
	if xitIdx < modeIdx {
		t.Error("XiT should not be placed before first-line mode content")
	}
}

func TestHelperSessionStartedShowsReady(t *testing.T) {
	h := helperFunction("/tmp/xit")
	if !strings.Contains(h, `"session_started"`) {
		t.Error("expected 'session_started' status check in helper")
	}
	// session_started must map to 准备就绪
	lines := strings.Split(h, "\n")
	foundSessionStartedReady := false
	for i, line := range lines {
		if strings.Contains(line, `turn_status == "session_started"`) {
			// Check next few lines for 准备就绪 return
			for j := i + 1; j < len(lines) && j <= i+3; j++ {
				if strings.Contains(lines[j], "吸T神功 · Kimi · 准备就绪") {
					foundSessionStartedReady = true
					break
				}
			}
		}
	}
	if !foundSessionStartedReady {
		t.Error("expected session_started to return 吸T神功 · Kimi · 准备就绪 in helper")
	}
}

func TestHelperSessionStartedDoesNotShowGuarding(t *testing.T) {
	h := helperFunction("/tmp/xit")
	// session_started must NOT be in the thinking/active guarding block
	lines := strings.Split(h, "\n")
	for i, line := range lines {
		if strings.Contains(line, `turn_status in ("thinking", "active")`) {
			// Verify the line does NOT contain session_started
			if strings.Contains(line, "session_started") {
				t.Errorf("session_started should not be in thinking/active tuple at line %d: %s", i, line)
			}
			return
		}
	}
	t.Error("expected turn_status in ('thinking', 'active') guard in helper")
}

func TestComputeToolbarPreviewSessionStartedShowsReady(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, "turn.json"), []byte(`{"status":"session_started","event":"SessionStart","started_at":"2026-05-30T00:00:00Z"}`), 0644)

	preview := ComputeToolbarPreview(tmp)
	if preview.Preview != "吸T神功 · Kimi · 准备就绪" {
		t.Errorf("expected preview 吸T神功 · Kimi · 准备就绪 for session_started, got %s", preview.Preview)
	}
}

func TestComputeToolbarPreviewUserPromptSubmitShowsGuarding(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, "turn.json"), []byte(`{"status":"thinking","event":"UserPromptSubmit","started_at":"2026-05-30T00:00:00Z"}`), 0644)

	preview := ComputeToolbarPreview(tmp)
	if preview.Preview != "吸T神功 · Kimi · 守护你的T" {
		t.Errorf("expected preview 吸T神功 · Kimi · 守护你的T for thinking, got %s", preview.Preview)
	}
}

func TestComputeToolbarPreviewActiveShowsGuarding(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, "turn.json"), []byte(`{"status":"active","event":"","started_at":"2026-05-30T00:00:00Z"}`), 0644)

	preview := ComputeToolbarPreview(tmp)
	if preview.Preview != "吸T神功 · Kimi · 守护你的T" {
		t.Errorf("expected preview 吸T神功 · 守护你的T for active, got %s", preview.Preview)
	}
}

func TestComputeToolbarPreviewTokenFields(t *testing.T) {
	preview := ComputeToolbarPreview("/nonexistent")
	if preview.Unit != "token" {
		t.Errorf("expected unit token, got %s", preview.Unit)
	}
	if preview.TokenMethod != "saved_bytes / 4" {
		t.Errorf("expected token_method saved_bytes / 4, got %s", preview.TokenMethod)
	}
	if preview.ToolbarExample != "本次吸T1次 · 省9k Token" {
		t.Errorf("expected toolbar_example 本次吸T1次 · 省9k Token, got %s", preview.ToolbarExample)
	}
}

func TestComputeToolbarPreviewRotationInterval(t *testing.T) {
	preview := ComputeToolbarPreview("/nonexistent")
	if preview.RotationInterval != "5s" {
		t.Errorf("expected rotation_interval 5s, got %s", preview.RotationInterval)
	}
}

func TestComputeToolbarPreviewCompletedText(t *testing.T) {
	preview := ComputeToolbarPreview("/nonexistent")
	if preview.CompletedText != "吸T完成 · Kimi · 本次省9k Token" {
		t.Errorf("expected completed_text 吸T完成 · Kimi · 本次省9k Token, got %s", preview.CompletedText)
	}
}

func TestComputeToolbarPreviewAutoCompletedShowsTokens(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	now := time.Now().UTC()
	// 36035 bytes -> 9008 tokens -> 省9k Token
	state := fmt.Sprintf(`{"status":"completed","started_at":"","finished_at":"%s","saved_bytes":36035}`, now.Add(-5*time.Second).Format(time.RFC3339))
	os.WriteFile(filepath.Join(stateDir, "current.json"), []byte(state), 0644)

	preview := ComputeToolbarPreview(tmp)
	if preview.Preview != "吸T完成 · Kimi · 本次省9k Token" {
		t.Errorf("expected preview 吸T完成 · Kimi · 本次省9k Token, got %s", preview.Preview)
	}
}

func TestHelperUsesSavedBytesDividedByFour(t *testing.T) {
	h := helperFunction("/tmp/xit")
	if !strings.Contains(h, "saved_bytes // 4") {
		t.Error("expected 'saved_bytes // 4' in Python helper")
	}
}

func TestHelperDoesNotUseSavedBytesDividedBy1024(t *testing.T) {
	h := helperFunction("/tmp/xit")
	if strings.Contains(h, "// 1024") {
		t.Error("helper should not contain '// 1024' for saved display")
	}
}

func TestHelperDoesNotContainApproxOrKT(t *testing.T) {
	h := helperFunction("/tmp/xit")
	if strings.Contains(h, "省约") {
		t.Error("helper should not contain '省约'")
	}
	if strings.Contains(h, "kT") {
		t.Error("helper should not contain 'kT'")
	}
}

func TestHelperContainsTokenSuffix(t *testing.T) {
	h := helperFunction("/tmp/xit")
	if !strings.Contains(h, " Token") {
		t.Error("expected ' Token' suffix in helper")
	}
}
