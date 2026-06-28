package codexhook

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

// TurnState is the per-turn accumulator for Codex. A "turn" = one user prompt
// (one UserPromptSubmit -> ... -> one Stop). Multiple `xit auto` calls within
// the same turn accumulate into RunCount/SavedTokensTotal; a new turn (new
// turn_id, or no marker-continuation) resets both to zero. Only counters and
// identifiers are persisted — never raw command output, raw_log paths, full
// transcripts, or user prompt text.
type TurnState struct {
	SessionID              string `json:"session_id"`
	TurnID                 string `json:"turn_id"`
	RunCount               int    `json:"run_count"`
	SavedTokensTotal       int    `json:"saved_tokens_total"`
	FooterContinuationUsed bool   `json:"footer_continuation_used"`
	UpdatedAt              string `json:"updated_at"`
}

const turnStateTTL = 24 * time.Hour

func turnStateRoot(home string) string {
	return filepath.Join(home, "state", "codex-turns")
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

func turnSessionDir(home, sessionID string) string {
	return filepath.Join(turnStateRoot(home), safeIDPart(sessionID))
}

func turnStatePath(home, sessionID, turnID string) string {
	return filepath.Join(turnSessionDir(home, sessionID), safeIDPart(turnID)+".json")
}

func turnLockPath(home, sessionID string) string {
	return filepath.Join(turnSessionDir(home, sessionID), ".lock")
}

// withTurnLock runs fn while holding an exclusive filesystem lock on the turn
// state file, so concurrent `xit auto` calls within the same turn never lose
// an increment (no last-write-wins). Uses a dependency-free O_CREATE|O_EXCL
// lockfile with retry; fails open (runs fn without the lock) if the lock
// cannot be acquired within the timeout, so a stuck/stale lock can never
// permanently block the user's command.
func withTurnLock(home, sessionID string, fn func() error) error {
	if err := os.MkdirAll(turnSessionDir(home, sessionID), 0755); err != nil {
		return fn()
	}
	lockPath := turnLockPath(home, sessionID)
	deadline := time.Now().Add(2 * time.Second)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			_ = f.Close()
			defer os.Remove(lockPath)
			return fn()
		}
		if time.Now().After(deadline) {
			// Fail-open: never block the user's command indefinitely on a
			// stuck lock (e.g. a crashed prior process left it behind).
			return fn()
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func readTurnStateRaw(home, sessionID, turnID string) *TurnState {
	data, err := os.ReadFile(turnStatePath(home, sessionID, turnID))
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
	if err := os.MkdirAll(turnSessionDir(home, st.SessionID), 0755); err != nil {
		return err
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	path := turnStatePath(home, st.SessionID, st.TurnID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readLatestSessionTurnState(home, sessionID string) *TurnState {
	dir := turnSessionDir(home, sessionID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var states []*TurnState
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var st TurnState
		if json.Unmarshal(data, &st) == nil && st.SessionID == sessionID {
			states = append(states, &st)
		}
	}
	sort.Slice(states, func(i, j int) bool { return states[i].UpdatedAt > states[j].UpdatedAt })
	if len(states) == 0 {
		return nil
	}
	return states[0]
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

// ReadTurnState returns the current turn state for sessionID, or nil if none
// exists or it belongs to a different session.
func ReadTurnState(home, sessionID, turnID string) *TurnState {
	if sessionID == "" {
		return nil
	}
	var st *TurnState
	if turnID != "" {
		st = readTurnStateRaw(home, sessionID, turnID)
	}
	if st == nil {
		st = readLatestSessionTurnState(home, sessionID)
	}
	if st == nil || st.SessionID != sessionID {
		return nil
	}
	return st
}

// FooterContinuationMarker is embedded in the Stop "block" reason text and
// checked by UserPromptSubmit to recognize a Codex-triggered continuation
// (so it does not reset the turn that is still being finished).
//
// Built entirely from zero-width Unicode characters (no visible glyphs) so
// that when Codex echoes/resubmits the blocked reason text, a user reading
// the chat transcript never sees an internal-looking literal token — the
// marker is preserved byte-for-byte for our own substring matching but
// renders as nothing.
const FooterContinuationMarker = "\u200b\u200c\u200d\u2060\u200b\u200c\u200d\u2060"

// ResetTurnForPrompt handles UserPromptSubmit: starts a new turn (RunCount=0,
// SavedTokensTotal=0) unless prompt carries FooterContinuationMarker (a
// Stop-triggered continuation of the turn that is still finishing) or the
// turn_id is identical to the one already recorded (idempotent — Codex must
// not be able to wipe real counts by re-delivering the same event).
func ResetTurnForPrompt(home, sessionID, turnID, prompt string) (*TurnState, error) {
	var result *TurnState
	CleanupExpiredTurnStates(home, time.Now())
	err := withTurnLock(home, sessionID, func() error {
		if strings.Contains(prompt, FooterContinuationMarker) {
			// Continuation of the in-progress turn: do not touch existing state.
			result = readLatestSessionTurnState(home, sessionID)
			return nil
		}
		existing := readTurnStateRaw(home, sessionID, turnID)
		if existing != nil && existing.SessionID == sessionID && existing.TurnID == turnID && turnID != "" {
			// Same turn re-delivered (e.g. duplicate event): no-op, keep counts.
			result = existing
			return nil
		}
		fresh := &TurnState{
			SessionID: sessionID,
			TurnID:    turnID,
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		result = fresh
		return writeTurnStateRaw(home, fresh)
	})
	return result, err
}

// IncrementTurnState atomically adds one run + savedTokens to the turn
// identified by sessionID/turnID. If no turn state exists yet (e.g.
// UserPromptSubmit was not installed/triggered), it is lazily created
// (fail-open) so `xit auto` accounting never silently disappears.
func IncrementTurnState(home, sessionID, turnID string, savedTokens int) (*TurnState, error) {
	var result *TurnState
	CleanupExpiredTurnStates(home, time.Now())
	err := withTurnLock(home, sessionID, func() error {
		st := readTurnStateRaw(home, sessionID, turnID)
		if st == nil {
			st = &TurnState{SessionID: sessionID, TurnID: turnID}
		}
		if turnID != "" {
			st.TurnID = turnID
		}
		st.RunCount++
		st.SavedTokensTotal += savedTokens
		st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		result = st
		return writeTurnStateRaw(home, st)
	})
	return result, err
}

// MarkFooterContinuationUsed records that a Stop "block" continuation has already
// been issued once for this turn, so a second Stop call can never block
// again (loop prevention) regardless of stop_hook_active.
func MarkFooterContinuationUsed(home, sessionID, turnID string) error {
	return withTurnLock(home, sessionID, func() error {
		st := ReadTurnState(home, sessionID, turnID)
		if st == nil {
			return nil
		}
		st.FooterContinuationUsed = true
		st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		return writeTurnStateRaw(home, st)
	})
}

// CleanupTurnState removes the turn state file once the footer has been
// confirmed in the final answer (or the turn is abandoned). Never keeps
// long-lived history — only the single active turn is ever persisted.
func CleanupTurnState(home, sessionID, turnID string) error {
	return withTurnLock(home, sessionID, func() error {
		st := ReadTurnState(home, sessionID, turnID)
		if st == nil {
			return nil
		}
		err := os.Remove(turnStatePath(home, st.SessionID, st.TurnID))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	})
}

// formatCodexFooterTokens mirrors the global token-display standard used by
// every other XiT surface: >=1000 -> "约 X.XXk Token" (two decimals, 约
// prefix); <1000 -> "N Token". Duplicated here (rather than imported from
// cmd/xit) because internal/codexhook cannot import the main package.
func formatCodexFooterTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("约 %.2fk Token", float64(n)/1000)
	}
	return fmt.Sprintf("%d Token", n)
}

// BuildFooterLines returns the exact two-line XiT footer for a turn with
// real accumulated savings. Only call when st.RunCount > 0.
func BuildFooterLines(st *TurnState) (string, string) {
	line1 := "吸T神功 · Codex · 守护你的T"
	line2 := fmt.Sprintf("本次省 %s · 本轮共吸 %d次", formatCodexFooterTokens(st.SavedTokensTotal), st.RunCount)
	return line1, line2
}

// LastMessageHasFooter reports whether the assistant's last message already
// contains the exact two-line footer for this turn.
func LastMessageHasFooter(st *TurnState, lastAssistantMessage string) bool {
	if st == nil {
		return false
	}
	line1, line2 := BuildFooterLines(st)
	return strings.Contains(lastAssistantMessage, line1) && strings.Contains(lastAssistantMessage, line2)
}
