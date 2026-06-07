package opencodehook

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// RunHookCommand is the entrypoint for `xit opencode-hook log-event`.
// It reads a JSON record from stdin and appends it to events.jsonl.
func RunHookCommand(home string) error {
	logDir := filepath.Join(home, "opencode-hooks")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil
	}

	logPath := filepath.Join(logDir, "events.jsonl")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(os.Stdin)
	var input []byte
	for scanner.Scan() {
		input = append(input, scanner.Bytes()...)
	}
	if err := scanner.Err(); err != nil {
		return nil
	}

	var rec map[string]interface{}
	if err := json.Unmarshal(input, &rec); err != nil {
		return nil
	}
	if _, ok := rec["timestamp"]; !ok {
		rec["timestamp"] = time.Now().UTC().Format(time.RFC3339)
	}
	data, _ := json.Marshal(rec)
	f.WriteString(string(data) + "\n")
	return nil
}

// XiTHome returns the XiT home directory.
func XiTHome() string {
	if v := os.Getenv("XIT_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".xit")
	}
	return filepath.Join(home, ".xit")
}
