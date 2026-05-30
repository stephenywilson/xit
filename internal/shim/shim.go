package shim

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/stephenywilson/xit/internal/config"
)

const Marker = "# XiT shim managed file"

func Status(home string, cfg *config.Config) string {
	var b strings.Builder
	b.WriteString("XiT Shim Status\n\n")
	for _, name := range []string{"kimi", "claude", "codex", "gemini", "cursor"} {
		t, ok := cfg.Targets[name]
		if !ok {
			continue
		}
		b.WriteString(fmt.Sprintf("%s:\n", name))
		if !t.Enabled {
			b.WriteString("  status: not configured\n")
			continue
		}
		shimPath := defaultShimPath(name)
		if t.ShimPath != "" {
			shimPath = t.ShimPath
		}
		original := t.OriginalPath
		if original == "" {
			original = t.Path
		}
		if original == "" {
			original = "not found"
		}
		b.WriteString(fmt.Sprintf("  enabled:  %v\n", t.ShimEnabled))
		b.WriteString(fmt.Sprintf("  original: %s\n", original))
		b.WriteString(fmt.Sprintf("  shim:     %s\n", shimPath))
		if t.Takeover {
			b.WriteString(fmt.Sprintf("  takeover: %v\n", t.Takeover))
			if t.BackupPath != "" {
				b.WriteString(fmt.Sprintf("  backup:   %s\n", t.BackupPath))
			}
		}
		if _, err := os.Stat(shimPath); err == nil {
			if IsManagedShim(shimPath) {
				if t.Takeover {
					b.WriteString("  status:   installed (XiT takeover)\n")
				} else {
					b.WriteString("  status:   installed (XiT)\n")
				}
			} else {
				b.WriteString("  status:   exists (not XiT)\n")
			}
		} else {
			b.WriteString("  status:   not installed\n")
		}
		if name == "kimi" {
			b.WriteString("  note:     Kimi TUI takeover is disabled by default for safety.\n")
			b.WriteString("            Use --force-unsafe-tui only for development testing.\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func Install(home string, cfg *config.Config, target string, yes bool, takeover bool) error {
	if !yes {
		return fmt.Errorf("shim install requires --yes to confirm. This will create a file in ~/.local/bin/%s", target)
	}
	if _, ok := cfg.Targets[target]; !ok {
		return fmt.Errorf("unknown target: %s", target)
	}
	t := cfg.Targets[target]
	if !t.Enabled {
		return fmt.Errorf("target %s is not initialized. Run: xit init %s", target, target)
	}
	// Find the real binary path, avoiding recursion.
	realPath := t.OriginalPath
	if realPath == "" {
		realPath = t.Path
	}
	if realPath == "" {
		return fmt.Errorf("cannot find original %s path. Run: xit init %s", target, target)
	}
	shimPath := defaultShimPath(target)
	if t.ShimPath != "" {
		shimPath = t.ShimPath
	}

	// Handle takeover when original path equals shim path.
	var backupPath string
	if realPath == shimPath {
		if !takeover {
			return fmt.Errorf("original %s path is the same as shim path (%s). Use --takeover to safely back up and replace the original", target, shimPath)
		}
		// Ensure the existing file is not already a XiT shim.
		if IsManagedShim(shimPath) {
			return fmt.Errorf("shim path %s is already a XiT shim. No takeover needed", shimPath)
		}
		backupPath = findBackupPath(shimPath)
		if err := os.Rename(shimPath, backupPath); err != nil {
			return fmt.Errorf("cannot back up original %s to %s: %w", target, backupPath, err)
		}
		// After moving, the real binary is now at backupPath.
		realPath = backupPath
	}

	// Check if something already exists at shimPath.
	if info, err := os.Stat(shimPath); err == nil {
		if info.IsDir() {
			return fmt.Errorf("shim path %s is a directory", shimPath)
		}
		if !IsManagedShim(shimPath) {
			return fmt.Errorf("shim path %s already exists and is not a XiT shim. Please remove it manually first", shimPath)
		}
	}

	// Ensure ~/.local/bin exists.
	if err := os.MkdirAll(filepath.Dir(shimPath), 0755); err != nil {
		return fmt.Errorf("cannot create shim directory: %w", err)
	}

	// Use absolute xit path in shim to avoid PATH instability.
	xitBin := detectXitBin()
	script := GenerateScript(target, realPath, xitBin)
	if err := os.WriteFile(shimPath, []byte(script), 0755); err != nil {
		return fmt.Errorf("cannot write shim: %w", err)
	}

	t.ShimEnabled = true
	t.ShimPath = shimPath
	t.OriginalPath = realPath
	t.Integration = "shim"
	if takeover {
		t.Takeover = true
		t.BackupPath = backupPath
	}
	cfg.Targets[target] = t
	if err := config.Save(home, cfg); err != nil {
		return fmt.Errorf("cannot save config: %w", err)
	}
	return nil
}

func Remove(home string, cfg *config.Config, target string) error {
	if _, ok := cfg.Targets[target]; !ok {
		return fmt.Errorf("unknown target: %s", target)
	}
	t := cfg.Targets[target]
	shimPath := t.ShimPath
	if shimPath == "" {
		shimPath = defaultShimPath(target)
	}
	if _, err := os.Stat(shimPath); err != nil {
		return fmt.Errorf("shim not found at %s", shimPath)
	}
	if !IsManagedShim(shimPath) {
		return fmt.Errorf("refusing to remove %s: not a XiT managed shim", shimPath)
	}

	// If takeover was used, restore the original from backup.
	if t.Takeover {
		if t.BackupPath == "" {
			return fmt.Errorf("cannot remove shim: backup path is missing. Refusing to delete %s to avoid losing the original command", shimPath)
		}
		if _, err := os.Stat(t.BackupPath); err != nil {
			return fmt.Errorf("cannot remove shim: backup file not found at %s. Refusing to delete %s", t.BackupPath, shimPath)
		}
		if err := os.Remove(shimPath); err != nil {
			return fmt.Errorf("cannot remove shim: %w", err)
		}
		if err := os.Rename(t.BackupPath, shimPath); err != nil {
			return fmt.Errorf("cannot restore original from %s to %s: %w", t.BackupPath, shimPath, err)
		}
		// Ensure restored file is executable.
		os.Chmod(shimPath, 0755)
		t.ShimEnabled = false
		t.Takeover = false
		t.Integration = "wrapper"
		cfg.Targets[target] = t
		if err := config.Save(home, cfg); err != nil {
			return fmt.Errorf("cannot save config: %w", err)
		}
		return nil
	}

	// Standard remove without takeover restore.
	if err := os.Remove(shimPath); err != nil {
		return fmt.Errorf("cannot remove shim: %w", err)
	}
	t.ShimEnabled = false
	t.Integration = "wrapper"
	cfg.Targets[target] = t
	if err := config.Save(home, cfg); err != nil {
		return fmt.Errorf("cannot save config: %w", err)
	}
	return nil
}

func IsManagedShim(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.HasPrefix(string(data), Marker)
}

func GenerateScript(target, originalPath, xitBin string) string {
	return fmt.Sprintf(`%s
# target: %s
# original: %s
exec %s %s "$@"
`, Marker, target, originalPath, xitBin, target)
}

func detectXitBin() string {
	if p, err := exec.LookPath("xit"); err == nil {
		return p
	}
	if p, err := exec.LookPath(os.Args[0]); err == nil {
		return p
	}
	return "xit"
}

func findBackupPath(shimPath string) string {
	base := shimPath + ".xit-original"
	if _, err := os.Stat(base); os.IsNotExist(err) {
		return base
	}
	for i := 1; i <= 10; i++ {
		p := fmt.Sprintf("%s.%d", base, i)
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p
		}
	}
	return base + ".xit-original.overflow"
}

var homeDirFn = os.UserHomeDir

func defaultShimPath(target string) string {
	home, err := homeDirFn()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".local", "bin", target)
}
