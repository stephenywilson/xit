package codexhook

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFakeCodexConfig(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckTrustStatus_NoConfigFile(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	ts, err := CheckTrustStatus("/some/project")
	if err != nil {
		t.Fatal(err)
	}
	if ts.AllRecorded {
		t.Fatal("expected AllRecorded=false when no config.toml exists")
	}
}

func TestCheckTrustStatus_AllFourRecorded(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	projectPath := "/Users/example/myproject"
	hooksPath := filepath.Join(projectPath, ".codex", "hooks.json")
	content := `
[hooks.state]

[hooks.state."` + hooksPath + `:pre_tool_use:0:0"]
trusted_hash = "sha256:aaa"

[hooks.state."` + hooksPath + `:post_tool_use:0:0"]
trusted_hash = "sha256:bbb"

[hooks.state."` + hooksPath + `:user_prompt_submit:0:0"]
trusted_hash = "sha256:ccc"

[hooks.state."` + hooksPath + `:stop:0:0"]
trusted_hash = "sha256:ddd"
`
	writeFakeCodexConfig(t, fakeHome, content)

	ts, err := CheckTrustStatus(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if !ts.AllRecorded {
		t.Fatalf("expected all four events recorded, got %+v", ts)
	}
	for _, ev := range xitEvents() {
		if !ts.RecordedByEvent[ev.Name] {
			t.Errorf("expected %s recorded, got false", ev.Name)
		}
	}
}

func TestCheckTrustStatus_PartiallyRecorded(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	projectPath := "/Users/example/myproject"
	hooksPath := filepath.Join(projectPath, ".codex", "hooks.json")
	// Only PreToolUse recorded — simulating a partial/legacy trust state.
	content := `[hooks.state."` + hooksPath + `:pre_tool_use:0:0"]
trusted_hash = "sha256:aaa"
`
	writeFakeCodexConfig(t, fakeHome, content)

	ts, err := CheckTrustStatus(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if ts.AllRecorded {
		t.Fatal("expected AllRecorded=false for a partial trust state")
	}
	if !ts.RecordedByEvent["PreToolUse"] {
		t.Error("expected PreToolUse to be recorded")
	}
	if ts.RecordedByEvent["Stop"] {
		t.Error("expected Stop to NOT be recorded")
	}
}

// TestCheckTrustStatus_DifferentProjectNotConfused ensures a trust entry for
// a DIFFERENT project's hooks.json path never counts toward this project.
func TestCheckTrustStatus_DifferentProjectNotConfused(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	otherHooksPath := filepath.Join("/Users/example/other-project", ".codex", "hooks.json")
	content := `[hooks.state."` + otherHooksPath + `:pre_tool_use:0:0"]
trusted_hash = "sha256:aaa"
`
	writeFakeCodexConfig(t, fakeHome, content)

	ts, err := CheckTrustStatus("/Users/example/myproject")
	if err != nil {
		t.Fatal(err)
	}
	if ts.AllRecorded || ts.RecordedByEvent["PreToolUse"] {
		t.Fatalf("a different project's trust entry must not count, got %+v", ts)
	}
}
