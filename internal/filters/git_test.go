package filters

import (
	"strings"
	"testing"

	"github.com/stephenywilson/xit/internal/runner"
)

func TestFilterGitStatus(t *testing.T) {
	input := `## main...origin/main
 M cmd/xit/main.go
 M internal/filters/git.go
?? README.md
A  newfile.txt
`
	res := &runner.Result{Stdout: []byte(input), ExitCode: 0, DurationMs: 10}
	s, err := filterGit([]string{"git", "status"}, res)
	if err != nil {
		t.Fatal(err)
	}
	if s.KeyFacts["branch"] != "main" {
		t.Errorf("branch = %v, want main", s.KeyFacts["branch"])
	}
	if s.KeyFacts["staged"].(int) != 1 {
		t.Errorf("staged = %v, want 1", s.KeyFacts["staged"])
	}
	if s.KeyFacts["unstaged"].(int) != 2 {
		t.Errorf("unstaged = %v, want 2", s.KeyFacts["unstaged"])
	}
	if s.KeyFacts["untracked"].(int) != 1 {
		t.Errorf("untracked = %v, want 1", s.KeyFacts["untracked"])
	}
}

func TestFilterGitDiff(t *testing.T) {
	input := `diff --git a/auth/login.go b/auth/login.go
index abc..def 100644
--- a/auth/login.go
+++ b/auth/login.go
@@ -10,5 +10,6 @@ func Login() {
+    new code
-    old code
}
diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,2 +1,3 @@
+import "fmt"
`
	res := &runner.Result{Stdout: []byte(input), ExitCode: 0, DurationMs: 10}
	s, err := filterGit([]string{"git", "diff"}, res)
	if err != nil {
		t.Fatal(err)
	}
	if s.KeyFacts["files_changed"].(int) != 2 {
		t.Errorf("files_changed = %v, want 2", s.KeyFacts["files_changed"])
	}
	if s.KeyFacts["additions"].(int) != 2 {
		t.Errorf("additions = %v, want 2", s.KeyFacts["additions"])
	}
	if s.KeyFacts["deletions"].(int) != 1 {
		t.Errorf("deletions = %v, want 1", s.KeyFacts["deletions"])
	}
	foundRisk := false
	for _, f := range s.FileList {
		if strings.Contains(f, "auth") {
			foundRisk = true
		}
	}
	if !foundRisk {
		t.Error("expected high-risk file auth/login.go in FileList")
	}
}
