package autoshim

import (
	"testing"
)

func TestShouldCompressSmallOutput(t *testing.T) {
	if ShouldCompress("git", []string{"status"}, 100, 0) {
		t.Error("small git status should passthrough")
	}
}

func TestShouldCompressLargeOutput(t *testing.T) {
	if !ShouldCompress("git", []string{"diff"}, 5000, 0) {
		t.Error("large git diff should compress")
	}
}

func TestShouldCompressMachineReadable(t *testing.T) {
	if ShouldCompress("git", []string{"log", "--format=json"}, 5000, 0) {
		t.Error("machine-readable output should passthrough")
	}
	if ShouldCompress("docker", []string{"ps", "--format", "json"}, 5000, 0) {
		t.Error("docker --format json should passthrough")
	}
}

func TestShouldCompressFailedCommand(t *testing.T) {
	if !ShouldCompress("go", []string{"test"}, 200, 1) {
		t.Error("failed test with any stderr should compress")
	}
}

func TestShouldCompressGitDiff(t *testing.T) {
	if !ShouldCompress("git", []string{"diff"}, 50, 0) {
		t.Error("git diff should always compress even if tiny")
	}
}

func TestShouldCompressGoTest(t *testing.T) {
	if !ShouldCompress("go", []string{"test"}, 50, 0) {
		t.Error("go test should always compress")
	}
}

func TestShouldCompressGrep(t *testing.T) {
	if !ShouldCompress("grep", []string{"-r", "func"}, 100, 0) {
		t.Error("grep should compress even small output")
	}
}

func TestShouldCompressJQ(t *testing.T) {
	if ShouldCompress("jq", []string{"."}, 5000, 0) {
		t.Error("jq should passthrough by default")
	}
}

func TestShouldCompressNPMInstall(t *testing.T) {
	if ShouldCompress("npm", []string{"install"}, 5000, 0) {
		t.Error("npm install should passthrough")
	}
}

func TestShouldCompressNPMTest(t *testing.T) {
	if !ShouldCompress("npm", []string{"test"}, 50, 0) {
		t.Error("npm test should compress")
	}
}
