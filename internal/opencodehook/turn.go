package opencodehook

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

type TurnState struct {
	TurnKey          string `json:"turn_key"`
	RunCount         int    `json:"run_count"`
	SavedTokensTotal int    `json:"saved_tokens_total"`
	UpdatedAt        string `json:"updated_at"`
}

const turnStateTTL = 24 * time.Hour

func turnStateRoot(home string) string {
	return filepath.Join(home, "state", "opencode-turns")
}

// MakeTurnKey returns the stable, irreversible OpenCode turn key used by the
// plugin and CLI. It intentionally does not expose the raw session/message IDs.
func MakeTurnKey(sessionID, userMessageID string) string {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(userMessageID) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(sessionID + "\x00" + userMessageID))
	return fmt.Sprintf("%x", sum[:])[:24]
}

func safeIDPart(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 80 {
			break
		}
	}
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%s-%x", strings.Trim(b.String(), "."), sum[:4])
}

func turnKeyDir(home string) string {
	return turnStateRoot(home)
}

func turnStatePath(home, turnKey string) string {
	return filepath.Join(turnKeyDir(home), safeIDPart(turnKey)+".json")
}

func turnLockPath(home, turnKey string) string {
	return filepath.Join(turnKeyDir(home), safeIDPart(turnKey)+".lock")
}

func withTurnLock(home, turnKey string, fn func() error) error {
	if err := os.MkdirAll(turnKeyDir(home), 0755); err != nil {
		return fn()
	}
	lockPath := turnLockPath(home, turnKey)
	deadline := time.Now().Add(2 * time.Second)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			_ = f.Close()
			defer os.Remove(lockPath)
			return fn()
		}
		if time.Now().After(deadline) {
			return fn()
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func readTurnStateRaw(home, turnKey string) *TurnState {
	data, err := os.ReadFile(turnStatePath(home, turnKey))
	if err != nil {
		return nil
	}
	var st TurnState
	if json.Unmarshal(data, &st) != nil {
		return nil
	}
	return &st
}

func writeTurnStateRaw(home string, st *TurnState) error {
	if err := os.MkdirAll(turnKeyDir(home), 0755); err != nil {
		return err
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	path := turnStatePath(home, st.TurnKey)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func CleanupExpiredTurnStates(home string, now time.Time) {
	root := turnStateRoot(home)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var st TurnState
		if json.Unmarshal(data, &st) != nil {
			return nil
		}
		updatedAt, err := time.Parse(time.RFC3339, st.UpdatedAt)
		if err != nil || now.Sub(updatedAt) > turnStateTTL {
			_ = os.Remove(path)
		}
		return nil
	})
}

func IncrementTurnState(home, turnKey string, savedTokens int) (*TurnState, error) {
	if turnKey == "" {
		return nil, nil
	}
	CleanupExpiredTurnStates(home, time.Now())
	var result *TurnState
	err := withTurnLock(home, turnKey, func() error {
		st := readTurnStateRaw(home, turnKey)
		if st == nil {
			st = &TurnState{TurnKey: turnKey}
		}
		st.RunCount++
		st.SavedTokensTotal += savedTokens
		st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		result = st
		return writeTurnStateRaw(home, st)
	})
	return result, err
}

func ReadTurnState(home, sessionID, userMessageID string) *TurnState {
	return ReadTurnStateByKey(home, MakeTurnKey(sessionID, userMessageID))
}

func ReadTurnStateByKey(home, turnKey string) *TurnState {
	if turnKey == "" {
		return nil
	}
	st := readTurnStateRaw(home, turnKey)
	if st == nil || st.TurnKey != turnKey {
		return nil
	}
	return st
}

func TurnStateFiles(home string) []string {
	var files []string
	root := turnStateRoot(home)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && d != nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".json") {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files
}
